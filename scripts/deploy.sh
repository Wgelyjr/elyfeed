#!/usr/bin/env bash
# Deploy elyfeed to production.
#
# Run this on the production host from /opt/elyfeed: it pulls main,
# rebuilds the image, and restarts the stack. Safe to run repeatedly.
# Triggered by GitHub Actions on push to main (self-hosted runner), and
# can be run manually at any time.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="elyfeed:latest"

cd "$ROOT"
SELF_HASH="$(git hash-object scripts/deploy.sh)"
git fetch origin
git checkout main
git pull --ff-only
# Re-exec if this pull updated this script: bash holds the old copy in
# memory, so without this the new version would never run.
if [ "$(git hash-object scripts/deploy.sh)" != "$SELF_HASH" ]; then
  exec bash "$ROOT/scripts/deploy.sh"
fi

# --network host: needed on hosts without /dev/net/tun, where podman's
# default build networking (pasta/slirp4netns) cannot be set up. Harmless
# elsewhere.
podman build --network host -t "$IMAGE" .

# Mirror elyfeed-stack.service's environment (EnvironmentFile=.env plus
# ELYFEED_PORT=8080): podman-compose 1.3.0 does not read .env files, so the
# vars must be in its process environment or the app config (BASE_URL,
# SMTP, OIDC, ...) comes up empty.
set -a
if [ -f .env ]; then
  . ./.env
fi
set +a
export ELYFEED_PORT="${ELYFEED_PORT:-8080}"

# --force-recreate: podman-compose does not detect image changes on its own.
podman-compose up -d --force-recreate

# Some hosts' netavark leaves stale port-publish nftables rules behind;
# prune any that point at stopped containers so the published port works.
if [ -x /usr/local/bin/elyfeed-netavark-heal.sh ]; then
  /usr/local/bin/elyfeed-netavark-heal.sh
fi

echo "production deployed"
