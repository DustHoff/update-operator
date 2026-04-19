#!/bin/bash
# patch.sh – runs inside the update container on the target node.
#
# The container image ships a local mirror of the apt repositories so that
# nodes can be updated without internet access.  This script:
#   1. Copies the repository lists from the image into the host filesystem
#   2. Copies GPG keyrings needed for signature verification
#   3. Runs apt-get inside the host's mount/PID namespace
#
# Environment variables (set by NodeUpdateReconciler):
#   HOLDPKG     – space-separated list of packages to pin (apt-mark hold)
#   INSTALLPKG  – space-separated list of packages to install/upgrade;
#                 if empty a full dist-upgrade is performed instead

set -euo pipefail

HOST=/host

echo "[patch] copying apt repository lists to host"
mkdir -p "${HOST}/etc/apt/sources.list.d"
cp /patch/source/*.list "${HOST}/etc/apt/sources.list.d/"

echo "[patch] copying GPG keyrings to host"
if [ -d /patch/keyrings ] && [ "$(ls -A /patch/keyrings 2>/dev/null)" ]; then
  mkdir -p "${HOST}/etc/apt/trusted.gpg.d"
  cp /patch/keyrings/. "${HOST}/etc/apt/trusted.gpg.d/"
fi

# All subsequent commands run inside the host's mount / network / PID namespaces
# so that apt sees the real host packages and can reach the copied repository lists.
NSENTER="nsenter --target 1 --mount --uts --ipc --net --pid --"

echo "[patch] updating apt cache"
${NSENTER} apt-get update -y

if [ -n "${HOLDPKG:-}" ]; then
  echo "[patch] holding packages: ${HOLDPKG}"
  # shellcheck disable=SC2086
  ${NSENTER} apt-mark hold ${HOLDPKG}
fi

if [ -n "${INSTALLPKG:-}" ]; then
  echo "[patch] installing/upgrading packages: ${INSTALLPKG}"
  # shellcheck disable=SC2086
  ${NSENTER} apt-get install -y --no-install-recommends ${INSTALLPKG}
else
  echo "[patch] running full dist-upgrade"
  ${NSENTER} apt-get dist-upgrade -y
fi

echo "[patch] cleaning up"
${NSENTER} apt-get autoremove -y
${NSENTER} apt-get clean

echo "[patch] done"
