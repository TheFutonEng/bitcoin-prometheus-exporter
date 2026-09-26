#!/usr/bin/env bash
# Signs the published index and every platform image inside it.
#
# Two modes, and a release uses both:
#
#   keyless (COSIGN_KEYLESS=1) binds the signature to the workflow that built
#     the image, so a verifier trusts a repo, ref and workflow rather than
#     whoever holds a key. Requires an OIDC token, so it only works in CI.
#
#   key pair (COSIGN_KEY=<path>) is the one that works in an airgap. It is
#     signed with --tlog-upload=false on purpose: an entry in the public
#     transparency log is worthless to a verifier who cannot reach Rekor, and
#     writing one would oblige them to pass --insecure-ignore-tlog anyway. The
#     documented offline command expects no log entry.
#
# cosign v3 note: --tlog-upload=false conflicts with the default signing-config
# and errors out unless --use-signing-config=false is passed too. Verified
# against cosign v3.1.3.
#
# --recursive signs the index AND every image inside it in one call. The index
# is what a tag resolves to and what most people verify; the per-platform
# digests are what someone pinning a single architecture checks.
#
# env:
#   COSIGN_KEYLESS=1  enable keyless signing (needs an OIDC token, so CI only)
#   COSIGN_KEY=       path to a private key — enables the offline mode
#   COSIGN_PASSWORD=  that key's password. Leave UNSET to be prompted; set it,
#                     even to empty, to pass it through non-interactively.
#   COSIGN_EXTRA=     extra cosign flags, e.g. --allow-insecure-registry
set -euo pipefail

cd "$(dirname "$0")/.."
IMAGE_REF="${1:?usage: sign-image.sh <image-ref>}"

: "${COSIGN_KEYLESS:=}"
: "${COSIGN_KEY:=}"

if [ -z "${COSIGN_KEYLESS}" ] && [ -z "${COSIGN_KEY}" ]; then
  echo "sign: neither COSIGN_KEYLESS nor COSIGN_KEY is set — nothing to do" >&2
  exit 1
fi

index_ref="$(make -s digest-ref)"
echo "sign: ${index_ref} (and every image in it)"

read -r -a EXTRA <<<"${COSIGN_EXTRA:-}"

# Passed as an env prefix rather than inline so a password containing shell
# metacharacters is not word-split. Unset stays unset, so cosign prompts.
PWENV=()
[ -n "${COSIGN_PASSWORD+x}" ] && PWENV=(env "COSIGN_PASSWORD=${COSIGN_PASSWORD}")

if [ -n "${COSIGN_KEYLESS}" ]; then
  echo "sign: keyless"
  cosign sign --recursive --yes "${EXTRA[@]}" "${index_ref}"
fi

if [ -n "${COSIGN_KEY}" ]; then
  echo "sign: key pair (no transparency-log entry, for offline verification)"
  "${PWENV[@]}" cosign sign --recursive --yes \
    --key "${COSIGN_KEY}" --use-signing-config=false --tlog-upload=false \
    "${EXTRA[@]}" "${index_ref}"
fi

echo "sign: done"
