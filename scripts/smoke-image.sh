#!/usr/bin/env bash
# Boots an exporter image and proves it actually runs: reports its version, and
# serves /metrics with the exporter's own self-metrics present.
#
# It is pointed at no node, so bitcoin_up is 0 and every collector fails. That
# is the point — the check is that the binary loads and serves on this
# platform, which is what catches a broken cross-compile. Node-facing behaviour
# is covered by the integration suite.
set -euo pipefail

IMAGE_REF="${1:?usage: smoke-image.sh <image-ref>}"
NAME="smoke-$$-$RANDOM"
PORT="${SMOKE_PORT:-19999}"

cleanup() { docker rm -f "${NAME}" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "smoke: ${IMAGE_REF}"

version="$(docker run --rm "${IMAGE_REF}" -version)"
echo "  version: ${version}"
case "${version}" in
  bitcoin-exporter\ *) ;;
  *) echo "smoke: unexpected -version output" >&2; exit 1 ;;
esac

docker run -d --name "${NAME}" -p "127.0.0.1:${PORT}:9332" "${IMAGE_REF}" \
  -rpc.url=http://127.0.0.1:1 -rpc.user=smoke -rpc.password=smoke \
  -scrape.timeout=2s -rpc.timeout=1s >/dev/null

for _ in $(seq 1 30); do
  if curl -fsS --max-time 3 "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

metrics="$(curl -fsS --max-time 5 "http://127.0.0.1:${PORT}/metrics")" || {
  echo "smoke: /metrics did not respond" >&2
  docker logs "${NAME}" 2>&1 | tail -20 >&2
  exit 1
}

# The node is unreachable by construction, so assert on what must be there
# regardless: the liveness gauge and the exporter's own build info.
for want in '^bitcoin_up ' '^bitcoin_exporter_build_info{'; do
  if ! grep -qE "${want}" <<<"${metrics}"; then
    echo "smoke: /metrics is missing ${want}" >&2
    exit 1
  fi
done

echo "  /metrics: $(grep -c '^bitcoin' <<<"${metrics}") exporter metric lines"
echo "  build_info: $(grep -o 'bitcoin_exporter_build_info{[^}]*}' <<<"${metrics}")"
echo "smoke: ok"
