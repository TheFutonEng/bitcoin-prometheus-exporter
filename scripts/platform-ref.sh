#!/usr/bin/env bash
# Prints image@<digest> for one platform's IMAGE MANIFEST inside a published
# multi-arch index.
#
# Attestation manifests also appear in the index, carrying platform
# unknown/unknown and a vnd.docker.reference.type annotation. Those are skipped:
# what is wanted here is the runnable image for the platform.
set -euo pipefail

IMAGE_REF="${1:?usage: platform-ref.sh <image-ref> <platform>}"
PLATFORM="${2:?usage: platform-ref.sh <image-ref> <platform>}"

raw="$(docker buildx imagetools inspect "${IMAGE_REF}" --raw)"

digest="$(PLATFORM="${PLATFORM}" python3 -c '
import json, os, sys
want = os.environ["PLATFORM"]
index = json.load(sys.stdin)
for m in index.get("manifests", []):
    ann = m.get("annotations") or {}
    if ann.get("vnd.docker.reference.type"):
        continue                      # an attestation, not an image
    p = m.get("platform") or {}
    if "{}/{}".format(p.get("os"), p.get("architecture")) == want:
        print(m["digest"])
        break
' <<<"${raw}")"

if [ -z "${digest}" ]; then
  echo "platform-ref: ${IMAGE_REF} has no image manifest for ${PLATFORM}" >&2
  exit 1
fi

# Strip the tag, not the registry port. A naive ${ref%%:*} turns
# 127.0.0.1:5000/name:1.0 into "127.0.0.1"; the tag is only the part after the
# last colon, and only when that colon follows the last slash.
repo="${IMAGE_REF}"
case "${IMAGE_REF##*/}" in
  *:*) repo="${IMAGE_REF%:*}" ;;
esac

printf '%s@%s\n' "${repo}" "${digest}"
