#!/usr/bin/env bash
# Proves the published image for one platform is bit-for-bit what this tree
# builds.
#
# Compares IMAGE MANIFEST digests, not index digests. The index carries
# attestation manifests whose contents include build timestamps and random ids,
# so index digests are not expected to match and never will. Do not "fix" this
# to compare them.
set -euo pipefail

cd "$(dirname "$0")/.."
IMAGE_REF="${1:?usage: verify-reproducible.sh <image-ref> <platform>}"
PLATFORM="${2:?usage: verify-reproducible.sh <image-ref> <platform>}"

published="$(scripts/platform-ref.sh "${IMAGE_REF}" "${PLATFORM}")"
published_digest="${published##*@}"

echo "verify-repro: ${PLATFORM}"
echo "  published: ${published_digest}"

rebuilt="$(scripts/repro-digest.sh "${PLATFORM}")"
echo "  rebuilt:   ${rebuilt}"

if [ "${published_digest}" != "${rebuilt}" ]; then
  echo "verify-repro: MISMATCH — the published image is not what this tree builds" >&2
  echo "  check that VERSION, REVISION and SOURCE_DATE_EPOCH match the release," >&2
  echo "  and that the buildkit version is the one the release used" \
       "($(make -s print-buildkit-image))" >&2
  exit 1
fi

echo "verify-repro: ${PLATFORM} reproduces exactly"
