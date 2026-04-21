# Release notes — resolver v2.1.1 (harness-completion patch)

**Ship date:** 2026-04-21.
**Tag:** `v2.1.1` (local-only; not pushed).
**Plan:** [`.omc/plans/resolver-v2-1-1-plan.md`](./.omc/plans/resolver-v2-1-1-plan.md) (status: `COMPLETE — executed 2026-04-21`).
**Baseline:** extends v2.1 (shipped 2026-04-20, commit `70b7623`).

---

## Summary

v2.1.1 is a harness-completion patch: matcher wiring for reducer/classifier,
sweep-role verification via hard-fail gate, and uniform `metrics_json`
population. Full v2.1 harness is now usable end-to-end — all thirteen roles
load, all live roles populate the scorecard, and silent-drop failure modes
have been closed.

---

## What's new

### Fix 1 — Matcher wiring (reducer-json + classifier unblock)

Added three matcher kinds and one scenario metadata field to
`internal/scenario` and `internal/verdict`:

- `label_is: <label>` — case-insensitive, whitespace-trimmed,
  trailing-punctuation-stripped, `<think>…</think>`-preamble-stripped equality
  against assistant content. Used by classifier scenarios.
- `parse_valid_json: true` — `json.Valid([]byte(stripped))` after `<think>`
  and whitespace strip. Only `true` is meaningful; explicit `false` is
  rejected as authoring error.
- `json_field_present: <field>` — content parses as a JSON object and the
  named top-level field is present with a non-null value.
- `expected_label: <label>` — Scenario metadata mirroring `ExpectedTool`.
  Not consumed by validator/evaluator; informational for classifier-role
  reporting.

Before this fix, reducer-json's 4 scenarios and classifier's 6 scenarios
failed YAML validation with `matcher must set exactly one kind, got 0` and
silently produced 0 captures. Post-fix: both roles load end-to-end.
(Commit `2cd2465`.)

### Fix 2 — Sweep-role hard-fail gate

`internal/runner/executor.go` gains a pre-loop guard in `RunTier1`: any
scenario declaring `fixtures` or `needle` now fails with
`verdict.ScoreError` and reason `"scenario declares fixtures/needle but
role path does not assemble context (v2.2 carry-over)"` instead of
silently running with dropped context. The long-context role's single
needle scenario intentionally FAILs its v2.1.1 verdict under this gate.
(Commit `40d5b83`.)

This replaces a silent-drop failure mode that shipped undetected in v2.1.
Any future Scenario field added without a consumer in the executor path
will now surface loudly on the first scenario that uses it.

### Fix 3 — Uniform `metrics_json`

`internal/report/scorecard.go` now populates common counters for every
role: `{pct, correct, partial, incorrect, error, total}`. Role-specific
derivations retained: classifier → `accuracy`, reducer-json /
reducer-sexp → `parse_validity`. Golden regenerated once under
`UPDATE_GOLDEN=1`; diff is purely additive within each role's `metrics`
object. Notebooks that filter on `metrics_json != '{}'` now pivot
uniformly across the full role set. (Commits `2ba9e03`, `f679830`.)

---

## What's changed semantically

- **Long-context verdict** — now hard-fails until the v2.2
  context-assembly runner lands. Scorecard rows show
  `Score == verdict.ScoreError` with the `v2.2 carry-over` reason.
- **Reducer-sexp verdict** — harness emits an INFO scorecard entry
  (scenarioCountObserved = 0, empty `Metrics`) with a one-time stderr
  warning; does not hard-fail. Removes the ambiguity of "0 scenarios =
  failed load" vs "intentionally empty". (Covered by v2.1's existing
  placeholder behaviour, formalised here.)
- **`metrics_json` shape** — uniformly populated across live roles; no
  more `'{}'` cells in `role_scorecards` for v2.1.1 captures. Heat-map
  notebooks gain an extra pivot axis.

Byte-exact golden parity with v2.1 broke exactly once (Fix 3) as an
intentional, additive change. No other v2.1 behaviour shifted.

---

## Sweep results

Full 3 models × 12 live roles × n=3 sweep ran on 2026-04-21 against
`http://spark-01:4000/v1/chat/completions`. Output committed to
`research/captures/Qwen_Qwen3.6-35B-A3B-FP8/<model>/<role>/`. 108
`role_scorecards` rows ingested; 0 empty `metrics_json` cells in the
v2.1.1 sweep set.

### PASS / FAIL per (model, role)

Each row below is n=3 seeds. Roles with intentional structural FAIL
(long-context hard-fail) are flagged `(HF)`.

| Model            | Role                | Verdict (n=3) |
|------------------|---------------------|---------------|
| gresh-general    | agentic-toolcall    | FAIL × 3 |
| gresh-general    | safety-refuse       | PASS × 3 |
| gresh-general    | safety-escalate     | PASS × 3 |
| gresh-general    | health-check        | PASS × 3 |
| gresh-general    | node-resolution     | FAIL × 3 |
| gresh-general    | dep-reasoning       | PASS × 3 |
| gresh-general    | hitl                | PASS × 3 |
| gresh-general    | multiturn           | FAIL × 3 |
| gresh-general    | tool-count-survival | FAIL × 3 |
| gresh-general    | long-context        | FAIL × 3 (HF) |
| gresh-general    | reducer-json        | PASS × 3 |
| gresh-general    | classifier          | FAIL × 3 |
| gresh-coder      | agentic-toolcall    | PASS × 3 |
| gresh-coder      | safety-refuse       | PASS × 3 |
| gresh-coder      | safety-escalate     | FAIL × 3 |
| gresh-coder      | health-check        | PASS × 3 |
| gresh-coder      | node-resolution     | FAIL × 3 |
| gresh-coder      | dep-reasoning       | PASS × 3 |
| gresh-coder      | hitl                | PASS × 3 |
| gresh-coder      | multiturn           | FAIL × 3 |
| gresh-coder      | tool-count-survival | PASS × 3 |
| gresh-coder      | long-context        | FAIL × 3 (HF) |
| gresh-coder      | reducer-json        | FAIL × 3 |
| gresh-coder      | classifier          | FAIL × 3 |
| gresh-reasoner   | agentic-toolcall    | FAIL × 3 |
| gresh-reasoner   | safety-refuse       | PASS × 3 |
| gresh-reasoner   | safety-escalate     | FAIL × 3 |
| gresh-reasoner   | health-check        | PASS × 3 |
| gresh-reasoner   | node-resolution     | FAIL × 3 |
| gresh-reasoner   | dep-reasoning       | PASS × 3 |
| gresh-reasoner   | hitl                | PASS × 3 |
| gresh-reasoner   | multiturn           | FAIL × 3 |
| gresh-reasoner   | tool-count-survival | PASS × 3 |
| gresh-reasoner   | long-context        | FAIL × 3 (HF) |
| gresh-reasoner   | reducer-json        | FAIL × 3 |
| gresh-reasoner   | classifier          | FAIL × 3 |

Totals: **17 PASS** / **19 FAIL** of 36 model-role runs (each run = n=3
seeds). 3 of the 19 FAILs are the intentional long-context hard-fail.
The remaining 16 FAILs are legitimate threshold drops and are the signal
this release was built to surface. Scenario-quality work on those roles
is a v2.2 queue item.

---

## Known gaps (v2.2 carry-over)

- **Long-context context-growth assembly.** `RunTier1` still has no
  fixture/needle runner; the long-context role's needle scenario
  intentionally FAILs in v2.1.1 captures and must wait for a v2.2
  context-assembly runner (or a `RunTier1` extension).
- **Reducer-json true 5-rate derivation.** `pct` and `parse_validity`
  both compute to `correct/total` in v2.1.1 — redundant. v2.2 derives
  the five independent rates (parse_validity, schema_validity,
  envelope_purity, locality_compliance, status_correctness) from the
  matcher boolean vectors instead.
- **Reducer-sexp scenario authorship.** v2.1.1 inherits v2.1's
  0-scenario placeholder plus inline INFO-verdict skip. Port
  sshwarm's `live-sexp-suite-*.json` into `roles/reducer-sexp/` to
  remove the special case.
- **`--sweep` CLI cleanup.** `tier2-sweeps/` has been empty since v2.1;
  the legacy `cmd/resolver/main.go --sweep tool-count|context-size`
  path is dead code. Decision deferred to v2.2: delete,
  deprecation-warn, or repurpose at
  `roles/{tool-count-survival,long-context}/*.yaml`.
- **`Expected*` consolidation.** v2.1.1 adds `ExpectedLabel` alongside
  `ExpectedTool`. If a third `Expected*` field lands, collapse into
  `Expected map[string]any`.
- **"Harness ships N" cosmetic ingest warning.** Pre-existing from
  v2.1; not escalated by this release.
- **CI still offline.** No GitHub Action runs `go test -tags duckdb
  ./...` on push. v2.2 scope.

All tracked in [`.omc/plans/open-questions.md`](./.omc/plans/open-questions.md)
under the `v2.1.1 follow-ups` section.

---

## Verification

| Check | Result |
|---|---|
| `go test -count=1 ./internal/scenario/... ./internal/verdict/...` | PASS (Fix 1 tests green) |
| `./resolver --role reducer-json --dry-run` → 4 scenarios | PASS |
| `./resolver --role classifier --dry-run` → 6 scenarios | PASS |
| `go test ./internal/runner/... -run TestRunTier1_Rejects...` | PASS (Fix 2 hard-fail) |
| `go test -tags duckdb ./cmd/resolver/... -run TestGoldenReplay` | PASS (post-regen) |
| `./scripts/golden-diff-sanity.sh` | PASS (drift scope-locked to metrics) |
| `./.reporting/resolver-duckdb aggregate` | 171 runs ingested |
| `SELECT COUNT(*) FROM role_scorecards WHERE started_at >= 2026-04-21` | 108 (≥ 99) |
| `SELECT COUNT(*) FROM role_scorecards WHERE metrics_json = '{}' AND started_at >= 2026-04-21` | 0 |

---

## Commits (local; not pushed)

```
075fc84 v2.1.1 sweep: 3 models × 12 live roles × n=3 captures
f679830 v2.1.1 Fix 3b: regenerate goldens (role_scorecards shape + metrics_json)
2cd2465 v2.1.1 Fix 1: matcher wiring for reducer-json + classifier
40d5b83 v2.1.1 Fix 2: RunTier1 hard-fail for fixtures/needle + sweep-role tests
2ba9e03 v2.1.1 Fix 3a: populate role_scorecards.metrics_json (golden regen pending in T3b)
```
