#!/usr/bin/env bash
# Verifies whichever signing modes are configured, over the index and every
# platform image.
#
# A mode is checked only when its configuration is present, so this is usable
# both in the release (where it proves the signing step round-trips) and by a
# consumer with only one of the two.
#
#   COSIGN_IDENTITY  the exact certificate identity for keyless verification.
#   COSIGN_PUB       path to the public key for offline verification.
#   COSIGN_EXTRA     extra cosign flags, e.g. --allow-insecure-registry
#
# Signing something nothing verifies is the gap this exists to close.
set -euo pipefail

cd "$(dirname "$0")/.."
IMAGE_REF="${1:?usage: verify-signatures.sh <image-ref>}"

: "${COSIGN_IDENTITY:=}"
: "${COSIGN_PUB:=}"
: "${COSIGN_OIDC_ISSUER:=https://token.actions.githubusercontent.com}"

if [ -z "${COSIGN_IDENTITY}" ] && [ -z "${COSIGN_PUB}" ]; then
  echo "verify-sig: set COSIGN_IDENTITY and/or COSIGN_PUB" >&2
  exit 1
fi

read -r -a EXTRA <<<"${COSIGN_EXTRA:-}"

index_ref="$(make -s digest-ref)"
refs=("${index_ref}")
for platform in $(make -s print-platforms); do
  refs+=("$(scripts/platform-ref.sh "${IMAGE_REF}" "${platform}")")
done

if [ -n "${COSIGN_IDENTITY}" ]; then
  echo "verify-sig: keyless, identity ${COSIGN_IDENTITY}"
  for ref in "${refs[@]}"; do
    cosign verify \
      --certificate-identity "${COSIGN_IDENTITY}" \
      --certificate-oidc-issuer "${COSIGN_OIDC_ISSUER}" \
      "${EXTRA[@]}" "${ref}" >/dev/null
    echo "  ok ${ref##*@}"
  done
fi

if [ -n "${COSIGN_PUB}" ]; then
  # --insecure-ignore-tlog because the key-pair signature deliberately carries
  # no log entry; the warning is about the absence of a log, not the signature.
  echo "verify-sig: key pair, ${COSIGN_PUB}"
  for ref in "${refs[@]}"; do
    cosign verify --key "${COSIGN_PUB}" --insecure-ignore-tlog "${EXTRA[@]}" "${ref}" >/dev/null
    echo "  ok ${ref##*@}"
  done
fi

echo "verify-sig: done"
