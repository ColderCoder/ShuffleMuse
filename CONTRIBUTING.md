# Contributing to ShuffleMuse

Thank you for helping improve ShuffleMuse.

## Development setup

Requirements:

- Go 1.24.4 or newer
- Bun
- FFmpeg and ffprobe
- Docker with the Compose plugin for container checks

Install and build the frontend before running the application:

```bash
cd web
bun install --frozen-lockfile
bun run build
cd ..
```

Run the backend with an explicit music root and data path:

```bash
MUSIC_DIR="$PWD/music" \
MUSIC_BOLTDB_PATH="$PWD/data/tags.db" \
go run ./cmd/server
```

## Required checks

Before opening a pull request, run:

```bash
go test ./...
go test -race ./...
go vet ./...

cd web
bun run test:run
bun run build

cd ..
docker compose config --quiet
docker compose -f docker-compose.build.yml config --quiet
docker compose -f docker-compose.build-cn.yml config --quiet
docker build -t shufflemuse:dev .
```

Keep the music directory read-only from the application's perspective. Do not
add generated media, databases, credentials, local `.env` files, frontend
dependencies, or build output to commits.

## Pull requests

- Keep changes focused and include regression tests for changed behavior.
- Update the API, configuration, operations, or user documentation when their
  public behavior changes.
- Do not introduce a server-side cache of converted cover image bytes.
- Preserve bounded queues and cancellation for media subprocesses.

Security vulnerabilities must be reported privately as described in
[SECURITY.md](SECURITY.md), not in a public issue.
