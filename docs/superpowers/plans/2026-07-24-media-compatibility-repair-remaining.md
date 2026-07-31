# Media Compatibility Repair Remaining Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the approved media compatibility repair without changing the normalized aggregate design or the already-complete Task 1 and Task 2 migration work.

**Architecture:** `FilmWork` remains the aggregate root, `FilmFile` remains its contiguous topology, and `SourceMagnet` remains its persisted playback candidate set. Playback retries distinct magnets with one cloud provider, deletion removes the entire aggregate and its artifact root, and NFO synchronization derives from normalized records. Drivers own source-specific discovery and metadata, while `internal/db` and `drivers/virtual_file` enforce aggregate contracts.

**Tech Stack:** Go 1.25.4, GORM, SQLite package tests, OpenList drivers, virtual media artifact helpers, and the repository's existing test commands.

## Global Constraints

- Start from the existing dirty `feature/film-manage` tree. Preserve unrelated user changes.
- Do not commit, stage, switch branches, fetch, clean, create a worktree, or overwrite user files. A human must explicitly request any commit.
- Artifact paths remain `data/emby/{source}/{PrimaryDir}/{code}` without `StorageID`.
- Deletion never checks last references. Deleting any media file deletes the entire aggregate.
- `PrimaryDir` is first-write immutable. Actors and tags are trimmed, stable-deduped unions.
- FC2, JavDB, and Pornhub expose both NFO flags. `SyncNfo` is stale-only, `RefreshNfo` is forced-full, and when both are enabled only forced-full runs.
- JavDB enrichment is eventually consistent. Do not add synchronous enrichment to discovery or favorites.
- Playback uses one configured cloud provider and selected-first distinct-magnet retries. Remove the `BackPlayDriverType` field and every path that reads it.
- FC2 uses persisted magnets before Suke and persists discovered magnets before a later Suke-dependent playback.
- Internally, media keeps `SourceRef` and `SourceURL`; projected `Url` remains canonical `SourceURL`. Do not restore old viewkey compatibility through `Url`.
- Do not expand migration CLI output with skipped IDs or skipped reasons.
- FC2 category pagination has no page cap and retains the original ID-growth stop condition. A page failure preserves accumulated IDs and ends that pagination run; independent item failures continue.
- Do not add StorageID artifact paths, directory relation tables, a second-driver fallback, synchronous JavDB enrichment, `Url = SourceRef` compatibility, skipped-ID details, or last-reference deletion checks.

---

## Current Working Tree Prerequisites

### Completed Task 1, do not reimplement or revert

The following modified files already implement approved aggregate persistence behavior:

```text
internal/db/media.go
internal/db/media_test.go
internal/db/source_magnet.go
internal/db/source_magnet_test.go
```

- [x] `PrimaryDir` preserves first write.
- [x] actors and tags are stable-deduped, trimmed unions.
- [x] NFO versioning changes only for NFO-visible metadata.
- [x] source-magnet selection preserves the stored fingerprint across rediscovery.

### Completed Task 2, do not reimplement or revert

The following modified and untracked files already implement approved migration compatibility behavior:

```text
internal/migration/media/artifact_test.go
internal/migration/media/database.go
internal/migration/media/database_test.go
internal/migration/media/compatibility.go
internal/migration/media/file_ids.go
internal/migration/media/timestamp_test.go
```

- [x] first directory and first canonical URL are kept while actors and tags are merged.
- [x] ambiguous plain source paths fail before writes.
- [x] multipart source topology, legacy IDs, sizes, paths, and timestamps survive migration.
- [x] canonical cloud-cache aliases preserve remote handles and are idempotent.
- [x] migration CLI behavior remains unchanged, including no skipped-ID detail expansion.

The prerequisite command is already green and must stay green:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./internal/migration/media ./cmd/migrate-media ./internal/db
```

## File and Interface Map

| Area | Files | Contract produced |
| --- | --- | --- |
| Same-provider playback | `internal/offline_download/tool/cloud_play.go`, `internal/db/source_magnet.go`, `drivers/fc2/media_link.go`, `drivers/javdb/{driver.go,media_link.go,meta.go,util.go}` | Ordered persisted magnet attempts, cache-first playback, candidate error state, and complete removal of the second-provider configuration and paths. |
| Aggregate deletion | `internal/db/media.go`, `drivers/virtual_file/media.go`, `drivers/virtual_file/media_artifact.go` | Any-file deletion resolves and deletes the whole work, cloud cache, and artifact root. |
| NFO and artifact synchronization | `internal/db/media.go`, `drivers/virtual_file/nfo.go`, each driver's `meta.go`, `driver.go`, and scheduled media jobs | Discovery writes database state only; scheduled maintenance publishes artifacts, with stale-only `SyncNfo`, forced-full `RefreshNfo`, and forced precedence. |
| FC2 compatibility | `drivers/fc2/{driver.go,util.go,media_link.go,miss_av.go,job.go}` | Tombstones, multipart topology, persisted-first magnets, and continuation. |
| JavDB compatibility | `drivers/javdb/{driver.go,job.go,media_job.go,media_link.go}` | Normalized Filter, async enrichment, subtitle tags, and NFO staleness. |
| Pornhub compatibility | `drivers/pornhub/{driver.go,util.go}` | Referer, mocked fallback, canonical sources, and complete metadata projection. |
| Projection contract | `drivers/virtual_file/media.go`, driver discovery tests | Canonical `Url`, explicit source fields, title once, legacy file metadata. |

`MagnetCache` means the shared database index for files already downloaded into 115/PikPak. It stores the cloud provider, canonical media filename, remote file ID, magnet, and provider-specific options such as 115 `pickCode`. It is not `SourceMagnet` and it is not a disk artifact. A valid row avoids another offline download. Matching `main`, a stale remote handle remains stored while playback retries through persisted source magnets; `CreateMagnetCache` deletes the old provider-and-name row only when a successful new download is being cached, then inserts the replacement. Rows are intentionally shared by code across storages and sources and aggregate deletion removes them by code with no reference check.

The implementation must expose and use these interfaces. Keep them unexported unless an existing package boundary requires export:

```go
// internal/offline_download/tool/cloud_play.go
func CloudPlay(
    ctx context.Context,
    args model.LinkArgs,
    driverType string,
    driverPath string,
    downloadingFile model.Obj,
    magnets []model.SourceMagnet,
) (*model.Link, error)

// internal/db/source_magnet.go
func ListPlaybackSourceMagnets(workID uint) ([]model.SourceMagnet, error)
func UpdateSourceMagnetLastError(magnetID uint, message string) error

// drivers/virtual_file/media.go
func DeleteMediaWork(workID uint) error
func DeleteMediaFile(fileID uint) error

// drivers/virtual_file/nfo.go
func SyncMediaNFOs(storageID uint, source string, force bool) error
```

`ListPlaybackSourceMagnets` orders selected candidates first, then ascending priority, then ascending ID. `CloudPlay` receives persisted candidates only. A driver that discovers candidates must upsert them and reload this ordered list before calling `CloudPlay`.

The playback package uses these exact private test seams so regression tests never contact a real cloud provider:

```go
var queryCloudCache = db.QueryMagnetCacheByName
var resolveCloudCacheLink = getLinkByCache
var attemptCloudMagnet = playSingleMagnet
```

`playSingleMagnet` owns exactly one submission/status/file-resolution attempt. Production variables point to the real functions; each test restores overrides with `t.Cleanup` and does not run in parallel.

---

### Task 3: Same-Provider Ordered Magnet Playback

**Files:**
- Modify: `internal/offline_download/tool/cloud_play.go`
- Create: `internal/offline_download/tool/cloud_play_test.go`
- Modify: `internal/db/source_magnet.go`
- Modify: `internal/db/source_magnet_test.go`
- Modify: `drivers/fc2/media_link.go`
- Modify: `drivers/fc2/job_test.go`
- Modify: `drivers/javdb/media_link.go`
- Modify: `drivers/javdb/driver.go`
- Modify: `drivers/javdb/meta.go`
- Modify: `drivers/javdb/util.go`
- Modify: `drivers/javdb/job_test.go`

**Interfaces:**
- Consumes: completed selected-fingerprint persistence, `model.SourceMagnet`, `db.QueryMagnetCacheByName`, `db.SelectSourceMagnet`, and canonical file names from `model.BuildMediaFileName`.
- Produces: `ListPlaybackSourceMagnets`, `UpdateSourceMagnetLastError`, and the `CloudPlay` signature in the interface map.

- [ ] **Step 1: Write the failing playback tests**

Add these exact test cases with deterministic injected cache, download, and provider seams. Do not call a real cloud provider or Suke.

```text
TestCloudPlayUsesCanonicalCacheBeforeSourceMagnets
TestCloudPlayKeepsStaleRemoteCacheAndTriesSourceMagnets
TestCloudPlayRetriesDistinctMagnetsWithOneConfiguredProvider
TestCloudPlayRecordsFailedCandidateAndSelectsSuccessfulCandidate
TestFC2MediaMagnetsUsesPersistedRowsBeforeSuke
TestFC2MediaMagnetsPersistsSukeRowsBeforeReturningPlaybackCandidates
TestJavdbLinkUsesOnlyCloudPlayDriverType
```

The first test gives a valid canonical `MagnetCache` remote handle and asserts zero magnet attempts. The stale-cache test makes remote link resolution fail, asserts the old cache row remains while source magnets are attempted, then asserts a successful new cache write replaces that provider-and-name row. A failed redownload must leave the old row. The ordered retry test gives selected magnet A and priority magnet B, fails A, succeeds B, and asserts one provider for both attempts. The error-state test asserts A has a non-empty `LastError` and B is selected. The FC2 tests seed `SourceMagnet` rows, then assert Suke is not called, and separately assert a Suke result is upserted and reloaded before use. The JavDB test proves only `CloudPlayDriverType` is consumed.

- [ ] **Step 2: Run the tests and confirm RED**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./internal/offline_download/tool ./drivers/fc2 ./drivers/javdb
```

Expected: FAIL because `CloudPlay` accepts only one magnet, FC2 calls Suke before the database, candidate failures have no persistence path, and JavDB still exposes and reads `BackPlayDriverType` in multiple paths.

- [ ] **Step 3: Implement the minimum ordered attempt path**

Change the exact flow below, without adding a cloud-provider fallback:

```text
cache by (driver type, canonical filename)
  -> return a valid cache link
otherwise ordered persisted source magnets
  -> attempt each distinct fingerprint with the configured provider
  -> on failure: store LastError and continue
  -> on success: select that magnet and return its link
  -> on exhaustion: return the accumulated failure
```

`CloudPlay` owns the cache-first and per-candidate loop. A stale 115/PikPak remote handle is logged and retained, then the loop continues. `ListPlaybackSourceMagnets` supplies selected-first ordering. FC2 and JavDB upsert remotely discovered rows, reload persisted rows, and pass the list to `CloudPlay`. Remove `BackPlayDriverType` from `drivers/javdb/meta.go` and remove every read from `driver.go`, `util.go`, and neighboring playback helpers. Retain only the existing `FallbackPlay && MockedLink != ""` fallback after all same-provider candidates fail.

- [ ] **Step 4: Run the GREEN command**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./internal/offline_download/tool ./drivers/fc2 ./drivers/javdb
```

Expected: PASS. Cache hits perform no source lookup, failed selected magnets fall through to a different persisted magnet on the same provider, and success updates selection.

- [ ] **Step 5: Refactor and review checkpoint**

Keep provider acquisition and one-download attempt in a small private seam so tests remain deterministic. Verify that no branch reads `BackPlayDriverType`, no retry changes provider, no duplicate fingerprint is retried, and completed Task 1 selection preservation remains intact. Do not commit.

### Task 4: Aggregate Deletion, Cloud Cache, and Artifact Root Cleanup

**Files:**
- Modify: `internal/db/media.go`
- Modify: `internal/db/media_test.go`
- Modify: `drivers/virtual_file/media.go`
- Modify: `drivers/virtual_file/media_artifact.go`
- Modify: `drivers/virtual_file/media_test.go`
- Modify: `drivers/virtual_file/media_artifact_test.go`
- Modify: `drivers/fc2/discovery_test.go`
- Modify: `drivers/javdb/job_test.go`
- Modify: `drivers/pornhub/discovery_test.go`

**Interfaces:**
- Consumes: `FilmFile.WorkID`, `DeleteFilmWork`, canonical artifact identity, and canonical cache filename/code fields.
- Produces: `DeleteMediaFile(fileID)` as an aggregate-delete alias and `DeleteMediaWork(workID)` as the only virtual-file deletion route.

- [ ] **Step 1: Write the failing aggregate-deletion tests**

Add or replace the old sibling-preservation expectations with these exact tests:

```text
TestDeleteMediaFileDeletesWorkFilesMagnetsAndCloudCaches
TestDeleteMediaWorkRemovesWholeArtifactRootWithoutReferenceCheck
TestFC2RemoveIndividualMediaFileDeletesWholeAggregate
TestJavdbFilterDeletionDeletesWholeAggregate
TestPornhubRemoveIndividualMediaFileDeletesWholeAggregate
```

Seed a two-part work, two source magnets, cache rows with `Code == work.Code` for canonical part names, and files under `data/emby/{source}/{PrimaryDir}/{code}`. Delete part one. Assert that no work, file, magnet, matching-code cache row, or artifact root remains. Seed another database row that maps to the same artifact path and shared cache code and assert its presence does not suppress either deletion. This shared collision behavior is intentional.

- [ ] **Step 2: Run the tests and confirm RED**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./internal/db ./drivers/virtual_file ./drivers/fc2 ./drivers/javdb ./drivers/pornhub
```

Expected: FAIL because `DeleteMediaFile` deletes only one `FilmFile`, leaves siblings and magnets, and virtual deletion does not remove the complete artifact root or cache state.

- [ ] **Step 3: Implement one destructive aggregate route**

Resolve the work and artifact identity before mutation. Remove `data/emby/{source}/{PrimaryDir}/{code}` with the existing artifact helper, treating a missing path as success. Then make `DeleteFilmWork` use its transaction handle to delete `MagnetCache` rows with `Code == work.Code` (the same predicate as `DeleteAllMagnetCacheByCode`), source magnets, every film file, and the work. Do not call a helper backed by the package-global database from inside the transaction. Make `DeleteMediaFile` resolve `WorkID` and call the same aggregate route. Drivers, filtering, and FC2 tombstoning must call `virtual_file.DeleteMediaWork` or `DeleteMediaFile`, never a one-file delete primitive.

Do not query for sibling files, mapped directories, or last references before deletion. Do not add a relation table or `StorageID` to the artifact path.

- [ ] **Step 4: Run the GREEN command**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./internal/db ./drivers/virtual_file ./drivers/fc2 ./drivers/javdb ./drivers/pornhub
```

Expected: PASS. Any deletion entry point removes the whole aggregate and the exact canonical root.

- [ ] **Step 5: Refactor and review checkpoint**

Keep the database transaction free of filesystem path construction and keep virtual-file cleanup responsible for the path. Confirm cache cleanup uses aggregate data, every multipart part disappears together, and no new last-reference condition or sibling-preservation branch exists. Do not commit.

### Task 5: Shared Stale and Forced NFO Synchronization

**Files:**
- Modify: `internal/db/media.go`
- Modify: `internal/db/media_test.go`
- Modify: `drivers/virtual_file/nfo.go`
- Modify: `drivers/virtual_file/media_nfo_test.go`
- Modify: `drivers/fc2/driver.go`
- Modify: `drivers/fc2/meta.go`
- Modify: `drivers/fc2/media_job.go`
- Modify: `drivers/fc2/job_test.go`
- Modify: `drivers/javdb/driver.go`
- Modify: `drivers/javdb/meta.go`
- Modify: `drivers/javdb/media_job.go`
- Modify: `drivers/javdb/job_test.go`
- Modify: `drivers/pornhub/driver.go`
- Modify: `drivers/pornhub/meta.go`
- Modify: `drivers/pornhub/job.go`
- Modify: `drivers/pornhub/util.go`
- Modify: `drivers/pornhub/discovery_test.go`

**Interfaces:**
- Consumes: `MetadataVersion`, `NfoVersion`, `NfoLastError`, `virtual_file.MediaInfo`, and artifact identity paths.
- Produces: `SyncMediaNFOs(storageID, source, force)` where `force=false` means stale-only and `force=true` means forced-full.

- [ ] **Step 1: Write the failing NFO version tests**

Add these exact tests:

```text
TestSyncMediaNFOsWritesOnlyStaleWorks
TestRefreshMediaNFOsRewritesFreshAndStaleWorks
TestSyncMediaNFOsRecordsErrorAndLeavesWorkStale
TestSubtitleTagMetadataChangeMarksNFOStale
TestFC2SyncNfoUsesSharedNormalizedSync
TestJavdbSyncNfoUsesSharedNormalizedSync
TestPornhubSyncNfoUsesSharedNormalizedSync
TestNFOFlagsPreferForcedRefreshWhenBothEnabled
TestDiscoveryWritesDatabaseOnlyAndScheduledScanPublishesArtifacts
```

Seed one fresh and one stale normalized work in each test. Assert stale-only writes only the stale root and sets its `NfoVersion` to the written metadata snapshot. Assert forced-full rewrites both roots. Enable both flags and assert the scheduler performs one forced-full pass, not a forced pass plus a stale pass. Inject an artifact write error and assert `NfoLastError` is populated while `NfoVersion` remains behind. Add `model.TagSubtitle` and assert the work becomes stale. Discovery tests assert no disk files exist until the scheduled artifact method runs.

- [ ] **Step 2: Run the tests and confirm RED**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./internal/db ./drivers/virtual_file ./drivers/fc2 ./drivers/javdb ./drivers/pornhub
```

Expected: FAIL because the current driver jobs use legacy or scattered NFO paths and do not select stale normalized works or update version state after writes.

- [ ] **Step 3: Implement normalized NFO state transitions**

Add database queries for all works and stale works scoped to storage and source. `SyncMediaNFOs(..., false)` queries only `MetadataVersion > NfoVersion`; `SyncMediaNFOs(..., true)` queries all works. Convert each work to `MediaInfo` with its aggregate identity, write atomically through `UpdateMediaNfo`, then persist either success version plus cleared error or retained stale version plus write error. Add `SyncNfo` to FC2 config and `RefreshNfo` to Pornhub config; retain both fields in JavDB with corrected help text. Each scheduler uses `if RefreshNfo { force } else if SyncNfo { stale }`.

Remove direct `CacheImageAndNfo` and direct normalized NFO writes from discovery and favorite paths. Driver scheduled maintenance publishes missing posters and related artifacts from persisted `FilmWork` fields, applying source-specific headers such as Pornhub Referer, then invokes the shared NFO operation according to the two flags.

Add `var writeNormalizedMediaNFO = UpdateMediaNfo` beside the shared helper as the deterministic test seam. Tests restore it with `t.Cleanup` and do not run in parallel. Remove the duplicated stale-only loops from `drivers/fc2/media_job.go` and `drivers/javdb/media_job.go` only after the shared tests are green.

Keep JavDB enrichment asynchronous. It updates aggregate metadata and leaves NFO sync to the shared stale or forced operation.

- [ ] **Step 4: Run the GREEN command**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./internal/db ./drivers/virtual_file ./drivers/fc2 ./drivers/javdb ./drivers/pornhub
```

Expected: PASS. Stale-only and forced-full paths have distinct observable write counts and error state is retryable.

- [ ] **Step 5: Refactor and review checkpoint**

Keep version decisions at the shared helper boundary. Confirm image-only discovery fields cannot make an NFO stale, subtitle tags can, forced refresh ignores equal versions, and a failed work does not stop later work writes. Do not commit.

### Task 6: FC2 Tombstones, Multipart Topology, and Continuation

**Files:**
- Modify: `drivers/fc2/driver.go`
- Modify: `drivers/fc2/util.go`
- Modify: `drivers/fc2/media_link.go`
- Modify: `drivers/fc2/miss_av.go`
- Modify: `drivers/fc2/job.go`
- Modify: `drivers/fc2/discovery_test.go`
- Modify: `drivers/fc2/job_test.go`

**Interfaces:**
- Consumes: aggregate deletion from Task 4, `db.CreateMissedFilms`, persisted ordered source magnets from Task 3, and `db.ReplaceFilmFiles`.
- Produces: FC2 discovery that skips tombstones, persists selected manifests as contiguous parts, and returns accumulated good items after page or item errors.

- [ ] **Step 1: Write the failing FC2 tests**

Add these exact tests:

```text
TestFC2RemoveCreatesTombstoneAndDeletesAggregate
TestFC2DiscoverySkipsTombstonedCode
TestFC2FavoriteUsesAvailableSukeManifestForMultipartTopology
TestFC2DiscoveryContinuesAfterOneItemFailure
TestFC2DiscoveryRetainsAccumulatedIDsAfterPageFailure
TestMissAVDiscoveryContinuesAfterOneItemFailure
```

The multipart test supplies `sukeMeta.Magnets[0].GetFiles()` with three media files larger than 100 MiB and expects `PartIndex` values `1, 2, 3`, `PartCount` value `3`, source names and sizes from the deterministically sorted list, and canonical names ending in `-cd1.mp4`, `-cd2.mp4`, and `-cd3.mp4`. `GetMagnet()` and `GetFiles()` must use the same lazy detail-page result. The manifest is not persisted as a new model; its values are projected into `FilmFile` before the raw Suke objects are discarded. Ordinary category discovery must not call Suke merely to obtain it. Item continuation includes a good item before and after a failing item and asserts both remain. The page-failure test asserts IDs accumulated before the failed page are still persisted and no fixed page-count cap is applied. Tombstone tests delete a work, then rescan its code and assert it is not rediscovered.

- [ ] **Step 2: Run the tests and confirm RED**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./drivers/fc2 ./internal/db ./drivers/virtual_file
```

Expected: FAIL because the current FC2 path can resurrect deleted work, creates a singleton topology for multipart content, and aborts a scan on one failing item or page.

- [ ] **Step 3: Implement the minimum FC2 compatibility flow**

When FC2 removes a work, call aggregate deletion and persist the code tombstone. Filter non-favorite discovery candidates through the tombstone query before upsert. Whenever the playback or favorite path already calls Suke, `GetMagnet()` populates the same object's `GetFiles()` result; filter files above 100 MiB, sort by name as the existing cloud-cache path does, and call `ReplaceFilmFiles` before discarding the raw Suke result. Do not create a manifest table and do not fetch Suke during ordinary category discovery solely for topology. Category pagination has no fixed page cap: continue while each successful page contributes new IDs, and on a page error log it, end the current loop, and persist IDs already accumulated. MissAV loops log an item failure and continue with siblings. Keep the Task 3 persisted-first magnet ordering in `media_link.go`.

- [ ] **Step 4: Run the GREEN command**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./drivers/fc2 ./internal/db ./drivers/virtual_file
```

Expected: PASS. Deleted codes stay absent, multipart files are contiguous, and a bad page or item does not erase independent results.

- [ ] **Step 5: Refactor and review checkpoint**

Keep favorite behavior separate from tombstone filtering. Confirm no code reintroduces a per-file delete, no fallback reads Suke before persisted magnets, and no continuation path returns partial results as an error merely because an independent item failed. Do not commit.

### Task 7: JavDB Filter, Async Metadata, Subtitle Tag, and NFO Integration

**Files:**
- Modify: `drivers/javdb/driver.go`
- Modify: `drivers/javdb/job.go`
- Modify: `drivers/javdb/media_job.go`
- Modify: `drivers/javdb/media_link.go`
- Modify: `drivers/javdb/job_test.go`
- Modify: `drivers/javdb/discovery_test.go`
- Modify: `internal/db/media.go`
- Modify: `internal/db/media_test.go`

**Interfaces:**
- Consumes: aggregate deletion from Task 4, stale sync from Task 5, playback ordering from Task 3, `model.TagSubtitle`, and normalized `FilmWork` query helpers.
- Produces: scheduled normalized filtering and eventual metadata enrichment that marks NFO stale only when projected metadata changes.

- [ ] **Step 1: Write the failing JavDB tests**

Add these exact tests:

```text
TestJavdbScheduledMaintenanceRunsNormalizedFilter
TestJavdbFilterDeletesAggregateCloudCacheAndArtifacts
TestJavdbDiscoveryStoresBasicWorkBeforeEnrichment
TestJavdbMetadataScanAddsSubtitleTagAndMarksNFOStale
TestJavdbMetadataScanContinuesAfterOneWorkFailure
TestJavdbSyncNfoWritesSubtitleTagAfterMetadataScan
```

The scheduled test invokes the same private maintenance method used by the cron callback. The filter test seeds a normalized work whose title matches `Filter` and asserts the whole aggregate is gone. The discovery test asserts no translation, synopsis, actor, tag, or remote-magnet enrichment runs inline. The subtitle test gives metadata with subtitles, asserts `model.TagSubtitle` appears once, and asserts `MetadataVersion > NfoVersion` before the shared sync writes NFO.

- [ ] **Step 2: Run the tests and confirm RED**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./drivers/javdb ./internal/db ./drivers/virtual_file
```

Expected: FAIL because the current cron omits Filter, old Filter logic reads legacy `Film`, metadata updates are not fully tied to NFO stale state, and one error can stop the relevant loop.

- [ ] **Step 3: Implement normalized scheduled behavior**

Extract the cron body into one private maintenance method used by both `Init` and tests. Query normalized works by the existing comma-separated Filter prefix semantics and delete each through `virtual_file.DeleteMediaWork`. Keep discovery limited to basic aggregate persistence. In the background metadata scan, persist magnets, stable-union actors and tags, add `model.TagSubtitle` once when metadata says subtitles exist, and use aggregate update helpers so NFO-visible changes increment metadata version. Continue after per-work failures and write existing retry state. Let Task 5 perform the NFO write.

- [ ] **Step 4: Run the GREEN command**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./drivers/javdb ./internal/db ./drivers/virtual_file
```

Expected: PASS. Filter is scheduled against normalized works, discovery remains eventual, subtitle metadata appears once, and subsequent stale sync writes the tag.

- [ ] **Step 5: Refactor and review checkpoint**

Keep legacy `Film` reads out of production Filter and NFO paths. Verify the task did not restore synchronous JavDB enrichment, remove the same-provider playback decision, or add a direct NFO write that bypasses Task 5. Do not commit.

### Task 8: Pornhub Referer, Mocked Link, and Metadata Compatibility

**Files:**
- Modify: `drivers/pornhub/driver.go`
- Modify: `drivers/pornhub/util.go`
- Modify: `drivers/pornhub/discovery_test.go`
- Verify only: `drivers/virtual_file/media.go`
- Modify only if shared projection coverage is missing: `drivers/virtual_file/media_test.go`

**Interfaces:**
- Consumes: `SourceRef` for source lookup, `SourceURL` for canonical projection, `model.Link.Header`, `virtual_file.MediaInfo.ImgUrlHeaders`, and the shared NFO route from Task 5.
- Produces: direct video and image requests with Referer, explicit mocked-link fallback, and one-prefix metadata.

Use one private test seam for dynamic resolution:

```go
var resolvePornhubVideoLink = func(driver *Pornhub, sourceRef string) (string, error) {
    return driver.getVideoLink(sourceRef)
}
```

Tests restore overrides with `t.Cleanup` and do not run in parallel.

- [ ] **Step 1: Write the failing Pornhub tests**

Replace or extend the existing discovery tests with these exact cases:

```text
TestLinkAddsRefererToResolvedPornhubVideo
TestLinkReturnsMockedLinkWithoutResolutionWhenMockedIsEnabled
TestLinkReturnsResolutionErrorWhenMockedFallbackIsUnavailable
TestCacheDiscoveredWorkArtifactsPassesReferer
TestConvertFilmsPrefixesCodeOnceAndKeepsPlaylistTags
TestBuildDiscoveredWorkUsesPrimaryDirActorForEmptyUsername
TestPornhubProjectionUsesCanonicalSourceURLAndExplicitSourceRef
```

The link tests assert `Header.Get("Referer") == d.ServerUrl`. The mock test sets `d.Mocked` and `d.MockedLink`, expects the mock URL with nil error, and asserts the dynamic resolver is not called. With `d.Mocked == false`, a resolver error must be returned even when a dormant `MockedLink` string exists. The scheduled image test captures `MediaInfo` and asserts the image header. The projection test asserts `SourceRef` is the viewkey while `SourceURL` and projected `Url` are the canonical page URL. The fallback-actor test exercises `buildDiscoveredWork`, not the already-correct legacy `convertFilms` helper.

- [ ] **Step 2: Run the tests and confirm RED**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./drivers/pornhub ./drivers/virtual_file
```

Expected: FAIL because resolved video and image requests lack Referer, resolver failures do not honor configured mocked fallback, and metadata cases are not fully projected.

- [ ] **Step 3: Implement the narrow Pornhub fixes**

Set `Referer: d.ServerUrl` on the resolved direct video `model.Link` and on `MediaInfo.ImgUrlHeaders` used by the scheduled poster fetch; the generated background symlink has no separate request. Preserve the existing early `d.Mocked && d.MockedLink != ""` behavior and add no fallback field. When mocked mode is off, return dynamic resolver errors. Build titles through the shared media title builder exactly once, persist playlist tags in `FilmWork`, and use `primaryDir` as the normalized actor fallback when username is empty. Keep `SourceRef` and `SourceURL` separate and set projected `Url` to `SourceURL` only.

- [ ] **Step 4: Run the GREEN command**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./drivers/pornhub ./drivers/virtual_file
```

Expected: PASS. Protected video and image requests carry Referer, mocked fallback has explicit limits, and source fields and title metadata retain their contract.

- [ ] **Step 5: Refactor and review checkpoint**

Keep the fallback decision at the Pornhub link boundary. Confirm no path falls back to `Url` as a viewkey, no title receives the code prefix twice, and no broad error catch hides an unavailable mocked fallback. Do not commit.

### Task 9: Normalized Projection Integration Verification

**Files:**
- Verify first: `drivers/virtual_file/media.go`
- Modify only if a regression is proven: `drivers/virtual_file/media_test.go`
- Verify: `drivers/fc2/discovery_test.go`
- Verify: `drivers/javdb/discovery_test.go`
- Verify: `drivers/pornhub/discovery_test.go`
- Verify only, do not modify: `internal/migration/media/database_test.go`
- Verify only, do not modify: `cmd/migrate-media/main_test.go`

**Interfaces:**
- Consumes: `model.FilmFileWithWork`, `model.BuildMediaFileName`, `model.BuildMediaTitle`, `SourceRef`, `SourceURL`, and completed Task 2 file metadata preservation.
- Produces: every projected `model.EmbyFileObj` with explicit `SourceRef`, canonical `SourceURL`, canonical `Url`, one code prefix, and `FilmFile` ID, size, creation time, and modification time.

- [ ] **Step 1: Run the existing projection tests before adding code**

The current converter already maps `FilmFile.ID`, `SourceSize`, timestamps, `SourceRef`, `SourceURL`, and `Url=SourceURL`. Run these existing packages first:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./drivers/virtual_file ./drivers/fc2 ./drivers/javdb ./drivers/pornhub ./internal/migration/media ./cmd/migrate-media
```

Expected: PASS. If it passes, do not edit production projection code. Confirm the existing tests cover or add test-only coverage for these scenarios:

```text
TestConvertMediaFileToEmbyFileProjectsCanonicalSourceFields
TestConvertMediaFileToEmbyFilePreservesFileIDSizeAndTimes
TestConvertMediaFileToEmbyFilePrefixesCodeExactlyOnce
TestFC2ProjectionUsesCanonicalMultipartName
TestJavdbProjectionUsesCanonicalSourceURL
TestPornhubProjectionDoesNotOfferUrlViewKeyCompatibility
```

The source-field test creates a work where `SourceRef != SourceURL` and asserts `Url == SourceURL`, never `SourceRef`. The metadata test seeds nonzero source size and distinct timestamps, then asserts the projected object retains all values. The last test must call the source-lookup path through `SourceRef`, not through `Url`.

- [ ] **Step 2: Classify any projection failure by its owning earlier task**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./drivers/virtual_file ./drivers/fc2 ./drivers/javdb ./drivers/pornhub ./internal/migration/media ./cmd/migrate-media
```

Expected: the shared mapping and completed migration contracts are already green. A Pornhub double-prefix failure belongs to Task 8; missing migrated data belongs to the completed Task 2 and must be reported rather than silently reimplemented; a shared converter regression belongs here and requires a new failing regression test before a minimal edit.

- [ ] **Step 3: Add production code only if Step 2 proves a shared converter defect**

Any necessary edit is limited to the shared converter and must be preceded by a test that fails for the observed defect. Do not modify migration code or CLI output for this task.

- [ ] **Step 4: Run the GREEN command**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./drivers/virtual_file ./drivers/fc2 ./drivers/javdb ./drivers/pornhub ./internal/migration/media ./cmd/migrate-media
```

Expected: PASS. In the normal path this task is verification-only and produces no Go diff.

- [ ] **Step 5: Refactor and review checkpoint**

Centralize the mapping in the existing virtual-file converter. Confirm no driver recreates a divergent field mapping, no source-specific compatibility path assigns `Url = SourceRef`, and no Task 2 source file was changed. Do not commit.

### Task 10: Formatting, Targeted Tests, Race, Vet, and Build Gates

**Files:**
- Format only the changed Go files from Tasks 3 through 9.
- Verify: all packages changed by Tasks 3 through 9 plus completed Task 1 and Task 2 packages.

**Interfaces:**
- Consumes: completed targeted test suites and repository Go SDK path.
- Produces: formatted code, passing focused behavior tests, clean targeted vet, race evidence, and build evidence.

- [ ] **Step 1: Run the focused suite before formatting**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./internal/offline_download/tool ./internal/db ./drivers/virtual_file ./drivers/fc2 ./drivers/javdb ./drivers/pornhub ./internal/migration/media ./cmd/migrate-media
```

Expected RED reason: any failure is a task-local regression from Tasks 3 through 9. Fix the named behavior before proceeding; do not edit unrelated files to suppress it.

- [ ] **Step 2: Format only the implementation and test files named in Tasks 3 through 9**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/gofmt -w internal/db/media.go internal/db/media_test.go internal/db/source_magnet.go internal/db/source_magnet_test.go internal/offline_download/tool/cloud_play.go internal/offline_download/tool/cloud_play_test.go drivers/virtual_file/media.go drivers/virtual_file/media_test.go drivers/virtual_file/media_artifact.go drivers/virtual_file/media_artifact_test.go drivers/virtual_file/nfo.go drivers/virtual_file/media_nfo_test.go drivers/fc2/driver.go drivers/fc2/meta.go drivers/fc2/util.go drivers/fc2/media_link.go drivers/fc2/miss_av.go drivers/fc2/job.go drivers/fc2/media_job.go drivers/fc2/discovery_test.go drivers/fc2/job_test.go drivers/javdb/driver.go drivers/javdb/meta.go drivers/javdb/util.go drivers/javdb/job.go drivers/javdb/media_job.go drivers/javdb/media_link.go drivers/javdb/discovery_test.go drivers/javdb/job_test.go drivers/pornhub/driver.go drivers/pornhub/meta.go drivers/pornhub/util.go drivers/pornhub/job.go drivers/pornhub/discovery_test.go
```

Expected: only touched files are formatted. Do not format unrelated user files.

- [ ] **Step 3: Run race, vet, and build commands**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -race -count=1 ./internal/offline_download/tool ./internal/db ./drivers/virtual_file ./drivers/fc2 ./drivers/javdb ./drivers/pornhub
/Library/Go/sdk/go1.25.4/bin/go vet ./internal/offline_download/tool ./internal/db ./drivers/virtual_file ./drivers/fc2 ./drivers/javdb ./drivers/pornhub ./internal/migration/media ./cmd/migrate-media
/Library/Go/sdk/go1.25.4/bin/go build ./cmd/migrate-media ./drivers/fc2 ./drivers/javdb ./drivers/pornhub
```

Expected RED reason: a race, vet diagnostic, or build failure is a task-local defect unless it matches the documented repository baseline exactly.

- [ ] **Step 4: Run the GREEN focused gate**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./internal/offline_download/tool ./internal/db ./drivers/virtual_file ./drivers/fc2 ./drivers/javdb ./drivers/pornhub ./internal/migration/media ./cmd/migrate-media && /Library/Go/sdk/go1.25.4/bin/go test -race -count=1 ./internal/offline_download/tool ./internal/db ./drivers/virtual_file ./drivers/fc2 ./drivers/javdb ./drivers/pornhub && /Library/Go/sdk/go1.25.4/bin/go vet ./internal/offline_download/tool ./internal/db ./drivers/virtual_file ./drivers/fc2 ./drivers/javdb ./drivers/pornhub ./internal/migration/media ./cmd/migrate-media && /Library/Go/sdk/go1.25.4/bin/go build ./cmd/migrate-media ./drivers/fc2 ./drivers/javdb ./drivers/pornhub
```

Expected: PASS.

- [ ] **Step 5: Review checkpoint**

Inspect the working-tree diff without staging. Verify changed files stay within the task file lists plus the two handoff documents, completed Task 1 and Task 2 behavior remains intact, and no forbidden architecture was introduced. Do not commit.

### Task 11: Full Repository Baseline Classification

**Files:**
- Verify only: repository-wide Go packages and existing workflow context in `.github/workflows/build.yml`.

**Interfaces:**
- Consumes: Task 10 focused gate and documented baseline facts.
- Produces: a clear pass or a separated list of exact repository baseline or environment failures.

- [ ] **Step 1: Run the full test baseline**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./...
```

Expected RED reason: any failure not already documented as a repository baseline or local environment dependency is a release-blocking regression. The known historical classes are `jable_tv` formatting checks and tests needing a local aria2 service; classify by exact failing package and message, not by assumption.

- [ ] **Step 2: Run full vet and build**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go vet ./...
/Library/Go/sdk/go1.25.4/bin/go build ./...
```

Expected RED reason: a diagnostic or build failure in a changed media package is a task-local regression. Do not switch branches, fetch, clean, or edit unrelated packages while classifying it.

- [ ] **Step 3: Compare results to the documented baseline**

Record each failure as one of these exact outcomes:

```text
PASS
Known baseline or environment failure, exact package and message recorded
New regression, return to the task that owns the package
```

Expected: no new media compatibility regression.

- [ ] **Step 4: Run the full GREEN gate when baseline permits**

Run:

```bash
/Library/Go/sdk/go1.25.4/bin/go test -count=1 ./... && /Library/Go/sdk/go1.25.4/bin/go vet ./... && /Library/Go/sdk/go1.25.4/bin/go build ./...
```

Expected: PASS, or only the exact classified baseline or environment failures from Step 3. Do not conceal a new failure by changing test expectations.

- [ ] **Step 5: Review checkpoint**

Confirm the CI build context remains `bash build.sh dev web` followed by the cross-platform build workflow, as recorded in `.github/workflows/build.yml`. Do not run fetches, create artifacts, or commit.

### Task 12: Review-Work Gate and Human Handoff

**Files:**
- Review only: all files changed by Tasks 3 through 9 and the existing uncommitted Task 1 and Task 2 source files.

**Interfaces:**
- Consumes: Task 10 evidence, Task 11 baseline classification, and the binding design specification.
- Produces: a review-work result with every actionable issue resolved or explicitly returned to its owning task.

- [ ] **Step 1: Start the review-work gate**

Invoke the required skill:

```text
review-work
```

Expected RED reason: any reviewer finding that violates aggregate deletion, same-provider retries, NFO versioning, eventual JavDB enrichment, SourceRef and SourceURL semantics, Pornhub headers, or test coverage is actionable.

- [ ] **Step 2: Resolve each actionable finding at its owning seam**

For a finding, return only to the task that owns its file and behavior. Add a failing regression test named for that behavior, confirm the failure, apply the minimum correction, then rerun that task's exact GREEN command. Do not patch callers when the shared seam is defective.

Expected: each fix is covered by a named test and does not reopen Task 1 or Task 2.

- [ ] **Step 3: Rerun the owning focused gate**

Run the exact command from Task 3, 4, 5, 6, 7, 8, or 9 that owns the resolved finding.

Expected GREEN: PASS with the regression test included.

- [ ] **Step 4: Complete review-work and final status check**

Run the review-work gate to completion, then inspect status without staging:

```bash
GIT_MASTER=1 git status --short
```

Expected: review-work reports no unresolved actionable finding. The status shows intended uncommitted work and preserves pre-existing user files.

- [ ] **Step 5: Human handoff checkpoint**

Report the passing focused commands, full-baseline classification, review-work result, and exact uncommitted files. State plainly that no commits were created and wait for a human request before any commit action.

## Plan Self-Review

| Binding requirement | Owning task |
| --- | --- |
| Selected-first, distinct magnets, one cloud provider, no `BackPlayDriverType` | Task 3 |
| Whole-aggregate deletion, cache cleanup, artifacts, no reference check | Task 4 |
| Stale-only sync and forced-full refresh | Task 5 |
| FC2 persisted magnets, tombstones, multipart, continuation | Task 3 and Task 6 |
| JavDB Filter, eventual enrichment, subtitle NFO state | Task 7 |
| Pornhub Referer, MockedLink, metadata | Task 8 |
| SourceRef, SourceURL, canonical Url, projection metadata | Task 9 |
| Formatting, focused tests, race, vet, build, full baseline | Task 10 and Task 11 |
| Review-work and no-commit handoff | Task 12 |

The plan contains no skipped-ID CLI expansion, no `StorageID` artifact path, no directory relation table, no second-driver retry, no synchronous JavDB enrichment, no `Url = SourceRef` compatibility, and no last-reference deletion check.
