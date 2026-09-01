# AGENTS.md

elyfeed: single-user RSS reader. Go + Postgres backend that also serves an
embedded React (Vite + TS) PWA. Toolchain: Go 1.25+, Node 22 (Dockerfile).

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

Deploys go through GitHub Actions (`.github/workflows/ci.yml`): a push to
`main` (or `staging`) builds and tests in the cloud, then runs the host's
deploy script via a self-hosted runner labeled `prod` / `staging`. Deploys
only run after the test job passes. Manual re-deploys: the Actions page
(`workflow_dispatch`).

- Runners: `RUNNER_TOKEN=<token> ./scripts/install-runner.sh <labels>` on
  each host, where `<labels>` is `prod`, `staging`, or `prod,staging` for a
  host serving both (one-time token from repo Settings → Actions → Runners).
  Run as the user the deploy scripts run as (root on chesster — the script
  handles root via `RUNNER_ALLOW_RUNASROOT`); it registers an
  `actions.runner.Wgelyjr-elyfeed.<name>.service` systemd unit. The
  `production` label is required for public repos — the script adds it.
- Deploy jobs route on `[self-hosted, linux, prod]` / `[self-hosted, linux,
  staging]`. Do not add `amd64` there: the runner's auto-assigned arch
  label is `x64`, so an `amd64` requirement would never match.
- The old polling systemd service (`elyfeed-deploy.timer` on chesster) has
  been retired now that CI deploys land; do not re-enable it.
- Manual deploy (exactly what the runner runs):

```sh
# Production host:
cd /opt/elyfeed && ./scripts/deploy.sh
```

Local stack commands:

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
Pushing to `staging` auto-deploys through the runner; manual deploy on the
host:

```sh
cd /opt/elyfeed-staging && ./scripts/deploy-staging.sh
```

- `ELYFEED_DEV=true` there, so verification links print to `podman logs
  elyfeed-staging` instead of being emailed.
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
- Auth exists (password + SMTP, or OIDC) but there is no network-level
  isolation: never expose the port directly to untrusted networks — put a
  TLS-terminating reverse proxy in front (see README Security).
- Commits: one-line imperative message; run `make build && make test` before
  committing.
- Where things live: `internal/server` (HTTP + SPA serving), `internal/store`
  (persistence), `internal/rss` (parser), `internal/refresh` (background
  refresher), `web/` (frontend source).
