#!/usr/bin/env bash
# Generates regtest activity against the compose stack so the dashboard has
# something to plot: mines blocks and broadcasts a few transactions.
#
#   scripts/regtest-activity.sh            # one round
#   scripts/regtest-activity.sh --loop 30  # a round every 30 seconds
set -euo pipefail

CONTAINER=${CONTAINER:-bitcoind}
WALLET=${WALLET:-miner}

# bitcoin-cli reads /data/bitcoin.conf, the same file the node does, so it
# resolves the network and RPC port without being told.
cli() {
  docker exec "$CONTAINER" /usr/local/bin/bitcoin-cli -datadir=/data "$@"
}

ensure_wallet() {
  if ! cli listwallets | grep -q "\"$WALLET\""; then
    cli createwallet "$WALLET" >/dev/null 2>&1 || cli loadwallet "$WALLET" >/dev/null
  fi
}

round() {
  local addr
  addr=$(cli -rpcwallet="$WALLET" getnewaddress)

  # 101 blocks the first time, so the first coinbase matures and becomes
  # spendable; a single block on every round after that.
  local height
  height=$(cli getblockcount)
  if [ "$height" -lt 101 ]; then
    cli generatetoaddress $((101 - height)) "$addr" >/dev/null
  fi

  local i
  for i in 1 2 3; do
    cli -rpcwallet="$WALLET" -named sendtoaddress \
      address="$(cli -rpcwallet="$WALLET" getnewaddress)" \
      amount=0.1 fee_rate=$((RANDOM % 40 + 2)) >/dev/null
  done
  cli generatetoaddress 1 "$addr" >/dev/null

  echo "height=$(cli getblockcount) mempool=$(cli getmempoolinfo | grep -o '"size": *[0-9]*' | tr -dc 0-9)"
}

if [ "${1:-}" = "--loop" ]; then
  interval=${2:-30}
  ensure_wallet
  echo "generating activity every ${interval}s; ctrl-c to stop"
  while true; do
    round
    sleep "$interval"
  done
else
  ensure_wallet
  round
fi
