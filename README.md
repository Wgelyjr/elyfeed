# elyfeed

A single-user RSS feed reader: a Go + Postgres backend that also serves an
installable React (Vite + TypeScript) PWA frontend.

One binary. No auth. Point it at a Postgres, open it in a browser, and install
it to your home screen.

## Features

- Add/remove RSS 2.0 and Atom feeds
- Background refresh on a fixed interval
- Read/unread tracking
- Works offline: the app shell and recently fetched items are cached and
  restored on load (service worker + persisted React Query cache)
- Installable as a PWA (manifest + maskable icons)

## Stack

- **Backend**: Go (stdlib `net/http` mux, `go:embed` for the frontend)
- **Database**: PostgreSQL 16 (via podman compose)
- **Frontend**: React 18, Vite, TypeScript, TanStack Query, vite-plugin-pwa

## Requirements

- Go 1.22+
- Node 18+ and npm
- podman (or docker) with the compose plugin
- ImageMagick (only if regenerating icons)

## Quick start

```sh
# 1. Build the frontend into the Go embed dir, then the binary
make build

# 2. Start Postgres
make db-up

# 3. Run the server (listens on :8080)
make run
```

Open http://localhost:8080, add a feed URL, and hit **Refresh**.

To develop the frontend with hot reload (proxies `/api` to `:8080`):

```sh
make dev   # in one terminal (requires the Go server running on :8080)
make run   # in another terminal
```

## Configuration

All configuration is via environment variables.

| Variable           | Default                                                                 | Description                          |
| ------------------ | ----------------------------------------------------------------------- | ------------------------------------ |
| `DATABASE_URL`     | `postgres://elyfeed:elyfeed@localhost:5432/elyfeed?sslmode=disable`     | Postgres DSN (required)              |
| `HOST`             | `0.0.0.0`                                                               | Bind address                         |
| `PORT`             | `8080`                                                                  | Listen port                          |
| `REFRESH_INTERVAL` | `10m`                                                                   | Background feed refresh interval     |
| `FEED_USER_AGENT`  | `elyfeed/1.0 (+https://github.com/wgelyjr/elyfeed)`                     | User-Agent sent when fetching feeds  |

Example:

```sh
PORT=9000 REFRESH_INTERVAL=5m DATABASE_URL="postgres://user:pass@localhost:5432/elyfeed?sslmode=disable" go run .
```

## API

| Method   | Path                     | Description                                   |
| -------- | ------------------------ | --------------------------------------------- |
| `GET`    | `/api/feeds`             | List feeds                                    |
| `POST`   | `/api/feeds`             | Add a feed (fetches + seeds it), body `{url}` |
| `DELETE` | `/api/feeds/{id}`        | Remove a feed and its items                   |
| `GET`    | `/api/items`             | List items; query `feed_id`, `unread`, `limit`, `offset` |
| `GET`    | `/api/items/unread-count`| Total unread item count                       |
| `POST`   | `/api/items/{id}/read`   | Set an item read/unread, body `{read}`        |
| `POST`   | `/api/refresh`           | Refresh all feeds now                         |

## Project layout

```
main.go                     entrypoint: config -> db -> store -> refresher -> server
internal/config             env-based configuration
internal/db                 pgxpool + schema migration
internal/store              Store interface + Postgres implementation
internal/rss                RSS 2.0 + Atom parser (stdlib encoding/xml)
internal/refresh            background feed refresher
internal/server             HTTP handlers + embedded SPA serving
internal/web/embed.go       go:embed of the built frontend
internal/web/dist           built frontend output (generated; placeholder tracked)
web/                        Vite + React + TS frontend source
compose.yml                 podman/docker compose for Postgres
scripts/make-icons.sh       PWA icon generation
```

## Notes

- The frontend is embedded into the Go binary at build time. `make build`
  (re)builds the frontend into `internal/web/dist` before building the binary.
  A placeholder `internal/web/dist/index.html` is tracked so `go build` works on
  a fresh clone before the frontend has ever been built.
- Feed content is reduced to plain text and truncated (~2000 chars). HTML from
  feeds is not rendered, which avoids injecting feed-provided markup.
