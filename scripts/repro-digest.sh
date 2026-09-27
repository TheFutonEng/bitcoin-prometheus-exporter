#!/usr/bin/env bash
# Rebuilds one platform from this tree and prints the resulting image manifest
# digest, without pushing anything.
#
# Attestations are deliberately off. They carry per-build timestamps and random
# ids, so an index containing them is not reproducible by design — but the
# image manifest inside it is, and is byte-identical whether or not
# attestations were requested alongside it. Verified 2026-09-26: a
# --provenance=mode=max --sbom=true multi-platform build produced exactly the
# digests this target prints.
set -euo pipefail

cd "$(dirname "$0")/.."
PLATFORM="${1:?usage: repro-digest.sh <platform>}"

out="$(mktemp -d)"
trap 'rm -rf "${out}"' EXIT

docker buildx --builder "$(make -s print-builder)" build \
  --platform "${PLATFORM}" \
  --build-arg VERSION="$(make -s print-version)" \
  --build-arg REVISION="$(make -s print-revision)" \
  --build-arg SOURCE_DATE_EPOCH="$(make -s print-source-date-epoch)" \
  --provenance=false --sbom=false \
  --output "type=oci,dest=${out}/image.tar,rewrite-timestamp=true" \
  . >/dev/null

python3 -c '
import json, sys, tarfile
with tarfile.open(sys.argv[1]) as t:
    index = json.load(t.extractfile("index.json"))
print(index["manifests"][0]["digest"])
' "${out}/image.tar"
