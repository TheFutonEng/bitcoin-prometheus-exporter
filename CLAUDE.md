# CLAUDE.md — bitcoin-prometheus-exporter

Context for picking this repo up in a new session. How Claude commits and
pushes (the `clwsbot` identity, its own clone, HTTPS with a token that has no
`workflow` scope, never Rob's credentials) is in Rob's global
`~/.claude/CLAUDE.md` and is not repeated here.

## What this is

A Prometheus exporter for Bitcoin Core, in Go (`cmd/bitcoin-exporter`,
`internal/{rpc,collector,integration}`). It reads a node over JSON-RPC and
exposes metrics on `:9332`. The node it is developed and tested against is the
sibling repo's image, `ghcr.io/thefutoneng/bitcoin`
(TheFutonEng/bitcoin, which has its own CLAUDE.md with the release process this
repo's mirrors).

- `make up` / `make activity` / `make down`: regtest node + exporter +
  Prometheus + Grafana. `make check-stack` runs CI's alert-rule and
  dashboard-query checks against it.
- `make test`, `make lint`, `make test-integration` (real node container).
- Releases: `release.yml` on a `v*` tag publishes `<version>` and `latest` to
  GHCR, signed keyless and with the key pair (`cosign.pub` is committed),
  reproducible per platform (`make repro-digest`,
  `make verify-repro-published`). Only `v0.1.0` has been released (2026-09-28).

## Access and protections (verified 2026-11-27)

clwsbot has collaborator **Write** (accepted 2026-11-27). Write plus a
signing workflow is only safe with these in place, for the same reason as in
the bitcoin repo: keyless signing trusts any `release.yml` run on a tag, and
`cosign verify --key` checks no identity at all.

| Control | State |
|---|---|
| `release tags` ruleset (id 28236673) | active: create/update/delete of `refs/tags/v*` restricted |
| `main branch` ruleset (id 24243236) | active: PR with 1 approval, stale approvals dismissed, last-push approval, signed commits, required check `integration` |
| `release` environment | exists; deployment policy: tags `v*` only; holds `COSIGN_KEY`, `COSIGN_PASSWORD` |
| repo-level secrets | none (deleted) |

clwsbot cannot read `bypass_actors` (the API returns null to non-admins), so
the admin bypass on both rulesets can only be checked from Rob's account.

## Done (2026-11-27)

- `release` environment has a required reviewer (Rob; not re-verifiable from
  clwsbot, reading environment protection needs admin).
- #9: `guard` and `publish` name `environment: release`, so releases can sign
  again with the repo-level secrets gone.
- #10: the required `integration` check reports. It is the always-running
  `integration-result` job, named `integration`, which fails if change
  detection fails and, on a code change, unless every matrix leg passed. A
  `changes` job skips `test`, `integration` and `stack` on `*.md`/`LICENSE`-only
  changes (via `if:`, not `paths-ignore`, so the required check still reports).
- #11: node image `32.1-1` in the Makefile, `docker-compose.yml` and the
  integration default; CI matrix 32.1-1, 31.3-2, 31.1-3 and 31.1 (the original
  `-datadir`-in-cmd layout; keep that leg). The peer-subversion assertion asks
  the peer node instead of hard-coding a release. Bitcoin Core 32 dropped
  `fullrbf` from `getmempoolinfo`, so `bitcoin_mempool_full_rbf` is absent
  against it (missing fields are omitted by design); that was the only
  `/metrics` difference (all collectors) between 31.3-2 and 32.1-1.
- #7: Renovate is on. See below.

## Renovate

`renovate.json` extends `config:recommended`, plus:

- Regex managers for the Makefile's `GO_BASE`/`RUNTIME_BASE` and `ci.yml`'s
  `GO_VERSION`, all grouped with the Dockerfile ARGs into one **"base images"**
  PR. The Dockerfile and Makefile pins must agree (`scripts/check-pins.sh`),
  and that check runs only in `release.yml`, so a split bump would pass PR CI
  and break the next release.
- `ghcr.io/thefutoneng/bitcoin` is disabled. A node bump is manual: it touches
  the Makefile, `docker-compose.yml`, `integration_test.go` and the CI matrix,
  and should be checked by diffing `/metrics` against the old and new node.

Renovate's PRs that change `.github/workflows/` (action bumps, `GO_VERSION`)
come from the Renovate app, which can push workflow changes; they still need
Rob's approval like everything else.

## Open items

1. Optional: add `test` and `stack` to the `main` ruleset's required checks.
   Safe since #10: they skip only on docs-only changes, and a failed `changes`
   job fails `integration`.
2. Consider running `make check-pins` in PR CI (a workflow change, so Rob
   pushes) so a pin mismatch fails before merge, not at release time.
3. No release since `v0.1.0`. The next one is the first through the `release`
   environment.
