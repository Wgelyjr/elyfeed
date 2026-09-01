#!/usr/bin/env bash
# Install a GitHub Actions self-hosted runner for elyfeed on this host.
#
# Usage: RUNNER_TOKEN=<token> ./scripts/install-runner.sh <labels>
#
# <labels> is a comma-separated list drawn from "prod" and "staging" —
# each matches a runs-on label the CI deploy jobs look for. A host serving
# both environments uses "prod,staging". Get a one-time registration token from:
#
#   https://github.com/Wgelyjr/elyfeed/settings/actions/runners/new
#
# Run this as the user the deploy scripts run as (needs podman access and
# the git deploy key). Installs to /opt/elyfeed-runner and registers a
# systemd service, so the runner survives reboots — replacing the old
# polling systemd service.
set -euo pipefail

LABELS="${1:?usage: RUNNER_TOKEN=<token> ./scripts/install-runner.sh <prod|staging|prod,staging>}"
IFS=',' read -ra _label_parts <<< "$LABELS"
for _p in "${_label_parts[@]}"; do
  case "$_p" in
    prod | staging) ;;
    *) echo "each label must be 'prod' or 'staging'" >&2; exit 1 ;;
  esac
done
: "${RUNNER_TOKEN:?set RUNNER_TOKEN to the one-time runner registration token}"

# Run as root without sudo, or via sudo when unprivileged.
SUDO=""
if [ "$(id -u)" -ne 0 ]; then
  SUDO="sudo"
fi

REPO_URL="https://github.com/Wgelyjr/elyfeed"
INSTALL_DIR="/opt/elyfeed-runner"
# Unique runner name per label set, e.g. elyfeed-prod or elyfeed-prod-staging.
RUNNER_NAME="elyfeed-$(printf '%s' "$LABELS" | tr ',' '-')"

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH=x64 ;;
  aarch64) ARCH=arm64 ;;
  *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

$SUDO mkdir -p "$INSTALL_DIR"
$SUDO chown "$(id -un)" "$INSTALL_DIR"
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
  "https://github.com/actions/runner/releases/download/v${VERSION}/actions-runner-linux-${ARCH}-${VERSION}.tar.gz"
tar xzf runner.tar.gz
rm runner.tar.gz

# "production" is required for runners serving public repos.
# These hosts run the deploy scripts (and podman) as root, so allow the
# runner to run as root too.
RUNNER_ALLOW_RUNASROOT=1 ./config.sh \
  --url "$REPO_URL" \
  --token "$RUNNER_TOKEN" \
  --name "$RUNNER_NAME" \
  --labels "${LABELS},production" \
  --work "$INSTALL_DIR/work" \
  --replace

$SUDO ./svc.sh install
./svc.sh start

echo
echo "runner '$RUNNER_NAME' (labels: ${LABELS},production) installed and running (systemd service)."
echo "Verify at: $REPO_URL/settings/actions/runners"
