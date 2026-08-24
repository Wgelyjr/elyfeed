# elyfeed

I'm sorry about the bad name

A single-user RSS feed reader: a Go + Postgres backend that also serves an
installable React (Vite + TypeScript) PWA frontend. No auth, compose deployment.


<p align="center">
  <img src="docs/screenshots/all-feeds-light.png" width="920" alt="elyfeed - the All feeds view">
</p>

## Features

- **Feeds** - add RSS 2.0 and Atom feeds one at a time, or paste a whole list
  of URLs in bulk. Background refresh on a fixed interval.
- **Collections** - group feeds into named collections and read them together;
  move feeds between collections straight from the sidebar.
- **Reading** - All / Unread / Read filter with unread counts, infinite scroll,
  auto-mark-as-read as items scroll into view, and a "mark visible as read"
  shortcut for the unread queue.
- **Manage mode** - select feeds with checkboxes and bulk-delete them.
- **Offline** - the app shell and recently fetched items are cached and
  restored on load (service worker + persisted React Query cache).
- **PWA** - installable to your home screen (manifest + maskable icons).
- **Responsive** - full desktop layout with a slide-in drawer nav on mobile.
- **Dark mode** - follows your system color scheme.

## Screenshots

| | |
| :---: | :---: |
| <img src="docs/screenshots/collection-tech.png" width="440" alt="Collection"><br>**Read a whole collection** | <img src="docs/screenshots/unread-queue.png" width="440" alt="Unread queue"><br>**Unread queue + mark visible as read** |
| <img src="docs/screenshots/bulk-add.png" width="440" alt="Bulk add"><br>**Bulk add feeds** | <img src="docs/screenshots/organize-feed.png" width="440" alt="Organize"><br>**Move feeds between collections** |
| <img src="docs/screenshots/manage-mode.png" width="440" alt="Manage mode"><br>**Manage mode: bulk delete** | <img src="docs/screenshots/all-feeds-dark.png" width="440" alt="Dark mode"><br>**Dark mode** |
| <img src="docs/screenshots/mobile-drawer.png" width="440" alt="Mobile"><br>**Mobile drawer** | |

## Stack

- **Backend**: Go (stdlib `net/http` mux, `go:embed` for the frontend)
- **Database**: PostgreSQL 16 (via podman compose)
- **Frontend**: React 18, Vite, TypeScript, TanStack Query, vite-plugin-pwa
- **Packaging**: multi-stage `Dockerfile` (web build → go build → minimal alpine)

## Requirements

- **Container run**: podman (or docker) with the compose plugin. That's it.
- **Native run/build**: Go 1.24+ and Node 18+ with npm
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

## Run with containers (simplest)

Build the image and start Postgres + the app together:

```sh
make up
```

Then open http://localhost:8180 (the app's host port; see note below), add a
feed URL, and hit **Refresh**.

```sh
make logs     # follow app + db logs
make down     # stop and remove (the data volume is preserved)
```

Notes:

- The app listens on port **8080 inside the container**. The host port defaults
  to **8180** because 8080 is commonly taken on dev machines. Override with
  `ELYFEED_PORT=9000 make up` (then use `http://localhost:9000`).
- The app waits (up to 60s) for Postgres to accept connections before
  failing, so container startup order is handled automatically.
- Feed data persists in the `elyfeed_pgdata` volume. `make down` does not drop
  it; use `podman compose down -v` to discard the database too.

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

`GET /api` (or `GET /api/`) returns a JSON index of all endpoints — the API is
self-describing, which makes it easy for LLMs and other automation to discover.

| Method   | Path                     | Description                                   |
| -------- | ------------------------ | --------------------------------------------- |
| `GET`    | `/api`                   | Endpoint index (JSON)                         |
| `GET`    | `/api/feeds`             | List feeds                                    |
| `POST`   | `/api/feeds`             | Add a feed (fetches + seeds it), body `{url}` |
| `DELETE` | `/api/feeds/{id}`        | Remove a feed and its items                   |
| `GET`    | `/api/items`             | List items; query `feed_id`, `collection_id`, `unread`, `since`, `until` (RFC3339), `limit`, `offset` |
| `GET`    | `/api/items/unread-count`| Total unread item count                       |
| `POST`   | `/api/items/{id}/read`   | Set an item read/unread, body `{read}`        |
| `GET`    | `/api/digest`            | LLM-ready digest of a collection; query `collection_id` (required), `since`, `until` (RFC3339, default: last 24h), `format` (`markdown` default / `json`), `limit` |
| `POST`   | `/api/refresh`           | Refresh all feeds now                         |

### Automation (LLM digests, cron jobs)

The API is designed to be consumed directly by external automation. A daily
digest is one request:

```sh
# Markdown digest of everything new in collection 1 over the last 24h
curl -s "http://localhost:8180/api/digest?collection_id=1"

# Explicit window (RFC3339), as JSON for further processing
curl -s "http://localhost:8180/api/digest?collection_id=1&since=2026-08-22T09:00:00Z&until=2026-08-23T09:00:00Z&format=json"

# Or just the raw time-window query, for any collection/feed
curl -s "http://localhost:8180/api/items?collection_id=1&since=2026-08-22T09:00:00Z&limit=100"
```

The markdown output is ready to paste into an LLM prompt:

```markdown
# Digest — News (2026-08-22T09:00:00Z → 2026-08-23T09:00:00Z)

## Feed A

- [Title](https://…) — author, 2026-08-22 10:00 UTC
  > content excerpt…
```

Notes:

- Items without a publication date are bounded by their fetch time instead,
  so undated feeds are never silently dropped from a window.
- Item content is plain text, truncated (~2000 chars); digest excerpts are
  trimmed to ~300 chars.
- There is no auth: keep the port unreachable from untrusted networks.

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
Dockerfile                  multi-stage image: web build -> go build -> alpine
.dockerignore               build-context exclusions
compose.yml                 podman/docker compose for Postgres + app
scripts/make-icons.sh       PWA icon generation
```

## Notes

- The frontend is embedded into the Go binary at build time. `make build`
  (re)builds the frontend into `internal/web/dist` before building the binary.
  A placeholder `internal/web/dist/index.html` is tracked so `go build` works on
  a fresh clone before the frontend has ever been built.
- Feed content is reduced to plain text and truncated (~2000 chars). HTML from
  feeds is not rendered, which avoids injecting feed-provided markup.
