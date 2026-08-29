#!/usr/bin/env bash
# Deploy elyfeed to the staging host (chesster, 10.55.1.13).
#
# Run this on the staging host itself, from /opt/elyfeed-staging: it pulls
# the staging branch, rebuilds the image, and restarts the stack. Safe to
# run repeatedly. The stack also survives reboots via the
# elyfeed-staging.service systemd unit.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="localhost/elyfeed-staging:latest"

cd "$ROOT"
git fetch origin
git checkout staging
git pull --ff-only

# --network host: this host is a KVM guest without /dev/net/tun, so
# podman's default build networking (pasta/slirp4netns) cannot be set up.
podman build --network host -t "$IMAGE" .

# --force-recreate: podman-compose does not detect image changes on its own.
podman-compose -f compose.staging.yml up -d --force-recreate

# This host's netavark leaves stale port-publish nftables rules behind;
# prune any that point at stopped containers so the published port works.
if [ -x /usr/local/bin/elyfeed-netavark-heal.sh ]; then
  /usr/local/bin/elyfeed-netavark-heal.sh
fi

echo "staging deployed: http://10.55.1.13:2999"
