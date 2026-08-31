#!/usr/bin/env bash
# Install a GitHub Actions self-hosted runner for elyfeed on this host.
#
# Usage: RUNNER_TOKEN=<token> ./scripts/install-runner.sh <label>
#
# <label> must be "prod" (production host) or "staging" (chesster) — it
# matches the runs-on label the CI deploy jobs look for. Get a one-time
# registration token from:
#
#   https://github.com/Wgelyjr/elyfeed/settings/actions/runners/new
#
# Run this as the user the deploy scripts run as (needs podman access and
# the git deploy key). Installs to /opt/elyfeed-runner and registers a
# systemd service, so the runner survives reboots — replacing the old
# polling systemd service.
set -euo pipefail

LABEL="${1:?usage: RUNNER_TOKEN=<token> ./scripts/install-runner.sh <prod|staging>}"
case "$LABEL" in
  prod | staging) ;;
  *) echo "label must be 'prod' or 'staging'" >&2; exit 1 ;;
esac
: "${RUNNER_TOKEN:?set RUNNER_TOKEN to the one-time runner registration token}"

REPO_URL="https://github.com/Wgelyjr/elyfeed"
INSTALL_DIR="/opt/elyfeed-runner"

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
  *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

sudo mkdir -p "$INSTALL_DIR"
sudo chown "$(id -un)" "$INSTALL_DIR"
cd "$INSTALL_DIR"

if [ -x ./config.sh ]; then
  echo "a runner is already installed here; remove $INSTALL_DIR first to reinstall" >&2
  exit 1
fi

VERSION="$(
  curl -fsSL https://api.github.com/repos/actions/runner/releases/latest \
    | grep -o '"tag_name": *"v[0-9.]*"' | cut -d'"' -f4 | tr -d 'v'
)"
echo "installing actions-runner v${VERSION} (linux-${ARCH})"
curl -fsSL -o runner.tar.gz \
  "https://github.com/actions/runner/releases/download/v${VERSION}/actions-runner-${VERSION}-linux-${ARCH}.tar.gz"
tar xzf runner.tar.gz
rm runner.tar.gz

# "production" is required for runners serving public repos.
./config.sh \
  --url "$REPO_URL" \
  --token "$RUNNER_TOKEN" \
  --name "elyfeed-${LABEL}" \
  --labels "${LABEL},production" \
  --work "$INSTALL_DIR/work" \
  --replace

sudo ./svc.sh install
./svc.sh start

echo
echo "runner 'elyfeed-${LABEL}' installed and running (systemd service)."
echo "Verify at: $REPO_URL/settings/actions/runners"
