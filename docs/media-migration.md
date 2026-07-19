# Media Migration

`migrate-media` is a standalone operator command. Normal service startup does not migrate legacy media rows or files.

## Before You Start

Stop OpenList and make a consistent backup of the SQLite database and the data directory. Keep the backup until normalized media has been verified in production.

## Dry Run

Run a read-only preflight against a copy or the stopped database:

```text
go run ./cmd/migrate-media \
  --db /path/to/data.db \
  --data-dir /path/to/data \
  --dry-run
```

Dry-run requires `--db` to name an existing regular file and opens SQLite with `mode=ro`. It performs the complete identity, path-safety, byte-collision, cleanup, and journal-compatibility preflight without creating a database, WAL, SHM, journal sidecar, normalized table, artifact directory, or migration journal. The JSON report includes aggregate operations and separate planned move, delete, and empty-directory-removal counts.

## Storage Mapping

When one storage uses a supported media driver, its storage ID is selected automatically. With multiple storages for the same source, provide an explicit mapping for each legacy directory:

```text
--storage-map 'javdb:Actor A=12'
--storage-map 'javdb:个人收藏=13'
```

The command refuses ambiguous sources without a mapping. It never guesses between storage IDs. Storage ID remains part of normalized database identity, but runtime artifact paths do not contain it:

```text
data/emby/{source}/{primaryDir}/{code}
```

Preflight fails if retained works with different storage IDs would own the same runtime artifact root.

## Apply

After reviewing the dry-run report, apply the migration while the service remains stopped:

```text
go run ./cmd/migrate-media \
  --db /path/to/data.db \
  --data-dir /path/to/data \
  --journal /path/to/media-migration-journal.json \
  --storage-map 'javdb:Actor A=12' \
  --apply
```

The command prints a JSON report with planned and completed move, verified-delete, directory-removal, and existing-target counts. Apply opens SQLite read-only for preflight, closes that connection, and only then opens a separate read-write connection to initialize or change the normalized schema. Database identity writes are transactional.

Selected regular artifacts are renamed into the runtime root. An allowed internal leaf symlink is recreated as an internal target symlink and its legacy leaf is removed only after target verification. External leaf symlinks, symlinked ancestors, non-regular artifacts, and differing bytes for one required target fail preflight.

For multipart works, cd1 is authoritative for poster, background, NFO, and fanart. cd2 and later work-level copies are deleted only after the authoritative target hash is verified. Part subtitles from every valid legacy root are retained as `{code}.{index}.{ext}` or `{code}-cdN.{index}.{ext}`. When duplicate roots describe the same part, the exact-code directory supplies work-level artifacts; other roots may still supply unique subtitles. Legacy artifact directories are removed with `Remove` only when empty. The migration never uses recursive deletion.

## Resume

Reuse the same `--journal` path after an interruption. Journal v2 writes the immutable operation plan once, including the stable database-plan ID, operation IDs, paths, and expected hashes. Each placement, verification, cleanup, or directory-removal state is appended as a compact synced event to `<journal>.progress`, avoiding whole-plan rewrites as the migration grows. Recovery replays complete events, ignores only a truncated final partial line, and rejects malformed complete events, unknown operations or states, and plan mismatches. It then reconciles filesystem truth with expected hashes and continues idempotently, including interruption after rename, deletion, or deterministic temporary-symlink creation.

Journal v1 represented copy-and-retain behavior and is not compatible. The command fails closed when a v1 journal is present; archive or remove that journal only after independently verifying that no interrupted migration needs recovery, then run dry-run again.

## Legacy Retention

Migration never deletes legacy `Film` or `MagnetCache` rows, including rows reported as skipped. Selected artifact files are moved, and redundant work-level files are verification-gated cleanup candidates. Unrecognized files remain untouched and keep their legacy directory nonempty.
