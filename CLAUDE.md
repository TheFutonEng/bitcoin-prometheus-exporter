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

## Open items, in priority order

1. ~~The `release` environment has no required reviewer.~~ Fixed by Rob
   2026-11-27 (not re-verified from clwsbot: reading environment protection
   needs admin).
2. **`release.yml` does not name the environment yet.** The change (`environment:
   release` on `guard` and `publish`, same as TheFutonEng/bitcoin#28) is commit
   `097f2a6` on branch `release-environment` in Claude's clone, not pushed:
   it touches a workflow, so Rob pushes it. Until it merges, with the repo
   secrets gone, `HAS_KEY` reads empty and `guard` refuses to release (unless
   dispatched with `allow_keyless_only`). Safe, but releases are blocked.
3. **The required check `integration` never reports** (the matrix legs are
   `integration (<image>)`). Fixed on branch `ci/required-checks`: an
   always-running `integration-result` job named `integration` that passes
   only if change detection succeeded and every matrix leg did. Documentation-
   only changes are skipped by a `changes` job, not `paths-ignore`, so the
   required check still reports on them (this supersedes `ci/skip-docs-only`).
   Workflow change, so Rob pushes. Once merged, `test` and `stack` can be added
   to the ruleset safely: they are skipped (which counts as passing) only when
   `changes` says docs-only, and if `changes` itself fails, `integration` fails.
4. **Node image is three bitcoin releases behind.** Pinned to `31.1-3` in
   `Makefile` (`NODE_IMAGE`), `docker-compose.yml`, and
   `internal/integration/integration_test.go`; CI's matrix tests 31.1-3,
   31.1-1 and 31.1. Current is **`31.3-2`** (Bitcoin Core 31.3 on
   `base-nossl-debian13`); 31.2-1 and 31.3-1 also exist. A bump also has to
   change the hard-coded `/Satoshi:31.1.0/` subversion in
   `integration_test.go`. Keep the `31.1` leg in the matrix: it is the one
   with the original `-datadir`-in-cmd layout, per the comment in `ci.yml`.
   Bitcoin Core 32.x is next on the node side and may change RPC fields.
5. **PR #7** (Renovate's onboarding, opened 2026-11-25) is open and
   undecided.
