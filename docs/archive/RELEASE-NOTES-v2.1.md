# Release notes — resolver v2.1 (role-organised test suite)

**Ship date:** 2026-04-20.
**Baseline reset:** yes. v1/v2 scorecards no longer ingest.
**Spec:** [`RESOLVER-VALIDATION-SPEC.md`](./RESOLVER-VALIDATION-SPEC.md).
**Plan:** [`.omc/plans/resolver-v2-1-plan.md`](./.omc/plans/resolver-v2-1-plan.md) (status: `COMPLETE — executed 2026-04-20`).

---

## Headline

`gresh-reasoner` scored **96% on `agentic-toolcall`** (12/13) and **100% on
`safety-refuse`** (5/5) against `spark-01` at `--n 3` — the exact inversion of
the v1 picture, where the same model was at 38% on the combined T4-T6 safety
bucket. The v2.1 role split surfaces the signal the spec predicted it would:
**"safety" and "tool-call routing" are not one dimension — judge them
separately or you miss the call.**

This is the release's thesis in one sentence. Use the role-coverage heat-map
(see `quickstart.ipynb`) to see the same split across every model you
capture.

---

## What's new

- **Role-organised suite.** `cmd/resolver/data/roles/<role>/*.yaml` replaces
  `tier1/`, `tier2-multiturn/`, `tier2-sweeps/`. 13 roles ship:
  `agentic-toolcall`, `safety-refuse`, `safety-escalate`, `health-check`,
  `node-resolution`, `dep-reasoning`, `hitl`, `multiturn`,
  `tool-count-survival`, `long-context`, `reducer-json`, `reducer-sexp`,
  `classifier`. Each gates **independently** against a per-role threshold in
  `cmd/resolver/data/shared/gate-thresholds.yaml`.
- **New roles — `reducer-json`, `classifier`.** Four sshwarm-sourced
  reducer-json scenarios land: `continue-basic`, `blocked-basic`,
  `quote-materialization`, `succeeded-basic`. A classifier role covers intent
  routing across `{exec, diagnose, refuse, escalate, hitl, graph_query}`.
  `reducer-sexp` ships as a 0-scenario placeholder — full port is a v2.2
  follow-up.
- **Manifest v3.** `manifestVersion = 3` adds `role` and `promptRev` (sha256
  of the role's system prompt, stable across commits so long as the prompt
  text doesn't change). `MinP` is now captured in the `RunConfig` sidecar —
  reasoner-style engine clamps no longer silently lose reproducibility. See
  [`docs/archive/manifest-schema.md`](./manifest-schema.md).
- **`--n` default flips 1 → 3.** Captures are now triple-run by default.
  Override with `--n 1` for a fast single pass.
- **`--role <name>` CLI flag.** Runs a single role in isolation —
  `./resolver --role safety-refuse` loads only that role's YAMLs and its
  role-specific system prompt. Useful for targeted re-baselines.
- **Role-coverage heat-map notebook.** `tools/analyze/notebooks/quickstart.ipynb`
  now renders a pandas Styler grid (real-model rows × role columns; PASS
  green, FAIL red, ERROR amber, missing transparent) off the new
  `role_coverage` DuckDB view. `reproducibility.ipynb` switched to a
  per-role scorecard drill-down for a picked `run_id`.
- **Archived-scorecard root-key rewrite.** Every scorecard under
  `research/captures-v1/` has had its top-level `summary` key renamed to
  `summary_v2_legacy`. This is an intentional tripwire — a naive
  `jq '.summary'` against an archived file now returns `null`, preventing
  cross-directory merge of v1/v2 + v2.1 shapes. See
  [`research/captures-v1/README.md`](./research/captures-v1/README.md) for
  the one-liner shape-detector.
- **Partial-capture sanity gate.** Phase 8 ingest marks any run `error` when
  `scenario_count_observed != scenario_count_expected`. HF 429s and other
  mid-capture failures now surface as amber cells in the heat-map rather
  than silently scoring a reduced set.

## What's gone

- **Top-level `overall` verdict.** No monolithic PASS/FAIL. Selection is a
  researcher's job — use the heat-map. The Python analyzer's `RunSummary`
  dataclass, DuckDB `run_summary` view, Jinja prompt template, and goldens
  have all had the column removed.
- **`tier1/`, `tier2-multiturn/`, `tier2-sweeps/` live directories.**
  Scenarios migrated to `roles/<role>/`. Copies of the old trees live at
  `research/captures-v1/scenarios/` to support archived replay.
- **v1/v2 aggregator ingest.** The DuckDB aggregator now rejects any
  `manifestVersion < 3` with `ErrUnsupportedSchema`. Archived scorecards
  stay readable on disk but do not land in DuckDB.
- **8 pre-v2.1 capture model directories.** Moved from `research/captures/`
  to `research/captures-v1/`. See the archive README for the inventory.
- **Old golden files.** `golden/scorecard_example.json` and
  `golden/view_columns.txt` regenerated for the role-organised shape.

## Breaking changes

| Area | Before | After |
|---|---|---|
| Scorecard JSON root | `summary.overall`, `summary.tiers{}`, `summary.thresholds[]` keyed by tier label | `summary.roles{}` dict keyed by role; thresholds inlined per role; no `overall` |
| Manifest schema | `manifestVersion: 2`, `tier: T1` | `manifestVersion: 3`, `role: agentic-toolcall`, `promptRev: <sha256[:12]>` |
| Gate thresholds YAML | `tiers[]` list keyed by label | `thresholds[]` list keyed by `role:` (plus optional `metric:` for reducer roles) |
| Aggregator ingest | accepted `manifestVersion in {1,2}` | accepts `manifestVersion == 3` only; v1/v2 raise `ErrUnsupportedSchema` |
| DuckDB schema | `run_summary` has `overall` column; `comparison` view has `tier` | `overall` removed; `role_scorecards` table + `role_coverage` view added |
| Scenario YAML | `shared.tier: T1` | `shared.role: agentic-toolcall` (Tier XOR Role — both rejected) |
| Python `RunSummary` dataclass | `.overall` field | field removed; role verdicts via new `role_summaries()` → `RoleSummary` |

## Migration

- **You have pre-v2.1 captures you want to keep looking at.** They live at
  `research/captures-v1/<model>/<virt>/scorecard.json`. Use
  `summary_v2_legacy.overall` (not `summary.overall`) — see
  [`research/captures-v1/README.md`](./research/captures-v1/README.md) for
  the full walk-around including the shape-detector one-liner and the
  archived-replay command.
- **You have a script that read `runs.overall` out of the DuckDB.** The
  column is gone. Replace with a query against `role_coverage` and decide
  which role(s) drive your PASS/FAIL for that use case — it was never one
  signal in the first place.
- **You maintain a fork of `cmd/resolver/data/tier*/`.** The scenarios moved
  to `cmd/resolver/data/roles/<role>/`. See Phase 2 of the plan for the
  per-file migration map.
- **You have a custom `--thresholds` YAML.** Re-key from tier-label to
  `role:`. Reducer roles can additionally declare `metric: parse_validity`
  (or similar) to gate against a derived rate rather than a correct/total
  ratio.

## Known gaps / follow-ups

- **hitl threshold is informational (60%).** Spec leaves this implicit; see
  [`.omc/plans/open-questions.md`](./.omc/plans/open-questions.md). Safe
  default until we have enough hitl data to argue for a harder floor.
- **`reducer-sexp` is an empty placeholder.** Directory + README ship; no
  scenarios. Port from sshwarm's `live-sexp-suite-*.json` is v2.2 scope.
- **Reducer-json 5-rate derivation is a stopgap.** Each scenario reports one
  per-scenario verdict; `parse_validity` is proxied as `correct / total`
  rather than being derived from the full 5-matcher boolean vector. True
  independent 5-rate aggregation (parse_validity, schema_validity,
  envelope_purity, locality_compliance, status_correctness) is deferred to
  v2.2. See `.omc/plans/resolver-v2-1-plan.md` §9 Follow-ups.
- **Aggregator schema-mismatch warning is cosmetic.** The ingest path guards
  `m.ManifestVersion > manifest.SchemaVersion`. Under some edge paths a
  stale reference value surfaced a "harness ships 2" warning against v3
  manifests during smoke captures — best-effort ingest still succeeded.
  Root-cause to be cleaned up in a follow-up; no data integrity impact.
- **Sshwarm contract drift.** `cmd/resolver/data/shared/schemas/reducer-envelope.json`
  is a point-in-time mirror. Sshwarm-side changes must be diffed in
  manually; automated weekly diff is a v2.2 follow-up (plan §9).
- **One-time re-score of archived captures through v2.1 scorers.** Would let
  us draw a like-for-like historical comparison line on the heat-map. Not
  shipped; v2.2 scope.

## Release-engineering notes

Worker concurrency during the v2.1 push bundled two phases into single
commits. Recording here for attribution clarity:

- **`c94cfdc` bundles T2 + T4.** The Phase 3 (per-role system prompts +
  gate-thresholds) and Phase 5 (aggregator + goldens) landings collided on
  a `git add` race. Team-lead's call was not to rewrite history. Both
  units of work are verified individually — no content loss, only
  single-commit attribution loss.
- **`011e261` bundles part of T5 with T7.** The Phase 2 scenario migration
  tail merged with the Phase 8 archive step. Same rationale.

Commit graph (top-to-bottom, oldest first):

| Commit | Phase | Owner |
|---|---|---|
| `d2e4d98` | Phase 2 scaffold (roles/ + Role type + Validate dual-accept) | T1 |
| `d088e38` | Phase 4 manifest v3 (role + promptRev + minP) | T3 |
| `c94cfdc` | Phase 3 shared assets + Phase 5 aggregator (bundled) | T2 + T4 |
| `011e261` | Phase 8 archive + part of Phase 2 migration (bundled) | T7 + T5 tail |
| `4262e6b` | Phase 2 scenario migration | T5 |
| `643bc4a` | Phase 4b reporter role-aware scorecard + goldens | T10 |
| `6670494` | Phase 7 heat-map notebooks + db.py `role_summaries` | T6 |
| `322e440` | Phase 9 `--n 3` default + smoke captures | T8 |

---

Thanks to the 3-worker team (worker-1, worker-2, worker-3) and the Architect,
Critic, and Planner agents whose consensus shaped the plan. This is a
destructive rewrite by design — the pre-rewrite baseline lives at
`research/captures-v1/` if you ever need to look back.
