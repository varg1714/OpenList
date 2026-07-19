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

Dry-run reports identity, artifact, and collision counts. It does not write normalized rows, artifacts, or the journal.

## Storage Mapping

When one storage uses a supported media driver, its storage ID is selected automatically. With multiple storages for the same source, provide an explicit mapping for each legacy directory:

```text
--storage-map 'javdb:Actor A=12'
--storage-map 'javdb:个人收藏=13'
```

The command refuses ambiguous sources without a mapping. It never guesses between storage IDs.

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

The command prints a JSON report. Database identity writes are transactional. Artifact copies are atomic and verified by bytes. Conflicting target content or unsafe artifact paths returns a nonzero exit status.

## Resume

Reuse the same `--journal` path after an interruption. Completed artifact actions are recognized by their journal state and target bytes; pending actions are retried safely.

## Legacy Retention

Migration never deletes legacy tables, rows, or source artifact files. Legacy files remain available for rollback and manual comparison. Remove them only after an independent backup and verification process.
