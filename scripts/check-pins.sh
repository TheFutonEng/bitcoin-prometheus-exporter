#!/usr/bin/env bash
# Asserts the Dockerfile and the Makefile pin the same base images.
#
# They are declared in both places on purpose: the Dockerfile has to work for
# someone running `docker build` directly, and the Makefile has to know the
# pins to report them. Two sources of truth silently drift, so this fails the
# build rather than letting a release claim reproducibility against a base the
# Makefile no longer describes.
set -euo pipefail

cd "$(dirname "$0")/.."

fail=0
check() {
  local name="$1" make_value="$2"
  local docker_value
  docker_value="$(sed -n "s/^ARG ${name}=//p" Dockerfile | head -1)"
  if [ -z "${docker_value}" ]; then
    echo "check-pins: Dockerfile has no ARG ${name}" >&2
    fail=1
  elif [ "${docker_value}" != "${make_value}" ]; then
    echo "check-pins: ${name} disagrees" >&2
    echo "  Dockerfile: ${docker_value}" >&2
    echo "  Makefile:   ${make_value}" >&2
    fail=1
  else
    echo "check-pins: ${name} ok"
  fi
}

check GO_BASE      "$(make -s print-go-base)"
check RUNTIME_BASE "$(make -s print-runtime-base)"

# Both bases must be pinned by digest, not merely by tag.
for pin in "$(make -s print-go-base)" "$(make -s print-runtime-base)"; do
  case "${pin}" in
    *@sha256:*) ;;
    *) echo "check-pins: '${pin}' is not pinned by digest" >&2; fail=1 ;;
  esac
done

exit "${fail}"
