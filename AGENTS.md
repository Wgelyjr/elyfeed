# AGENTS.md

elyfeed: single-user RSS reader. Go + Postgres backend that also serves an
embedded React (Vite + TS) PWA. No auth. Toolchain: Go 1.24+, Node 18+.

## Build

```sh
make build   # builds web/ into internal/web/dist, then the Go binary (bin/elyfeed)
```

- Frontend only: `cd web && npm run build` (runs `tsc --noEmit && vite build`)
- Frontend typecheck: `cd web && npx tsc --noEmit`
- The frontend is embedded into the binary at build time. After changing
  `web/`, run `make build` — `internal/web/dist` is gitignored except a
  placeholder `index.html`, so commits only need the placeholder update.

## Test

```sh
make test    # go test ./...
make vet     # go vet ./...
```

There is no frontend test suite; the `tsc --noEmit` in the web build is the
only frontend check.

## Run (dev)

```sh
make db-up   # Postgres in podman
make run     # server on :8080 (needs DATABASE_URL or the compose default)
make dev     # optional: Vite dev server with HMR, proxies /api to :8080
```

## Deploy

```sh
make up      # podman compose: builds image, starts db + app (app on host :8180)
make logs    # follow app + db logs
make down    # stop/remove; data volume is preserved
```

- Override host port: `ELYFEED_PORT=9000 make up`
- Discard the database too: `podman compose down -v`

### Staging (chesster host)

Staging runs the `staging` branch at http://10.55.1.13:2999, isolated from
production via `compose.staging.yml` (own containers, image tag, and volume).

```sh
# On the staging host: pull the staging branch, rebuild, and redeploy.
cd /opt/elyfeed-staging && ./scripts/deploy-staging.sh
```

- `ELYFEED_DEV=true` there, so verification links print to `podman logs
  elyfeed-staging` instead of being emailed.
- The stack is kept up by the `elyfeed-staging.service` systemd unit.
- Never run `make up` / `podman compose up` in `/opt/elyfeed` (production)
  when targeting staging, and vice versa.

## Notes

- `GET /api` returns a JSON index of all endpoints (self-describing API);
  good way to verify changes against a running server with curl.
- DB schema lives in `internal/db/migrations.sql`, applied at startup.
- No linters configured; `go vet` + `tsc --noEmit` are the only checks.
- The PWA service worker caches assets — after a frontend change, do a
  hard reload (or unregister the SW) before judging a running deploy.
- Env config: `DATABASE_URL`, `HOST`, `PORT`, `REFRESH_INTERVAL`,
  `FEED_USER_AGENT` (see README).
- No auth anywhere: never expose the port to untrusted networks.
- Commits: one-line imperative message; run `make build && make test` before
  committing.
- Where things live: `internal/server` (HTTP + SPA serving), `internal/store`
  (persistence), `internal/rss` (parser), `internal/refresh` (background
  refresher), `web/` (frontend source).
