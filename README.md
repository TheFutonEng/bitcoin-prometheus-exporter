# bitcoin-prometheus-exporter

A Prometheus exporter for Bitcoin Core. It reads a node over JSON-RPC and
publishes chain, network, mempool, fee, mining, peer and wallet metrics — 115 of
them across seven collectors.

It talks to any Bitcoin Core node reachable over RPC, local or remote, pruned or
archival, with or without a wallet. Missing or version-specific RPC fields are
omitted rather than reported as zero, so a metric that is present is a metric the
node actually answered. Development and CI verify it against
`ghcr.io/thefutoneng/bitcoin:31.1-1`.

```
┌──────────┐   JSON-RPC    ┌──────────┐   /metrics   ┌────────────┐
│ bitcoind │◄──────────────│ exporter │◄─────────────│ Prometheus │
└──────────┘  cookie/auth  └──────────┘    :9332     └────────────┘
```

## Quick start

### The whole stack, with a node to test against

```bash
git clone https://github.com/TheFutonEng/bitcoin-prometheus-exporter
cd bitcoin-prometheus-exporter
make up                       # bitcoind (regtest) + exporter + Prometheus + Grafana
make activity                 # mine a block and broadcast a few transactions
```

| Service | URL | Notes |
| --- | --- | --- |
| Exporter | <http://127.0.0.1:9332/metrics> | |
| Prometheus | <http://127.0.0.1:9090> | alert rules preloaded |
| Grafana | <http://127.0.0.1:3000> | `admin` / `admin`, dashboard provisioned |
| Node RPC | `127.0.0.1:8332` | regtest |

`make activity` is worth running a few times — a fresh regtest chain has no
blocks, no peers and no fee history, so most panels start empty. For a chain
with real peers and real fee estimates, point `.env` at the signet config:

```ini
BITCOIN_CONF=./deploy/bitcoin/signet.conf
BITCOIN_COOKIE=/data/signet/.cookie
```

and `make down && make up`. See [Node configuration](#node-configuration) to
run it against a config file of your own.

Tear it all down with `make down`.

#### Reaching it from another machine

Every port is published on `127.0.0.1`, so the stack is not reachable off the
host. To change that, copy the example override:

```bash
cp docker-compose.override.yml.example docker-compose.override.yml
$EDITOR docker-compose.override.yml      # set the addresses
docker compose up -d
```

Compose loads `docker-compose.override.yml` automatically, and it is gitignored
because the right address is machine specific. Note the `!override` tag in the
example: it *replaces* the port list, whereas a plain override appends and
leaves the loopback binding published as well.

Then point the make targets at the new address:

```bash
make metrics     EXPORTER_URL=http://203.0.113.10:19332
make check-stack PROMETHEUS_URL=http://203.0.113.10:19090
```

Before you do this on a network you share: Grafana ships `admin`/`admin` with
anonymous viewing on, Prometheus and the exporter have no authentication, and
on a real node the exporter publishes peer addresses and — with the wallet
collector enabled — balances. bitcoind's RPC is deliberately left off the
override. A host firewall will not help, either: docker manages its own
iptables rules and published ports bypass `ufw`.

An SSH tunnel avoids all of that if you only need it occasionally:

```bash
ssh -N -L 13000:127.0.0.1:3000 you@the-host       # then http://localhost:13000
```

### Against a node you already run

```bash
make build
./dist/bitcoin-exporter -rpc.url=http://127.0.0.1:8332
```

With no credentials configured, the exporter looks for a `.cookie` file under
`~/.bitcoin`, `/data`, `/bitcoin` and `/root/.bitcoin` for the chain named by
`-rpc.chain`. Point it somewhere else with `-rpc.cookie-file`, or use an
`rpcauth` user:

```bash
BITCOIN_RPC_PASSWORD=… ./dist/bitcoin-exporter \
  -rpc.url=http://node.internal:8332 -rpc.user=prometheus
```

Then scrape it:

```yaml
scrape_configs:
  - job_name: bitcoin
    static_configs:
      - targets: ["127.0.0.1:9332"]
```

### As a container

```bash
docker run --rm -p 9332:9332 \
  -v bitcoin-data:/data:ro \
  ghcr.io/thefutoneng/bitcoin-prometheus-exporter:0.1.0 \
  -rpc.url=http://bitcoind:8332 -rpc.cookie-file=/data/.cookie
```

The image runs as uid `65532`, the same uid as `ghcr.io/thefutoneng/bitcoin`, so
a shared data volume's cookie file is readable without extra permissions.

Published tags, all `linux/amd64` and `linux/arm64` under one OCI index:

| Tag | Moves |
| --- | --- |
| `0.1.0` | never — an exact release |
| `latest` | to the newest release |

There is deliberately no floating `0.1` tag: a reference that moves between
patch releases is one people end up depending on without meaning to. `latest`
exists only because some tooling expects it, and is not what you should deploy.
Prereleases publish their exact version and nothing else, so they never move
`latest`.

Only tagged releases are published; there is no rolling `main` image. For
anything you care about, pin the digest rather than a tag — `make digest-ref`
prints it, and so does every release's job summary.

## Verifying what you pulled

The index and every image in it carry a cosign signature, and the index carries
SLSA provenance and an SBOM produced during the build. Verifying is the point of
publishing them.

**Keyless** binds the signature to the workflow that built the image, so you are
trusting a repository, ref and workflow rather than whoever holds a key:

```bash
cosign verify \
  --certificate-identity-regexp \
    '^https://github\.com/TheFutonEng/bitcoin-prometheus-exporter/\.github/workflows/release\.yml@refs/tags/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/thefutoneng/bitcoin-prometheus-exporter:0.1.0
```

The identity is not optional. Without it you would accept a signature from
anyone Sigstore will issue a certificate to, which is no check at all. The regexp
is anchored at the start but not the end, so it accepts any tag from this
workflow in this repository — deliberate, so one published command keeps
working, but it means the signature attests to "a release of this repo" rather
than "this exact version". Pin the digest if you care which.

**Offline**, with the published key, for an airgap or anywhere Sigstore is
unreachable:

```bash
cosign verify --key cosign.pub --insecure-ignore-tlog \
  ghcr.io/thefutoneng/bitcoin-prometheus-exporter:0.1.0
```

The key-pair signature deliberately carries no transparency-log entry — an entry
nobody can reach is not worth the dependency — so `--insecure-ignore-tlog` is
expected. That warning is about the absence of a log, not about the signature.

The provenance and SBOM ride in the index and need no network beyond the
registry itself:

```bash
docker buildx imagetools inspect --format '{{ json .Provenance }}' \
  ghcr.io/thefutoneng/bitcoin-prometheus-exporter:0.1.0
docker buildx imagetools inspect --format '{{ json .SBOM }}' \
  ghcr.io/thefutoneng/bitcoin-prometheus-exporter:0.1.0
```

### Reproducible builds

Each platform's image is bit-for-bit what its tag builds, and you can check that
yourself rather than taking our word for it:

```bash
git checkout v0.1.0
make verify-repro-published PLATFORM=linux/amd64
```

That rebuilds the platform from this tree and compares the result against the
image manifest digest in the registry. It is what the release itself runs before
signing anything.

What makes it hold: both base images are pinned by digest, the Go toolchain is
pinned exactly, the build passes `-trimpath -buildvcs=false` with `CGO_ENABLED=0`,
layer timestamps come from `SOURCE_DATE_EPOCH` (the commit time, so it is a
property of the tree rather than of when you built), and the buildkit version is
pinned so the comparison is not between two different builders.

It compares **image manifests**, not the index. The index also contains
attestation manifests, whose contents include build timestamps and random ids by
design, so index digests are not expected to match and never will.

## Node configuration

The stack passes **no command-line flags** to bitcoind. Everything the node
needs comes from a config file mounted at `/data/bitcoin.conf`, which the image
reads because its entrypoint sets `-datadir=/data`. Two are provided:

| File | Network |
| --- | --- |
| `deploy/bitcoin/regtest.conf` | regtest, the default |
| `deploy/bitcoin/signet.conf` | signet |

To run the stack against your own configuration, point `BITCOIN_CONF` at your
file and `BITCOIN_COOKIE` at the matching cookie path:

```ini
BITCOIN_CONF=./my-bitcoin.conf
BITCOIN_COOKIE=/data/signet/.cookie
```

Two constraints on that file:

- **It must select its own network** — `regtest=1`, `signet=1`, `testnet4=1`, or
  nothing at all for mainnet. The stack deliberately passes no `-chain`, because
  bitcoind refuses to start when a command-line `-chain` and a conf-file network
  setting are both present: *"Invalid combination of -regtest, -signet, -testnet,
  -testnet4 and -chain. Can use at most one."*
- **RPC must be reachable from the exporter container** — `rpcbind=0.0.0.0`,
  an `rpcallowip` covering the compose network, and `rpcport=8332`.
  Network-specific settings belong in a `[regtest]` / `[signet]` section; only
  the network selector and `server=1` go at the top level.

`bitcoin-cli` reads the same file, so `docker compose exec bitcoind bitcoin-cli
-datadir=/data <command>` needs no network or port flags either — which is why
the healthcheck and `scripts/regtest-activity.sh` pass none.

Nothing is published off the host: `docker-compose.yml` binds every port to
`127.0.0.1`.

## Authentication

Two mechanisms, in priority order:

1. **`rpcuser` / `rpcpassword`** — `-rpc.user` and `-rpc.password`, or the
   `BITCOIN_RPC_USER` and `BITCOIN_RPC_PASSWORD` environment variables. Prefer
   the environment for the password so it stays out of the process table.
2. **Cookie file** — `-rpc.cookie-file`, or auto-detection from `-rpc.chain`.
   bitcoind rewrites its cookie on every restart, so the exporter re-reads the
   file whenever its size or modification time changes. No restart needed.

## Collectors

`chain`, `network`, `mempool`, `fees`, `mining` and `peers` run by default;
`wallet` is opt-in, because a node started with `-disablewallet` has no wallet
RPCs and most nodes do not need the metrics.

```bash
bitcoin-exporter -collector.enable=wallet          # add to the defaults
bitcoin-exporter -collector.disable=fees,mining    # drop from the defaults
bitcoin-exporter -collectors=chain,mempool         # replace the defaults
bitcoin-exporter -collectors=all                   # everything
```

`-collector.disable` is applied last, so it wins over `-collector.enable`.

### Cardinality

Two knobs decide how many series the exporter produces:

- **`-collector.peers.detail`** (off) publishes one set of series per connected
  peer, labelled with the peer's address. On a mainnet node with 125 peers that
  is roughly 1,500 extra series, and peer addresses churn. The aggregate
  breakdowns — by network, user agent and connection type — are always on and
  are usually what you want to alert on.
- **`-collector.fees.targets`** (`1,2,3,6,36,144,1008`) is one series per
  confirmation target per unit.

Everything else is bounded by the node's own configuration.

## Deployment considerations

This repository ships no deployment manifests; how you run the exporter is
yours to decide. What follows is the information that decision needs.

The common arrangement is a **sidecar beside the node** — same Kubernetes pod,
or same compose project. That shape is worth understanding because of what it
does and does not seal:

- **The node's RPC never leaves the pod.** Containers in a pod share a network
  namespace, so the exporter reaches bitcoind on `127.0.0.1` and the RPC port
  need not be exposed at all. The cookie comes off a shared volume; both this
  image and `ghcr.io/thefutoneng/bitcoin` run as uid `65532`, so no permission
  juggling is required.
- **The scrape endpoint must leave the pod.** Prometheus runs elsewhere, so
  `:9332` has to listen on the pod address rather than loopback. On a flat
  cluster network that means anything able to route to the pod can read it.

What a default scrape exposes is chain state, mempool, fees, hash rate, node
version, and peer counts grouped by network, user agent and connection type.
The chain is public and the aggregates name nobody.

Two opt-in collectors change that materially:

| Flag | Publishes | Consider |
| --- | --- | --- |
| `-collector.peers.detail` | per-peer addresses | your node's peer topology, which is what someone attempting an eclipse attack or deanonymisation wants |
| `-collector.enable=wallet` | wallet balances | self-evident on a real node |

Both are off by default, which is what keeps a default scrape low-risk.

The exporter itself does no TLS and no authentication, deliberately. Where the
scrape path needs protecting, the controls that fit are the ones already in
your environment — a NetworkPolicy restricting `:9332` to the Prometheus pod, a
service mesh's mTLS, or a reverse proxy — all of which sit below the
application and need no credential distribution.

## Flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `-web.listen-address` | `:9332` | Address to serve metrics on. |
| `-web.telemetry-path` | `/metrics` | Path to expose metrics under. |
| `-rpc.url` | `http://127.0.0.1:8332` | Node JSON-RPC endpoint. |
| `-rpc.user` / `-rpc.password` | — | `rpcauth` credentials. |
| `-rpc.cookie-file` | auto-detected | Path to the node's `.cookie`. |
| `-rpc.chain` | `main` | Chain name, used only to find the cookie. |
| `-rpc.timeout` | `5s` | Timeout for a single RPC call. |
| `-scrape.timeout` | `10s` | Deadline for one whole scrape. |
| `-collectors` | defaults | Replace the active collector set. |
| `-collector.enable` / `-collector.disable` | — | Adjust the active set. |
| `-collector.peers.detail` | `false` | One series per connected peer. |
| `-collector.mining.blockstats` | `true` | Query `getblockstats` at the tip. |
| `-collector.fees.targets` | `1,2,3,6,36,144,1008` | `estimatesmartfee` targets. |
| `-collector.fees.mode` | `conservative` | `conservative` or `economical`. |
| `-collector.wallet.names` | all loaded | Restrict the wallet collector. |
| `-log.level` / `-log.format` | `info` / `logfmt` | Logging. |
| `-version` | | Print the version and exit. |

Every flag with an environment equivalent reads it as a fallback:
`BITCOIN_EXPORTER_LISTEN`, `BITCOIN_RPC_URL`, `BITCOIN_RPC_USER`,
`BITCOIN_RPC_PASSWORD`, `BITCOIN_RPC_COOKIE_FILE`, `BITCOIN_RPC_CHAIN`.

## Behaviour under failure

- **`bitcoin_up`** is a liveness probe independent of the collectors: 1 when the
  node answered, 0 when it did not. A node that answers but rejects a call is
  still up.
- **Collectors are independent.** They run concurrently, and one failing does not
  suppress the others. `bitcoin_exporter_collector_success{collector=…}` reports
  which succeeded.
- **Expected refusals are not failures.** A pruned node refusing
  `getblockstats`, a `-disablewallet` node with no wallet RPCs, and
  `estimatesmartfee` without enough history are all reported as data — a zero
  `bitcoin_latest_block_stats_available`, `bitcoin_wallets_loaded` or
  `bitcoin_fee_estimate_available` — rather than as collector errors.
- **Absent fields stay absent.** `getblockchaininfo` gained `time` in a recent
  release and lost `startingheight` from `getpeerinfo` in another; the exporter
  publishes optional fields only when the node sends them.

## Metrics

Grouped by the collector that produces them. Fee rates appear in both of the
units people use: `_btc_per_kvb` is what bitcoind reports, `_sat_per_vbyte` is
what people read. Values taken from `getblockstats` are in satoshis and sat/vB,
matching bitcoind.

#### `chain` (17 metrics)

| Metric | Labels | Description |
| --- | --- | --- |
| `bitcoin_best_block_time_seconds` | — | Unix timestamp of the current best block. |
| `bitcoin_blocks` | — | Height of the most-work fully validated chain. |
| `bitcoin_blocks_behind` | — | Known headers minus validated blocks; 0 when the node is at the tip. |
| `bitcoin_chain_info` | `chain` | Always 1, labelled with the chain the node is running on. |
| `bitcoin_chainwork` | — | Total cumulative proof of work on the best chain, as a float approximation. |
| `bitcoin_difficulty` | — | Proof-of-work difficulty of the current best block. |
| `bitcoin_headers` | — | Height of the best known block header. |
| `bitcoin_initial_block_download` | — | 1 while the node considers itself in initial block download. |
| `bitcoin_median_time_seconds` | — | Median unix timestamp of the last 11 blocks. |
| `bitcoin_prune_automatic` | — | 1 if pruning is managed automatically from a target size. |
| `bitcoin_prune_height` | — | Lowest block height for which full block data is still stored. |
| `bitcoin_prune_target_size_bytes` | — | Target size the node prunes the block store down to. |
| `bitcoin_pruned` | — | 1 if the node is running with block pruning enabled. |
| `bitcoin_size_on_disk_bytes` | — | Estimated size of the block and undo files on disk. |
| `bitcoin_verification_progress` | — | Estimated share of chain work verified so far, 0 to 1. |
| `bitcoin_warning_info` | `warning` | Always 1, labelled with a warning the node is currently reporting. |
| `bitcoin_warnings` | — | Number of warnings the node is currently reporting. |

#### `network` (20 metrics)

| Metric | Labels | Description |
| --- | --- | --- |
| `bitcoin_connections` | — | Total peer connections the node currently holds. |
| `bitcoin_incremental_fee_btc_per_kvb` | — | Minimum fee rate increment for replacement and mempool limiting, in BTC/kvB. |
| `bitcoin_incremental_fee_sat_per_vbyte` | — | Minimum fee rate increment for replacement and mempool limiting, in sat/vB. |
| `bitcoin_local_relay` | — | 1 if the node relays transactions from its own mempool. |
| `bitcoin_net_bytes_total` | `direction` | Cumulative p2p traffic since the node started. |
| `bitcoin_network_active` | — | 1 if p2p networking is enabled. |
| `bitcoin_network_limited` | `network` | 1 if the node will not make outbound connections on this network. |
| `bitcoin_network_proxy_info` | `network`, `proxy` | Always 1, labelled with the proxy configured for a network. |
| `bitcoin_network_reachable` | `network` | 1 if the node considers this network reachable. |
| `bitcoin_peers` | `direction` | Peer connections by direction. |
| `bitcoin_relay_fee_btc_per_kvb` | — | Minimum fee rate for a transaction to be relayed, in BTC/kvB. |
| `bitcoin_relay_fee_sat_per_vbyte` | — | Minimum fee rate for a transaction to be relayed, in sat/vB. |
| `bitcoin_time_offset_seconds` | — | Node clock offset from the median of its peers. |
| `bitcoin_upload_target_bytes` | — | Configured -maxuploadtarget for one cycle; 0 when unset. |
| `bitcoin_upload_target_bytes_left` | — | Bytes left in the current upload target cycle. |
| `bitcoin_upload_target_reached` | — | 1 if the upload target for the current cycle has been hit. |
| `bitcoin_upload_target_seconds_left` | — | Seconds left in the current upload target cycle. |
| `bitcoin_upload_target_serve_historical_blocks` | — | 1 if the node still serves historical blocks under the upload target. |
| `bitcoin_uptime_seconds` | — | Seconds the node process has been running. |
| `bitcoin_version_info` | `version`, `subversion`, `protocol_version` | Always 1, labelled with the node's version strings. |

#### `mempool` (13 metrics)

| Metric | Labels | Description |
| --- | --- | --- |
| `bitcoin_mempool_bytes` | — | Sum of the virtual sizes of all mempool transactions. |
| `bitcoin_mempool_fill_ratio` | — | Mempool memory usage divided by -maxmempool, 0 to 1. |
| `bitcoin_mempool_full_rbf` | — | 1 if the node accepts replacement of any transaction regardless of opt-in. |
| `bitcoin_mempool_loaded` | — | 1 once the mempool has finished loading from disk. |
| `bitcoin_mempool_max_bytes` | — | Configured -maxmempool memory ceiling. |
| `bitcoin_mempool_min_fee_btc_per_kvb` | — | Lowest fee rate the mempool currently accepts, in BTC/kvB. |
| `bitcoin_mempool_min_fee_sat_per_vbyte` | — | Lowest fee rate the mempool currently accepts, in sat/vB. |
| `bitcoin_mempool_min_relay_fee_btc_per_kvb` | — | Configured -minrelaytxfee floor, in BTC/kvB. |
| `bitcoin_mempool_min_relay_fee_sat_per_vbyte` | — | Configured -minrelaytxfee floor, in sat/vB. |
| `bitcoin_mempool_total_fee_btc` | — | Sum of the fees of all mempool transactions, in BTC. |
| `bitcoin_mempool_txs` | — | Number of transactions currently in the mempool. |
| `bitcoin_mempool_unbroadcast_txs` | — | Transactions the node has not yet seen acknowledged by a peer. |
| `bitcoin_mempool_usage_bytes` | — | Memory the mempool occupies in the node process. |

#### `fees` (4 metrics)

| Metric | Labels | Description |
| --- | --- | --- |
| `bitcoin_fee_estimate_answered_blocks` | `blocks`, `mode` | Confirmation target the estimate actually corresponds to, which may exceed the requested one. |
| `bitcoin_fee_estimate_available` | `blocks`, `mode` | 1 if the node had enough data to answer estimatesmartfee for this target. |
| `bitcoin_fee_estimate_btc_per_kvb` | `blocks`, `mode` | Estimated fee rate to confirm within N blocks, in BTC/kvB. |
| `bitcoin_fee_estimate_sat_per_vbyte` | `blocks`, `mode` | Estimated fee rate to confirm within N blocks, in sat/vB. |

#### `mining` (20 metrics)

| Metric | Labels | Description |
| --- | --- | --- |
| `bitcoin_latest_block_feerate_sat_per_vbyte` | `stat` | Fee rate distribution within the block at the tip, in sat/vB. |
| `bitcoin_latest_block_fees_sat` | — | Total fees paid in the block at the tip, in satoshis. |
| `bitcoin_latest_block_height` | — | Height of the block the tip statistics describe. |
| `bitcoin_latest_block_inputs` | — | Number of inputs spent in the block at the tip. |
| `bitcoin_latest_block_outputs` | — | Number of outputs created in the block at the tip. |
| `bitcoin_latest_block_segwit_txs` | — | SegWit transactions in the block at the tip. |
| `bitcoin_latest_block_segwit_weight` | — | Weight contributed by SegWit transactions in the block at the tip. |
| `bitcoin_latest_block_size_bytes` | — | Serialized size of the block at the tip. |
| `bitcoin_latest_block_stats_available` | — | 1 if getblockstats succeeded for the chain tip during this scrape. |
| `bitcoin_latest_block_subsidy_sat` | — | Block subsidy of the block at the tip, in satoshis. |
| `bitcoin_latest_block_time_seconds` | — | Unix timestamp of the block at the tip. |
| `bitcoin_latest_block_tx_size_bytes` | `stat` | Transaction size distribution within the block at the tip. |
| `bitcoin_latest_block_txs` | — | Transaction count of the block at the tip. |
| `bitcoin_latest_block_utxo_increase` | — | Net change in the UTXO set caused by the block at the tip. |
| `bitcoin_latest_block_value_sat` | — | Total value of the outputs in the block at the tip, in satoshis. |
| `bitcoin_latest_block_weight` | — | Weight of the block at the tip. |
| `bitcoin_mining_current_block_txs` | — | Transaction count of the last block template the node assembled. |
| `bitcoin_mining_current_block_weight` | — | Weight of the last block template the node assembled. |
| `bitcoin_mining_pooled_txs` | — | Transactions the node would consider for the next block template. |
| `bitcoin_network_hashps` | — | Estimated network hash rate in hashes per second. |

#### `peers` (26 metrics)

| Metric | Labels | Description |
| --- | --- | --- |
| `bitcoin_peer_addr_processed_total` | `id`, `addr` | Addresses accepted from this peer's ADDR messages. |
| `bitcoin_peer_addr_rate_limited_total` | `id`, `addr` | Addresses dropped from this peer's ADDR messages by rate limiting. |
| `bitcoin_peer_bytes_received_total` | `id`, `addr` | Bytes received from this peer since the connection opened. |
| `bitcoin_peer_bytes_sent_total` | `id`, `addr` | Bytes sent to this peer since the connection opened. |
| `bitcoin_peer_connection_time_seconds` | `id`, `addr` | Unix timestamp at which the connection to this peer opened. |
| `bitcoin_peer_info` | `id`, `addr`, `network`, `direction`, `connection_type`, `subversion`, `version` | Always 1, labelled with the identifying attributes of one peer. |
| `bitcoin_peer_last_block_seconds` | `id`, `addr` | Unix timestamp of the last block received from this peer; 0 if none. |
| `bitcoin_peer_last_receive_seconds` | `id`, `addr` | Unix timestamp of the last message received from this peer. |
| `bitcoin_peer_last_send_seconds` | `id`, `addr` | Unix timestamp of the last message sent to this peer. |
| `bitcoin_peer_last_transaction_seconds` | `id`, `addr` | Unix timestamp of the last transaction received from this peer; 0 if none. |
| `bitcoin_peer_min_fee_filter_btc_per_kvb` | `id`, `addr` | Minimum fee rate this peer wants transactions relayed at, in BTC/kvB. |
| `bitcoin_peer_min_ping_seconds` | `id`, `addr` | Best round-trip time seen for this peer. |
| `bitcoin_peer_ping_seconds` | `id`, `addr` | Last measured round-trip time to this peer. |
| `bitcoin_peer_ping_wait_seconds` | `id`, `addr` | Time spent waiting on an outstanding ping to this peer. |
| `bitcoin_peer_starting_height` | `id`, `addr` | Chain height this peer advertised when the connection opened. |
| `bitcoin_peer_synced_blocks` | `id`, `addr` | Highest block this peer has in common with the node. |
| `bitcoin_peer_synced_headers` | `id`, `addr` | Highest header this peer has in common with the node. |
| `bitcoin_peer_time_offset_seconds` | `id`, `addr` | Clock offset between the node and this peer. |
| `bitcoin_peers_banned` | — | Number of entries in the node's ban list. |
| `bitcoin_peers_by_connection_type` | `connection_type` | Connected peers grouped by how the connection was established. |
| `bitcoin_peers_by_network` | `network` | Connected peers grouped by the network they are reachable on. |
| `bitcoin_peers_by_subversion` | `subversion` | Connected peers grouped by their advertised user agent. |
| `bitcoin_peers_by_transport` | `transport` | Connected peers grouped by p2p transport protocol. |
| `bitcoin_peers_high_bandwidth` | `direction` | Peers in a BIP152 high-bandwidth compact block relationship, by direction. |
| `bitcoin_peers_ping_seconds` | `stat` | Aggregate round-trip time across connected peers. |
| `bitcoin_peers_relaying_txs` | — | Connected peers the node relays transactions to. |

#### `wallet` (15 metrics)

| Metric | Labels | Description |
| --- | --- | --- |
| `bitcoin_wallet_balance_btc` | `wallet`, `owner`, `category` | Wallet balance in BTC, split by ownership and spendability. |
| `bitcoin_wallet_birthtime_seconds` | `wallet` | Unix timestamp of the earliest transaction the wallet needs to scan from. |
| `bitcoin_wallet_descriptors` | `wallet` | 1 if the wallet uses descriptors rather than legacy keys. |
| `bitcoin_wallet_info` | `wallet`, `version`, `format` | Always 1, labelled with the descriptive attributes of a wallet. |
| `bitcoin_wallet_keypool_oldest_seconds` | `wallet` | Unix timestamp of the oldest key in the keypool. |
| `bitcoin_wallet_keypool_size` | `wallet`, `chain` | Pre-generated keys remaining in the wallet keypool. |
| `bitcoin_wallet_last_processed_block_height` | `wallet` | Height of the last block the wallet processed. |
| `bitcoin_wallet_pay_tx_fee_btc_per_kvb` | `wallet` | Configured -paytxfee for this wallet, in BTC/kvB. |
| `bitcoin_wallet_private_keys_enabled` | `wallet` | 1 if the wallet holds private keys. |
| `bitcoin_wallet_scanning` | `wallet` | 1 while the wallet is rescanning the chain. |
| `bitcoin_wallet_scanning_duration_seconds` | `wallet` | How long the in-progress rescan has been running. |
| `bitcoin_wallet_scanning_progress` | `wallet` | Progress of the in-progress rescan, 0 to 1. |
| `bitcoin_wallet_txs` | `wallet` | Number of transactions in the wallet. |
| `bitcoin_wallet_unlocked_until_seconds` | `wallet` | Unix timestamp until which an encrypted wallet stays unlocked; 0 means locked. |
| `bitcoin_wallets_loaded` | — | Number of wallets currently loaded by the node. |


#### Exporter self-metrics

| Metric | Labels | Description |
| --- | --- | --- |
| `bitcoin_up` | — | 1 if the node answered RPC during this scrape, 0 otherwise. |
| `bitcoin_exporter_build_info` | `version`, `revision`, `build_date`, `goversion` | Always 1, labelled with the build identity of the running exporter. |
| `bitcoin_exporter_collector_duration_seconds` | `collector` | Time each collector spent gathering metrics during the last scrape. |
| `bitcoin_exporter_collector_success` | `collector` | 1 if the collector completed without error during the last scrape. |
| `bitcoin_exporter_rpc_duration_seconds` | `method` | Histogram of JSON-RPC call latency. |
| `bitcoin_exporter_rpc_requests_total` | `method` | Total JSON-RPC calls made to the node. |
| `bitcoin_exporter_rpc_errors_total` | `method` | Total JSON-RPC calls that returned an error. |

The standard Go runtime and process collectors are registered too.

## Alerts

`deploy/prometheus/alerts.yml` ships ten rules and is loaded by the stack:
node down, exporter down, chain falling behind, no new blocks, low peer count,
no outbound peers (an eclipse-attack precondition), mempool near its ceiling,
disk growth, node warnings and clock skew.

## Dashboard

`deploy/grafana/dashboards/bitcoin-node.json` is provisioned into the stack and
imports cleanly into any Grafana — it picks its data source through a template
variable and filters on `job` and `instance`, so it works with more than one
node.

Series colours are assigned by entity rather than by rank, so a filter never
repaints the survivors, and no panel uses two y-axes. Where series are ordered
magnitudes — fee targets, in-block fee-rate percentiles, ping min/avg/max — they
use a single-hue ordinal ramp instead of categorical colours.

## Development

```bash
make test               # unit tests, race detector on
make lint               # gofmt + go vet, including the integration build tag
make test-integration   # spins up real node containers and scrapes them
make check-stack        # the alert-rule and dashboard checks CI runs, against a live stack
make image              # build the container image
```

`make metrics` and `make check-stack` assume the stack is on loopback. Point
them elsewhere if you have republished it:

```bash
make metrics     EXPORTER_URL=http://192.168.1.6:19332
make check-stack PROMETHEUS_URL=http://192.168.1.6:19090
```

The integration tests start `ghcr.io/thefutoneng/bitcoin:31.1-1` in regtest,
mine blocks, spend, peer two nodes together, and assert on the gathered metrics
through a `prometheus.NewPedanticRegistry` — the same strictness the binary
applies at runtime. Point them at another release to check compatibility:

```bash
NODE_IMAGE=ghcr.io/thefutoneng/bitcoin:31.1 make test-integration
```

The harness overrides the image's entrypoint and passes every bitcoind flag
itself, so it does not care how a given image splits entrypoint from cmd — 31.1
and 31.1-1 differ there. CI runs the suite against both. The image's own
defaults, including the mounted `bitcoin.conf`, are covered by the compose
stack instead.

They skip themselves if Docker is not available.

### Releasing

Releases are cut by pushing a tag; nothing else publishes. `ci.yml` runs on pull
requests, including from forks, and has no write access or secrets —
`release.yml` is the only workflow with either.

```bash
git tag -s v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

The workflow then: refuses to run from anything but a tag, checks the tag is
semver and that the base pins agree, builds and boots the image on **native
hardware for each platform**, pushes one multi-arch index with provenance and an
SBOM, pulls each platform back **by digest** and boots it, proves each is
bit-for-bit reproducible from the tag, signs the index and every image in it
both ways, verifies its own signatures round-trip, and prints the digests to the
job summary. Nothing is signed until every check above it has passed.

First-time setup, once:

```bash
cosign generate-key-pair          # keep cosign.key and the password safe
cosign public-key --key cosign.key > cosign.pub
git add cosign.pub && git commit -m "release: publish the signing public key"
gh secret set COSIGN_KEY < cosign.key
gh secret set COSIGN_PASSWORD
```

A missing `COSIGN_KEY` fails the release rather than quietly downgrading to
keyless-only, because `cosign.pub` being published means consumers are told to
use it. `workflow_dispatch` with `allow_keyless_only` is the deliberate escape
hatch. The workflow also refuses to publish if the committed `cosign.pub` is not
the public half of the signing key — otherwise everyone following the README
would verify against the wrong one.

Re-running a release must be launched **from the tag**, not a branch: a keyless
signature made on a branch carries an identity ending `@refs/heads/...`, which
the documented verification command cannot check.

### Adding a collector

Create `internal/collector/<name>.go`, declare descriptors with the package's
`desc` helper (which registers them for `Describe`), implement
`Update(ctx, ch) error`, and `register` the collector in an `init` function.
Nothing else needs to change: the flags, the help output and the per-collector
health metrics are all driven from the registry.

## License

Apache 2.0. See [LICENSE](LICENSE).
