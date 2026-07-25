# Media Migration

`migrate-media` is a standalone operator command. Normal service startup does not migrate legacy media rows or files.

## Before You Start

Stop OpenList and make a consistent backup of the SQLite database and the data directory. Keep the backup until normalized media has been verified in production.

## Build a Debian amd64 Package on OrbStack

Start OrbStack on an Apple Silicon Mac and confirm that its Docker daemon is available:

```sh
open -a OrbStack
docker info --format '{{.ServerVersion}} {{.OSType}}/{{.Architecture}}'
docker run --rm --platform linux/amd64 golang:1.25.4-alpine \
  sh -c 'command -v go && go version'
```

Build an x86-64 Linux binary with CGO enabled for SQLite and link it statically against musl:

```sh
mkdir -p dist
docker run --rm --platform linux/amd64 \
  -v "$PWD:/src" \
  -w /src \
  golang:1.25.4-alpine \
  sh -c "apk add --no-cache gcc musl-dev && \
    CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
    go build -buildvcs=false -trimpath \
      -tags='osusergo netgo sqlite_omit_load_extension' \
      -ldflags='-s -w -linkmode external -extldflags \"-static\"' \
      -o dist/migrate-media-linux-amd64 ./cmd/migrate-media"
```

Use `sh -c`, not `sh -lc`. The Alpine login shell resets the image PATH and may report `go: not found` even though Go is installed at `/usr/local/go/bin/go`.

Create a single-file ZIP, then verify its contents, checksums, architecture, static linkage, and execution on Debian Bookworm:

```sh
zip -j -FS \
  dist/migrate-media-linux-amd64.zip \
  dist/migrate-media-linux-amd64

file dist/migrate-media-linux-amd64
unzip -t dist/migrate-media-linux-amd64.zip
unzip -Z1 dist/migrate-media-linux-amd64.zip
shasum -a 256 \
  dist/migrate-media-linux-amd64 \
  dist/migrate-media-linux-amd64.zip

docker run --rm --platform linux/amd64 \
  -v "$PWD/dist/migrate-media-linux-amd64:/usr/local/bin/migrate-media:ro" \
  debian:bookworm-slim \
  /usr/local/bin/migrate-media --help
```

`file` must report an x86-64 statically linked ELF executable, `unzip -Z1` must list only `migrate-media-linux-amd64`, and the Debian smoke test must exit successfully.

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

For multipart works, cd1 is authoritative for poster, background, NFO, and fanart. cd2 and later work-level copies are deleted only after the authoritative target hash is verified. Part subtitles from every valid legacy root are retained as `{code}.{index}.{ext}` or `{code}-cdN.{index}.{ext}`. When duplicate roots describe the same part, the exact-code directory supplies work-level artifacts; other roots may still supply unique subtitles. After planned artifact placement and verification, legacy artifact roots are removed recursively, including unrecognized files such as `.strm`. Under each database-owned `{source}/{primaryDir}` parent, directories that are not canonical roots of either a legacy film or an existing normalized work are also removed recursively.

## Resume

Reuse the same `--journal` path after an interruption. Journal v2 writes the immutable operation plan once, including the stable database-plan ID, operation IDs, paths, and expected hashes. Each placement, verification, cleanup, or directory-removal state is appended as a compact synced event to `<journal>.progress`, avoiding whole-plan rewrites as the migration grows. Recovery replays complete events, ignores only a truncated final partial line, and rejects malformed complete events, unknown operations or states, and plan mismatches. It then reconciles filesystem truth with expected hashes and continues idempotently, including interruption after rename, deletion, or deterministic temporary-symlink creation.

Journal v1 represented copy-and-retain behavior and is not compatible. The command fails closed when a v1 journal is present; archive or remove that journal only after independently verifying that no interrupted migration needs recovery, then run dry-run again.

## Legacy Retention

Migration never deletes legacy `Film` or `MagnetCache` rows, including rows reported as skipped. Selected artifact files are moved, and redundant work-level files are verification-gated cleanup candidates. Legacy and database-orphan artifact directories are then removed recursively, so keep the data-directory backup until production verification is complete.
