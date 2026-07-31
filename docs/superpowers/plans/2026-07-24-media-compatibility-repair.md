# Media Compatibility Repair Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Every behavior follows RED -> GREEN -> refactor. Do not commit unless the user separately requests it.

**Goal:** Restore the media migration, playback, deletion, NFO, and driver contracts confirmed by the feature/film-manage review.

**Architecture:** Keep `FilmWork` as the aggregate root and `PrimaryDir` as immutable first ownership. Centralize aggregate deletion, source-magnet ordering, and NFO synchronization below the drivers. Preserve eventually consistent JavDB enrichment while making every metadata change visible to incremental NFO sync.

**Tech Stack:** Go 1.25.4, GORM, SQLite tests, existing OpenList media drivers and virtual-file helpers.

## Global Constraints

- Artifact paths remain `data/emby/{source}/{primaryDir}/{code}` without `StorageID`.
- Artifact deletion is unconditional; do not add a last-reference check.
- Keep one `PrimaryDir`; merge non-empty actors and tags with stable deduplication.
- `SyncNfo` synchronizes stale records after database changes; `RefreshNfo` forces a full database-to-disk rewrite.
- JavDB discovery stores basic records immediately and enriches them asynchronously.
- Use one cloud-play driver and retry different magnets; remove second-driver fallback behavior.
- Normalized media uses `SourceRef` and `SourceURL`; `Url` remains the canonical page URL and is not an old viewkey compatibility contract.
- Do not add skipped migration IDs or reasons to CLI output.
- Do not commit, switch branches, create worktrees, fetch, clean, or overwrite unrelated user changes.

---

### Task 1: Aggregate Persistence Invariants

**Files:**
- Modify: `internal/db/media.go`
- Modify: `internal/db/source_magnet.go`
- Test: `internal/db/media_test.go`
- Test: `internal/db/source_magnet_test.go`

**Produces:** stable actor/tag union, immutable `PrimaryDir`, material metadata-version updates, and selected-magnet preservation.

- [ ] Add failing tests for actor/tag deduplication and selected fingerprint preservation.
- [ ] Run focused tests and confirm failures describe the missing behavior.
- [ ] Implement minimal transaction-safe behavior.
- [ ] Run focused tests and package vet.

### Task 2: Migration Compatibility

**Files:**
- Modify: `internal/migration/media/database.go`
- Modify: `internal/migration/media/database_test.go`
- Modify: `cmd/migrate-media/main_test.go`

**Consumes:** Task 1 aggregate and source-magnet contracts.

**Produces:** legacy file identity/time/size preservation, duplicate plain-file rejection, first-directory actor/tag merging, and canonical cloud-cache aliases preserving remote handles.

- [ ] Add failing migration tests for each compatibility contract.
- [ ] Confirm RED with `go test -count=1 ./internal/migration/media ./cmd/migrate-media`.
- [ ] Implement fail-closed planning and idempotent alias population.
- [ ] Confirm GREEN and run package vet.

### Task 3: Cloud Playback Magnet Retry

**Files:**
- Modify: `internal/offline_download/tool/cloud_play.go`
- Create or modify tests in: `internal/offline_download/tool/`
- Modify: `drivers/javdb/media_link.go`
- Modify: `drivers/javdb/driver.go`
- Modify: `drivers/fc2/media_link.go`

**Produces:** cache-first playback, persisted selected-first magnet iteration, one rescan, and same-provider retries.

- [ ] Add failing tests around cache hit, first-magnet failure, second-magnet success, and FC2 persisted-magnet use.
- [ ] Confirm RED in affected packages.
- [ ] Extract one-attempt cloud play and implement ordered retry without `BackPlayDriverType`.
- [ ] Confirm GREEN and package vet.

### Task 4: Aggregate Deletion And Projection

**Files:**
- Modify: `internal/db/media.go`
- Modify: `drivers/virtual_file/media.go`
- Modify: `drivers/virtual_file/media_test.go`
- Modify: `drivers/{fc2,javdb,pornhub}/discovery_test.go`

**Produces:** deleting any media file removes the complete work aggregate, all parts, magnets, cloud-cache rows, and artifacts; migrated IDs/times/sizes project consistently.

- [ ] Replace sibling-preservation expectations with aggregate deletion tests.
- [ ] Confirm RED.
- [ ] Implement transactional aggregate deletion and projection fixes.
- [ ] Confirm GREEN and package vet.

### Task 5: Incremental And Full NFO Sync

**Files:**
- Add focused helpers under `drivers/virtual_file/` if needed.
- Modify: `drivers/javdb/media_job.go`
- Modify: `drivers/fc2/media_job.go`
- Modify: `drivers/pornhub/job.go`
- Modify corresponding job tests.

**Produces:** stale-only `SyncNfo`, forced-full `RefreshNfo`, and successful version/error recording.

- [ ] Add failing stale/full synchronization and subtitle-tag tests.
- [ ] Confirm RED.
- [ ] Implement shared projection/synchronization and metadata-version updates.
- [ ] Confirm GREEN and package vet.

### Task 6: FC2 Discovery And Tombstones

**Files:**
- Modify: `drivers/fc2/util.go`
- Modify: `drivers/fc2/miss_av.go`
- Modify: `drivers/fc2/driver.go`
- Modify: `drivers/fc2/discovery_test.go`
- Modify: `drivers/fc2/job_test.go`

**Produces:** page/item error continuation, deleted-work tombstones, filtered rediscovery, and selected-manifest multipart topology.

- [ ] Add failing continuation, tombstone, and multipart tests.
- [ ] Confirm RED.
- [ ] Implement minimal behavior using existing DB helpers.
- [ ] Confirm GREEN and package vet.

### Task 7: JavDB Filter And Async Completion

**Files:**
- Modify: `drivers/javdb/driver.go`
- Modify: `drivers/javdb/media_job.go`
- Modify: `drivers/javdb/job_test.go`

**Produces:** normalized `FilmWork` filtering, asynchronous enrichment, subtitle tag updates, and NFO staleness.

- [ ] Add failing filter and subtitle/NFO tests.
- [ ] Confirm RED.
- [ ] Implement normalized cleanup and metadata-version updates.
- [ ] Confirm GREEN and package vet.

### Task 8: Pornhub Compatibility

**Files:**
- Modify: `drivers/pornhub/driver.go`
- Modify: `drivers/pornhub/util.go`
- Modify: `drivers/pornhub/discovery_test.go`

**Produces:** video/poster Referer, configured mocked fallback, one-prefix titles, playlist tags, and empty-username actor fallback.

- [ ] Add failing link/header/fallback and metadata tests.
- [ ] Confirm RED.
- [ ] Implement compatibility behavior while keeping `SourceRef`/`SourceURL` internal contracts.
- [ ] Confirm GREEN and package vet.

### Task 9: Verification And Review

- [ ] Run formatting and diagnostics for every changed Go file.
- [ ] Run focused tests, race tests, targeted vet, and tool builds.
- [ ] Run the full repository tests/vet and separate baseline/environment failures.
- [ ] Run the five-lane review-work gate and resolve all actionable findings.
- [ ] Verify the final git state contains only intended uncommitted changes.
