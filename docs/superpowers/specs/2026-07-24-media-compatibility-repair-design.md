# Media Compatibility Repair Design

**Status:** Approved handoff. The decisions in this document are binding. The implementation agent must not reopen product decisions or ask for another interview.

## Purpose

Finish the media compatibility repair on `feature/film-manage` without changing the normalized aggregate model or the completed migration work. The repair restores playback, deletion, NFO, discovery, driver, and projection behavior while keeping the new `FilmWork`, `FilmFile`, and `SourceMagnet` model.

## Current Handoff Position

This handoff starts from a dirty working tree. Do not commit, stage, switch branches, fetch, clean, create a worktree, or overwrite unrelated user changes. No commits are allowed unless a human explicitly requests them.

### Completed Task 1, aggregate persistence invariants

The following modified source files are already complete and approved. They are prerequisites, not work to repeat or revert:

```text
internal/db/media.go
internal/db/media_test.go
internal/db/source_magnet.go
internal/db/source_magnet_test.go
```

Completed behavior:

1. `PrimaryDir` is immutable after the first `FilmWork` write.
2. Actors and tags are trimmed, empty values are discarded, and values are stable-deduped with existing values first.
3. A new work starts NFO-stale. A rediscovery increments `MetadataVersion` only when an NFO-projected value changes. `SourceRef`, `SourceURL`, image-only changes, and raw-title changes hidden by a translated title do not make NFO stale.
4. A selected source-magnet fingerprint survives rediscovery. New rows do not replace the stored selection merely because incoming discovery marks one selected.

### Completed Task 2, migration compatibility

The following modified and untracked source files are already complete and approved. They are prerequisites, not work to repeat or revert:

```text
internal/migration/media/artifact_test.go
internal/migration/media/database.go
internal/migration/media/database_test.go
internal/migration/media/compatibility.go
internal/migration/media/file_ids.go
internal/migration/media/timestamp_test.go
```

Completed behavior:

1. Migration keeps the first legacy directory and first canonical source URL, then stable-unions actors and tags from later compatible rows. A later directory contributes an actor, not another directory relation.
2. A plain work with distinct source paths fails before writes. Multipart work must have contiguous, unambiguous parts.
3. Migration preserves legacy file IDs when unambiguous, deterministically allocates synthetic IDs when needed, preserves source path, compatibility size, and timestamps, and validates that projection after apply.
4. Cloud-cache aliases use canonical media file names while preserving the existing remote handle, option map, subtitle data, scan time, and scan count. Alias creation is idempotent and fails before writes on a conflict.
5. Artifact migration uses the canonical aggregate root and safely folds compatible multipart artifacts into it.

The current focused verification is already green:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./internal/migration/media ./cmd/migrate-media ./internal/db
```

## Scope and Non-Goals

In scope:

1. Ordered same-provider magnet playback.
2. Aggregate deletion, cloud-cache cleanup, and artifact-root cleanup.
3. Stale-only and forced-full NFO synchronization.
4. FC2 tombstones, multipart discovery, persisted magnets, and failure continuation.
5. JavDB filtering, asynchronous enrichment, subtitle metadata, and NFO synchronization.
6. Pornhub playback and artifact request headers, mocked fallback, and metadata projection.
7. A stable normalized projection contract.
8. Targeted checks, race and vet checks, repository baseline classification, and review-work.

Out of scope:

1. Do not add `StorageID` to artifact paths.
2. Do not add a work-directory relation table or represent a work in multiple directories.
3. Remove `BackPlayDriverType` from driver configuration and every production path. Do not add a second cloud driver fallback.
4. Do not make JavDB enrichment synchronous.
5. Do not restore `Url = SourceRef` compatibility for old viewkey consumers.
6. Do not expand migration CLI output with skipped IDs or reasons.
7. Do not change completed Task 1 or Task 2 behavior.
8. Do not alter existing review reports or the original implementation plan.

## Domain and Persistence Rules

### Aggregate identity and location

`FilmWork` remains the aggregate root. Its database identity is `(StorageID, Source, Code)`. `FilmFile` belongs to exactly one work and carries a contiguous multipart topology. `SourceMagnet` belongs to exactly one work and carries the candidate URI, stable fingerprint, provider, priority, selection, subtitle flag, scan time, and last error.

The disk root is always:

```text
data/emby/{source}/{PrimaryDir}/{code}
```

It deliberately contains no `StorageID`. This is a product compatibility rule, including its collision behavior. Never add a reference count, last-reference query, or directory relation before removing this root.

`PrimaryDir` is the first-write directory and never changes. Actors and tags accumulate as stable, trimmed, non-empty unions. A later directory is represented as an actor only when migration needs to preserve that legacy placement.

### Source fields and public projection

Normalized media keeps both source fields:

| Field | Meaning |
| --- | --- |
| `SourceRef` | Source-specific lookup key, such as a Pornhub viewkey. |
| `SourceURL` | Canonical source page URL. |
| projected `Url` | The canonical `SourceURL`. |

`Url` is never a compatibility alias for `SourceRef`. Callers that need a source lookup key must consume `SourceRef`. No old viewkey-only object compatibility is promised.

### Metadata versions

`MetadataVersion` tracks changes that affect an NFO projection. `NfoVersion` is the metadata version successfully written to disk. A work is stale when `MetadataVersion > NfoVersion`.

The NFO-visible fields are the effective display title, synopsis, release date, stable actors, and stable tags, including the subtitle tag. Source lookup fields and image-only changes are not NFO changes. A failed NFO write records `NfoLastError` and leaves the work stale. A successful write records the snapshot version and clears `NfoLastError`.

## Required Data Flows

### Discovery and enrichment

1. A driver normalizes a source record to `Source`, `Code`, `SourceRef`, `SourceURL`, and its first directory.
2. Discovery stores the basic `FilmWork` and a valid file topology. It must preserve Task 1 aggregate invariants and must not write NFO, poster, fanart, or other media artifacts directly.
3. JavDB and Pornhub discovery end here. Translation, synopsis, actors, tags, magnets, subtitles, samples, posters, and NFO files are eventual scheduled work. FC2 favorite discovery also persists database state only; its scheduled maintenance publishes artifacts later. The first list may show basic metadata.
4. A single bad item must not discard good results from the same scan. FC2 category pagination has no fixed page-count cap: it keeps the original ID-growth boundary and stops when a successfully fetched page contributes no new IDs. A page fetch failure logs the error, ends that pagination run, and preserves all IDs already accumulated for database processing.
5. A metadata change that affects NFO makes the work stale. It does not synchronously rewrite NFO unless the explicit synchronization operation runs.

### Playback

1. Resolve the requested `FilmFileWithWork` and build its canonical filename, `{code}.mp4` or `{code}-cdN.mp4`.
2. Query the configured cloud provider's `MagnetCache` row by canonical filename first. This row is a shared index of an already-downloaded 115/PikPak remote file ID, not a source-magnet cache or a local artifact. A valid remote link returns without source-magnet discovery or download. Matching `main`, a stale remote handle is logged and retained while playback falls through to source-magnet attempts. If the new offline download succeeds, `CreateMagnetCache` replaces the old row by provider and canonical filename; if redownload fails, the old row remains.
3. If the cache misses, obtain persisted `SourceMagnet` rows ordered selected first, then priority, then ID. FC2 must use these persisted rows before querying Suke.
4. If a source must be queried, persist the complete discovered magnet set before attempting playback, then reload the ordered rows. FC2 therefore persists magnets before Suke is used for a future playback attempt.
5. Attempt distinct magnets with the one configured cloud provider. On a failed attempt, record that candidate's `LastError` and continue with the next distinct fingerprint. On success, select that magnet atomically and return its link.
6. `BackPlayDriverType` and every read of it are removed. Do not switch cloud providers. A configured mocked link may remain the final driver-level fallback only where that driver already exposes that product option.
7. When every magnet fails, return the accumulated playback failure unless the driver's explicit mocked-link fallback applies.

### Aggregate deletion

Deleting a group, one media file, a filtered work, or an FC2 tombstoned work means deleting the whole aggregate.

1. Resolve the work and its immutable artifact identity before deletion.
2. Remove the entire artifact root unconditionally. Do not check whether any other record could map to the root.
3. In one database transaction, delete the work's source magnets, all of its files, all `MagnetCache` rows whose `Code` equals `work.Code` (the same predicate as `DeleteAllMagnetCacheByCode`), and the work row. `MagnetCache` is deliberately shared by code across storages and sources, just like the artifact root; only one copy is retained and aggregate deletion removes that shared copy without a reference check.
4. Deleting any `FilmFile` resolves its `WorkID` and takes the same path. It never preserves sibling parts or repairs a reduced topology.
5. A missing root is successful cleanup. A filesystem or database failure returns an error, records the existing error context, and must not be hidden as success.

### NFO synchronization

`SyncNfo` and `RefreshNfo` are deliberately different operations.

1. FC2, JavDB, and Pornhub all expose both `SyncNfo` and `RefreshNfo` configuration fields.
2. `SyncNfo` reads only stale works for its storage and source, writes each NFO from the normalized aggregate, then records successful version state.
3. `RefreshNfo` reads all works for its storage and source and rewrites each NFO even when its versions match.
4. When both fields are enabled, scheduled maintenance runs forced-full refresh once and does not run the stale-only pass first.
5. Scheduled driver maintenance, not discovery, writes posters and other media artifacts from persisted aggregate fields. Source-specific request headers remain the driver's responsibility.
6. Each work is independent. A write failure records that work error and does not prevent later works from being processed.
7. Drivers call the shared normalized synchronization path. They must not revive legacy `Film`-based NFO reads or direct, scattered NFO writes after discovery or enrichment.

### FC2 discovery

1. Removing an FC2 work creates or keeps a tombstone for its code and removes the complete aggregate.
2. Non-favorite FC2 scans exclude tombstoned codes, so a deleted work does not reappear. Favorites preserve their existing product behavior.
3. A magnet manifest is only the source magnet's internal file list, currently available from Suke through `GetFiles()`. It is not a new persisted model. `GetMagnet()` and `GetFiles()` share the same lazy Suke detail-page fetch, so whenever FC2 already queries Suke for a magnet it must consume that candidate's file list before converting the result to persisted `SourceMagnet` rows. Use files larger than 100 MiB, in the existing deterministic name order, to create a contiguous `FilmFile` topology: one part becomes `{code}.mp4`; several parts become `{code}-cd1.mp4`, `{code}-cd2.mp4`, and so on. Ordinary category discovery still does not visit Suke solely to obtain topology and may begin with a singleton.
4. FC2 reads persisted source magnets before Suke. When persisted magnets are unavailable, Suke discovery stores candidates first, then playback uses the stored ordering.
5. A bad item is isolated. A failed category page ends the current ID-growth loop without discarding already accumulated IDs; there is no fixed maximum page count. MissAV retains its configured page behavior and continues after independent item failures.

### JavDB behavior

1. The configured `Filter` runs from scheduled maintenance and operates on normalized `FilmWork` data, not the legacy `Film` table.
2. A filtered work uses aggregate deletion, including cloud cache and artifacts.
3. Async JavDB enrichment updates source magnets, actors, tags, and subtitle status. `model.TagSubtitle` is present when the selected metadata reports subtitles.
4. Enrichment writes NFO-visible changes through the aggregate update methods so stale NFO state is visible to `SyncNfo`.
5. JavDB's scheduled maintenance continues after one work fails and records the existing retry state for that work.

### Pornhub behavior

1. Direct video links carry `Referer: d.ServerUrl`.
2. Scheduled poster downloads carry the same Referer. The generated background/fanart symlink performs no separate HTTP request.
3. The only Pornhub mock enable condition remains `d.Mocked && d.MockedLink != ""`; do not add another fallback field. When it is true, return the mock address with no dynamic resolution. Otherwise a video-resolution error is returned.
4. Normalized discovery projects the code prefix exactly once, keeps playlist tags, and uses the discovery directory as the actor fallback when a username is empty.
5. Pornhub still uses `SourceRef` for source lookup and `SourceURL` for the canonical page URL. Its projected `Url` remains canonical `SourceURL`.

## Error Handling Rules

1. Boundary failures are returned or recorded at the existing retry-state boundary. They are never converted to silent success.
2. Per-work, per-page, and per-item failures are isolated. Batch operations continue after an independent failure.
3. Playback failure records a candidate error and tries another distinct persisted magnet with the same provider. It never changes provider as a retry tactic.
4. A stale 115/PikPak remote handle does not immediately delete its shared `MagnetCache` row. It is logged and source-magnet attempts continue. A successful new download replaces the old provider-and-name row through `CreateMagnetCache`; a failed redownload leaves the old row intact. Aggregate deletion removes rows by shared code.
5. NFO failures preserve stale state and record `NfoLastError`. Later stale or forced runs retry.
6. Deletion never performs a last-reference check. The immutable path and aggregate cleanup contracts are trusted directly.

## Acceptance Criteria

1. Artifact paths are exactly `data/emby/{source}/{PrimaryDir}/{code}` without `StorageID`.
2. A selected persisted magnet is attempted first; a failed candidate causes same-provider attempts of different magnets; the successful one becomes selected; the `BackPlayDriverType` field and all of its paths are gone.
3. A cache hit by canonical media filename bypasses source lookup and download.
4. Deleting any one part removes all `FilmFile`, `SourceMagnet`, applicable cloud-cache, artifact-root, and `FilmWork` state without a last-reference query.
5. `SyncNfo` writes stale records only. `RefreshNfo` rewrites every normalized work. A write failure leaves the work stale and records the error.
6. FC2 deletion creates a tombstone, scans do not resurrect it, available Suke manifests create a contiguous topology without a new manifest model, category pagination retains its ID-growth boundary with no page cap, and independent item failures continue.
7. JavDB filtering executes in scheduled maintenance against normalized works. Discovery stays basic and asynchronous. Subtitle metadata marks NFO stale and is included on the next sync.
8. Pornhub video and image requests send Referer, mocked-link fallback retains its explicit behavior, and metadata has one code prefix, playlist tags, and an actor fallback.
9. Projection exposes `SourceRef` and canonical `SourceURL`, sets `Url` to canonical `SourceURL`, keeps file ID, size, creation time, and update time, and offers no `Url = SourceRef` compatibility promise.
10. Migration CLI output has no skipped-ID or skipped-reason expansion.
11. Targeted tests, race checks, vet, build, full baseline classification, and review-work complete with no unresolved task-local finding.
