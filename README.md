# elyfeed

I'm sorry about the bad name

A multi-user RSS feed reader: a Go + Postgres backend that also serves an
installable React (Vite + TypeScript) PWA frontend. Local accounts with email
verification (or generic OIDC login), per-user data isolation, compose
deployment.


<p align="center">
  <img src="docs/screenshots/all-feeds-light.png" width="920" alt="elyfeed - the All feeds view">
</p>

## Features

- **Auth** - local accounts with self-registration and email verification
  (SMTP), or generic OIDC login. Forgot-password / reset included. The first
  user to activate becomes the admin. Sessions are 30-day cookies (configurable).
- **Isolation** - feeds, collections and items belong to the signed-in user;
  the cache in the browser is per-user too.
- **Feeds** - add RSS 2.0 and Atom feeds one at a time, or paste a whole list
  of URLs in bulk. Background refresh on a fixed interval. Feed fetching has an
  SSRF guard (private/loopback/metadata addresses are refused by default).
- **Collections** - group feeds into named collections and read them together;
  move feeds between collections straight from the sidebar.
- **Reading** - All / Unread / Read filter with unread counts, infinite scroll,
  auto-mark-as-read as items scroll into view, and a "mark visible as read"
  shortcut for the unread queue.
- **Manage mode** - select feeds with checkboxes and bulk-delete them.
- **Offline** - the app shell and recently fetched items are cached per user
  and restored on load (service worker + persisted React Query cache).
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

Open http://localhost:8080 and **create an account**. `make run` starts in dev
mode (`ELYFEED_DEV=true`), so the verification email is printed to the server
log instead of being sent — open the link in it, then add a feed URL and hit
**Refresh**.

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

Then open http://localhost:8180 (the app's host port; see note below), create
an account, add a feed URL, and hit **Refresh**.

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
- The compose defaults are a **local dev deployment**: `ELYFEED_DEV=true` lets
  the app start without SMTP/OIDC, and verification links are printed to the
  log (watch `make logs`). For a real deployment, set `SMTP_*` (or `OIDC_*`
  plus `BASE_URL`) and `ELYFEED_DEV=false` — e.g. via a `.env` file next to
  `compose.yml` — and put a TLS-terminating reverse proxy in front (see
  [Security](#security)).

## Staging

A staging deployment of the `staging` branch runs on the `chesster` host at
<http://10.55.1.13:2999>, kept up by the `elyfeed-staging.service` systemd
unit. It is fully isolated from the production stack — separate checkout
(`/opt/elyfeed-staging`), container names, image tag
(`localhost/elyfeed-staging:latest`), and data volume — via
`compose.staging.yml`.

To deploy changes to staging, on the host run:

```sh
cd /opt/elyfeed-staging && ./scripts/deploy-staging.sh
```

The script pulls the `staging` branch, rebuilds the image, and restarts the
stack. Staging runs with `ELYFEED_DEV=true` (no SMTP), so email
verification links are printed to the container log instead of being sent:

```sh
podman logs elyfeed-staging | grep -o 'http://[^"]*verify?token=[a-f0-9]*' | tail -1
```

## Configuration

All configuration is via environment variables. The app refuses to start
unless `SMTP_HOST` or `OIDC_ISSUER` is set, unless `ELYFEED_DEV=true`.

| Variable             | Default                                                             | Description                                                                                                          |
| -------------------- | ------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `DATABASE_URL`       | `postgres://elyfeed:elyfeed@localhost:5432/elyfeed?sslmode=disable` | Postgres DSN (required)                                                                                              |
| `HOST`               | `0.0.0.0`                                                           | Bind address                                                                                                         |
| `PORT`               | `8080`                                                              | Listen port                                                                                                          |
| `REFRESH_INTERVAL`   | `10m`                                                               | Background feed refresh interval (Go duration; `0` disables)                                                         |
| `FEED_USER_AGENT`    | `elyfeed/1.0 (+https://github.com/wgelyjr/elyfeed)`                 | User-Agent sent when fetching feeds                                                                                  |
| `BASE_URL`           | _(empty)_                                                           | Public origin, e.g. `https://feeds.example.com`. Used for absolute links in email and the OIDC redirect URL. When it starts with `https://`, session cookies are marked `Secure`. Required when OIDC is enabled. |
| `SESSION_TTL`        | `720h` (30 days)                                                    | Login session lifetime (Go duration)                                                                                 |
| `ELYFEED_DEV`        | `false`                                                             | Local development: allows starting without SMTP/OIDC; verification/reset emails are printed to the log               |
| `SMTP_HOST`          | _(empty)_                                                           | SMTP server for verification/reset emails (enables real email)                                                       |
| `SMTP_PORT`          | `587`                                                               | SMTP port                                                                                                            |
| `SMTP_USER`          | _(empty)_                                                           | SMTP username                                                                                                        |
| `SMTP_PASS`          | _(empty)_                                                           | SMTP password                                                                                                        |
| `SMTP_FROM`          | `elyfeed <no-reply@localhost>`                                      | From address                                                                                                         |
| `SMTP_IMPLICIT_TLS`  | `false`                                                             | Use implicit TLS (port 465 style) instead of STARTTLS                                                                |
| `OIDC_ISSUER`        | _(empty)_                                                           | OIDC issuer URL; enables OIDC login (also requires `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, and `BASE_URL`)           |
| `OIDC_CLIENT_ID`     | _(empty)_                                                           | OIDC client ID                                                                                                       |
| `OIDC_CLIENT_SECRET` | _(empty)_                                                           | OIDC client secret                                                                                                   |
| `OIDC_SCOPES`        | `openid email profile`                                              | Comma-separated OIDC scopes                                                                                          |
| `FEED_ALLOW_PRIVATE` | `false`                                                             | Allow feed URLs that resolve to private/loopback/link-local/metadata addresses (development convenience; an SSRF risk on public deployments) |

Example:

```sh
PORT=9000 REFRESH_INTERVAL=5m DATABASE_URL="postgres://user:pass@localhost:5432/elyfeed?sslmode=disable" go run .
```

## API

`GET /api` (or `GET /api/`) returns a JSON index of all endpoints — the API is
self-describing, which makes it easy for LLMs and other automation to discover.
Each endpoint carries an `auth` flag (`true` when a session is required), and
the index includes the current user under `user` when the request is
authenticated (`null` otherwise).

All data endpoints require a session (the `elyfeed_session` cookie set by
login). Auth endpoints are public. Mutating endpoints require
`Content-Type: application/json` (415 otherwise) and reject bodies over 1 MiB
(413). Login and registration are rate-limited (429).

| Method   | Path                     | Description                                   |
| -------- | ------------------------ | --------------------------------------------- |
| `GET`    | `/api`                   | Endpoint index (JSON)                         |
| `POST`   | `/api/auth/register`     | Create an account, body `{email, name, password}`; sends a verification email |
| `POST`   | `/api/auth/login`        | Log in, body `{email, password}`; sets the session cookie |
| `POST`   | `/api/auth/logout`       | Log out; clears the session cookie            |
| `GET`    | `/api/auth/me`           | Current user (401 when logged out)            |
| `GET`    | `/api/auth/verify?token=`| Verify the email from the link in the email; logs the user in and redirects to `/` |
| `POST`   | `/api/auth/forgot-password` | Body `{email}`; sends a reset email (always 200) |
| `POST`   | `/api/auth/reset-password` | Body `{token, password}`; sets a new password |
| `GET`    | `/api/auth/oidc`         | Start OIDC login (302 to the provider); only when OIDC is configured |
| `GET`    | `/api/auth/oidc/callback`| OIDC redirect target                          |
| `GET`    | `/api/feeds`             | List feeds                                    |
| `POST`   | `/api/feeds`             | Add a feed (fetches + seeds it), body `{url}` |
| `DELETE` | `/api/feeds/{id}`        | Remove a feed and its items                   |
| `GET`    | `/api/items`             | List items; query `feed_id`, `collection_id`, `unread`, `since`, `until` (RFC3339), `limit`, `offset` |
| `GET`    | `/api/items/unread-count`| Total unread item count                       |
| `POST`   | `/api/items/{id}/read`   | Set an item read/unread, body `{read}`        |
| `GET`    | `/api/digest`            | LLM-ready digest of a collection; query `collection_id` (required), `since`, `until` (RFC3339, default: last 24h), `format` (`markdown` default / `json`), `limit` |
| `POST`   | `/api/refresh`           | Refresh all feeds now                         |

### Automation (LLM digests, cron jobs)

The API is designed to be consumed directly by external automation. Log in
once with `curl -c` to save the session cookie, then reuse it:

```sh
# Log in and save the session cookie
curl -s -c /tmp/elyfeed-cookies.txt -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"…"}' \
  http://localhost:8180/api/auth/login

# Markdown digest of everything new in collection 1 over the last 24h
curl -s -b /tmp/elyfeed-cookies.txt "http://localhost:8180/api/digest?collection_id=1"

# Explicit window (RFC3339), as JSON for further processing
curl -s -b /tmp/elyfeed-cookies.txt "http://localhost:8180/api/digest?collection_id=1&since=2026-08-22T09:00:00Z&until=2026-08-23T09:00:00Z&format=json"

# Or just the raw time-window query, for any collection/feed
curl -s -b /tmp/elyfeed-cookies.txt "http://localhost:8180/api/items?collection_id=1&since=2026-08-22T09:00:00Z&limit=100"
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

## Security

- **Put a reverse proxy in front.** Auth (accounts, rate limiting, SSRF guard)
  protects the data, but there is no network-level isolation: don't expose the
  port directly to untrusted networks. Terminate TLS at a reverse proxy and set
  `BASE_URL` to the public https origin — session cookies are then marked
  `Secure` automatically.
- **Email or OIDC is required for real deployments.** `ELYFEED_DEV=true` exists
  for local development only (it lets the app start without SMTP/OIDC and prints
  verification links to the log).
- **`FEED_ALLOW_PRIVATE`** disables the SSRF guard for private/loopback feed
  addresses (useful for self-hosted LAN feeds). Leave it `false` on public
  deployments.
- **Postgres is not exposed.** The compose db has no host port; the app reaches
  it over the compose network.
- **Sessions** are stored as SHA-256 hashes server-side, last `SESSION_TTL`
  (30 days by default), and are bound to an `HttpOnly`, `SameSite=Lax` cookie.
  Mutating API routes additionally require `Content-Type: application/json` as
  a CSRF mitigation.

## Project layout

```
main.go                     entrypoint: config -> db -> store -> auth -> refresher -> server
internal/config             env-based configuration
internal/db                 pgxpool + versioned schema migrations
internal/store              Store interface + Postgres implementation
internal/auth               accounts, sessions, tokens, rate limiting, mailer, OIDC
internal/rss                RSS 2.0 + Atom parser (stdlib encoding/xml) + SSRF guard
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
