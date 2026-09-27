#!/usr/bin/env bash
# Pulls one platform of the published image BY DIGEST, and boots it when this
# machine can.
#
# By digest, not by tag: a tag reference can be satisfied from the local image
# store, which would mean smoke-testing the image that was just built rather
# than the one in the registry. A digest reference cannot.
#
# A foreign-architecture image is pulled but not booted. Emulating it would be
# a weaker check than what already covers it, not a stronger one:
#
#   preflight booted this build on native hardware for the platform, and
#   verify-repro-published proves the PUBLISHED image is bit-for-bit that same
#   build. Native boot of X, plus published == X, gives native boot of the
#   published image — without QEMU in the chain.
#
# Run this on the platform's own hardware to boot it there too.
set -euo pipefail

cd "$(dirname "$0")/.."
IMAGE_REF="${1:?usage: verify-published.sh <image-ref> <platform>}"
PLATFORM="${2:?usage: verify-published.sh <image-ref> <platform>}"

ref="$(scripts/platform-ref.sh "${IMAGE_REF}" "${PLATFORM}")"
echo "verify-published: ${PLATFORM} -> ${ref}"

docker pull --platform "${PLATFORM}" --quiet "${ref}" >/dev/null
echo "  pulled by digest"

# What this docker daemon runs natively, in the os/arch form PLATFORM uses.
host_platform="$(docker version --format '{{.Server.Os}}/{{.Server.Arch}}')"

if [ "${PLATFORM}" != "${host_platform}" ]; then
  echo "  not booting: this host is ${host_platform}, the image is ${PLATFORM}"
  echo "  covered instead by the native preflight boot plus verify-repro-published"
  exit 0
fi

scripts/smoke-image.sh "${ref}"
