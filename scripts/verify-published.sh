#!/usr/bin/env bash
# Pulls one platform of the published image BY DIGEST and boots it.
#
# By digest, not by tag: a tag reference can be satisfied from the local image
# store, which would mean smoke-testing the image that was just built rather
# than the one in the registry. A digest reference cannot.
set -euo pipefail

cd "$(dirname "$0")/.."
IMAGE_REF="${1:?usage: verify-published.sh <image-ref> <platform>}"
PLATFORM="${2:?usage: verify-published.sh <image-ref> <platform>}"

ref="$(scripts/platform-ref.sh "${IMAGE_REF}" "${PLATFORM}")"
echo "verify-published: ${PLATFORM} -> ${ref}"

docker pull --platform "${PLATFORM}" --quiet "${ref}" >/dev/null
scripts/smoke-image.sh "${ref}"
