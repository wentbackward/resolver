# ADR 0001 — Manifest v2: `runConfig` sidecar + `/v1/models` probe

**Status:** Accepted
**Date:** 2026-04-19
**Supersedes:** none
**Plan:** `.omc/plans/resolver-v2-plan.md`, Phase 1
**Spec:** `.omc/specs/deep-interview-resolver-v2-comparison.md`

## Context

v1's scorecard is byte-pinned to `RESOLVER-VALIDATION-SPEC.md` §7. We intentionally forbid adding fields to `meta` — it would break cross-model historical comparability. But v1 records almost no context about **how** a run was configured: thinking on/off, MTP mode, tool parser, context window, quantisation, etc. are invisible to the scorecard consumer.

The benchmark's stated purpose — comparing LLMs for high-consequence agentic work — depends on knowing what was actually under the client when a given scorecard was produced. Same virtual model can map to different real models; same real model can be served with different vLLM flags; same flags can be wrapped in different proxy sampling clamps. Without provenance the comparison tooling can only say "these two `gresh-general` runs differ" — not **why**.

## Decision

Introduce manifest schema v2. Two additions:

1. **`runConfig` object** — optional, free-form per v2 plan, covering proxy route defaults, vLLM engine flags, and capture metadata. Loaded from a `--run-config PATH` YAML sidecar and embedded verbatim into `manifest.json`.
2. **`resolvedRealModel` probe** — wire the previously-scaffolded field by issuing `GET {endpoint_origin}/v1/models` before the first scenario request. 5 s deadline, `"unknown"` on any failure, single-line stderr log.

Bump `manifestVersion` constant from `1` to `2`.

### Rejected alternatives

- **Add the fields to scorecard `meta` instead.** Violates §7 byte parity. Every historical scorecard diff would permanently flag v2-captured runs as structurally different.
- **Probe vLLM directly (bypass proxy).** Couples the harness to the proxy's internal topology. Most of what we care about (MTP method, tool parser) is a startup flag vLLM doesn't expose via runtime API — probing it would only fractionally cover the fields the sidecar must carry anyway.
- **Make `runConfig` mandatory.** Rules out remote-hosted models (HF, Anthropic) where engine state is not queryable. Per v2 plan principle #4, `"unknown"` must be a valid value.

## Consequences

- v1 manifests remain ingestable by v2 tools. The aggregator's backward-compat test covers this.
- v2 manifests are forward-compat: consumers should ignore unknown top-level keys. The `runConfig` shape itself is free-form (every field optional) so additional fields can land without a v3 bump.
- Secret management: users are instructed (in `docs/manifest-schema.md`) not to place bearer tokens or private URLs in the sidecar, since it is captured verbatim.
- Probe adds a 5 s one-time cost to every live run. Skipped entirely in `--replay` mode.
- The probe's return value surfaces to the scorecard as `manifest.resolvedRealModel` — never into the scorecard's `meta`. Same separation of concerns that kept §7 parity in v1.

## Consequences (negative)

- Adopters running against providers whose `/v1/models` returns an atypical envelope (not `{data:[{id,root}]}`) will see `resolvedRealModel="unknown"` and a single stderr warning. That's the correct failure mode, but it is a real regression for anyone who had built tooling on the scaffolded-but-unused `ResolvedRealModel` field expecting it to be empty.
- HF-serverless models will always record engine-level fields as unset/unknown. This is honest but reduces the amount of data the v2 aggregator + analyser has to work with for HF rows — by design.

## Verification

- `internal/manifest/manifest_test.go` covers the round-trip, missing-file, invalid-YAML, omitempty, and v1-compat cases.
- `internal/adapter/probe_test.go` covers the success, non-2xx, malformed-body, timeout, and unreachable paths.
- `cmd/resolver/golden_test.go` (`TestGoldenReplay`) continues to pass — v1 scorecard parity is preserved because none of these changes touch `report/scorecard.go`.
