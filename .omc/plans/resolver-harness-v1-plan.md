# Plan: Resolver Agentic Harness v1 (Go)

**Source spec:** `.omc/specs/deep-interview-resolver-agentic-harness-v1.md` (ambiguity 17%, status PASSED)
**Mode:** Consensus (RALPLAN-DR short) · Iteration: 2 (after Architect + Critic review) · Non-interactive · **CONSENSUS APPROVED**

---

## RALPLAN-DR Summary

### Principles
1. **Spec is the contract.** `RESOLVER-VALIDATION-SPEC.md` §§3–9 define Tier 1 behaviour; implementation must preserve system prompt, tool shape, regex rules, scorecard JSON, filename format, and exit codes *exactly*.
2. **Tier 1 parity before Tier 2 features.** Sweeps and multi-turn scenarios reuse the Tier 1 runner; shipping them on an untested core is premature.
3. **Stdlib-plus-yaml-plus-tokenizer, no other deps.** `net/http`, `regexp`, `encoding/json`, plus `gopkg.in/yaml.v3` and exactly one tokenizer library. No web framework, no test framework beyond `testing` + `go-cmp` for diffs.
4. **Verdicts are pattern-based in v1.** No LLM judge; verdict determinism is a testing property and the whole scorecard is byte-diffable against a golden.
5. **Unified scenario schema from day one.** The same YAML shape drives Tier 1 (degenerate 1-turn scenario) and Tier 2 multi-turn. All v1 fields defined in Phase 1; later phases add data, not schema.

### Decision Drivers
1. **Time-to-useful-sweep.** The user's asked-for artefact is the tool-count + context-size curves. Tier 1 parity is a prerequisite, not the deliverable.
2. **Byte-exact spec parity.** Scorecard JSON keys, filename format, and exit codes must match `RESOLVER-VALIDATION-SPEC.md` §7–§8 so cross-model historical runs remain comparable forever.
3. **Extensibility without scenario rewrites.** Adapter interface and verdict interface stable enough that Anthropic / openclaw / hf-serverless adapters slot in later without touching YAML.

### Viable Options

**Option A — Port Tier 1 first, then extend runner to Tier 2 (CHOSEN)**
- *Pros:* Working regression baseline by end of Phase 2. Spec §7 scorecard and §5 regex rules exercised end-to-end before sweep logic layers on. Each subsequent phase adds capability without rewriting.
- *Cons:* Delays first sweep by one phase. Requires the scenario schema to be forward-compatible with Tier 2 fields in Phase 1 (now explicitly done — see principle #5).

**Option B — Build generalized multi-turn runner first, treat Tier 1 as 1-turn scenarios**
- *Pros:* Unified runner from day one; no migration.
- *Cons (invalidating):* Without a working Tier 1 scorecard, the project has no regression baseline against which to detect Tier 2 runner bugs. Spec's gated thresholds are the cheapest way to catch "model works, harness is wrong" vs. "harness works, model is wrong" early. *Partially folded:* Option B's valid insight (unify schema up front) is adopted as principle #5, eliminating the steelman attack vector without adopting the option.

**Option C — Parallel tracks (Tier 1 + Tier 2 concurrently)**
- *Pros:* Fastest wall-clock if two operators.
- *Cons (invalidating):* Single-operator context per the spec's non-goals; parallel tracks also guarantee interface churn — scenario schema and adapter interface would be ratified twice, once per track.

**Option D — Tier 1 parity only in v1; Sweeps A+B in v1.1**
- *Pros:* ~60% of the implementation effort. Hardens Tier 1 regex + parser + scorecard shape before building sweep machinery on top. Gives a clean regression baseline that can be diffed against every model change while sweep design matures.
- *Cons (invalidating):* User's Round 3 answer in the deep-interview spec explicitly picked "Tier 1 parity + sweeps A/B + multi-turn + mocked data" as the definition of "shippable v1". Rejecting Option D is therefore a *user-gated*, not a technically-gated, decision. If the user reverses Round 3, Option D becomes the right path — flagged in ADR follow-ups.

**Option E — LLM-as-judge verdicts in v1 (rejected for completeness)**
- *Pros:* Enables subjective criteria (reasoning quality, explanation clarity) from the start.
- *Cons (invalidating):* Deep-interview spec explicitly defers judge-LLM to v2 (Round 3 answer). Also violates principle #4 (verdict determinism) and inflates the v1 critical path by adding a second model to pin.

---

## Requirements Summary

Build a Go 1.22+ single-binary test harness at `/home/code/hacking/resolver/` that:

1. Reimplements the 31-query `RESOLVER-VALIDATION-SPEC.md` benchmark as Tier 1, with **exact** parity on system prompt (§3), tool definitions (§4), validation rules including partial-credit rules (§5), threshold gating (§6), scorecard JSON shape and filename format (§7), CLI contract (§8), and tool-call fallback parser (§9).
2. Extends the runner to multi-turn scenarios with scripted tool responses, mocked data-producing tools (`read_document`, `web_search`, `fetch_api`), and per-turn metrics.
3. Adds two meta-sweeps — Sweep A (tool count: 5/20/50/100/300) and Sweep B (context size: 5K/40K/80K/120K/200K) — that emit CSV curves plus operator-configurable PASS/FAIL gates via `--gate policy.yaml`.
4. Ships `openai-chat` adapter only (via llm-proxy). Anthropic, openclaw, hf-serverless are explicit v2.
5. Defaults: `--endpoint http://localhost:4000/v1/chat/completions` and `--model gresh-general` (llm-proxy virtual → Qwen/Qwen3.6-35B-A3B-FP8). Env vars `$RESOLVER_ENDPOINT` / `$RESOLVER_MODEL` override defaults with lower precedence than flags.
6. Serial execution per-query (spec §9), `temperature=0`, 180s per-request timeout. Sweep seeds may run in parallel only behind an explicit `--parallel` flag.
7. Every run writes a sibling `manifest.json` (model, adapter config, scenario hashes, seeds, timestamps). Scorecard `meta` stays byte-identical to spec §7 keys; Go-specific metadata lives in the manifest only.
8. Module path: `github.com/wentbackward/resolver` (matches the GitHub repo at https://github.com/wentbackward/resolver).

---

## Acceptance Criteria

(Each is testable. Labels: ✓ = inherited from deep-interview spec; + = added in Planner draft; ⊕ = added in iteration 2 from reviewer feedback.)

### Tier 1 Parity
- [ ] ✓ All 31 queries encoded in YAML under `scenarios/tier1/T1..T10.yaml`. System prompt at `scenarios/tier1/system-prompt.md` matches spec §3 verbatim; `go test` asserts `sha256(system-prompt.md) == <pinned constant>` to guard against whitespace/curly-quote drift.
- [ ] ✓ Five tool definitions match spec §4 shape; `required` is set for every argument; tool definitions live in a single shared YAML consumed by Tier 1 scenarios.
- [ ] ✓ Validation rules encoded per spec §5 — every listed regex present, case-insensitive applied, "partial = 0.5" enforced in tier aggregation.
- [ ] ⊕ **Partial-credit rules explicitly encoded and tested** for spec §5 cases where partial applies: T2.2 (1–2 `health_check` on spark-01), T7.1 / T7.2 / T7.3 (each with its own partial condition), T8.1 / T8.3, T9.1 / T9.2, T10.3. Per-rule unit tests: one input that scores `correct`, one that scores `partial`, one that scores `incorrect`.
- [ ] ✓ Tool-call fallback parser handles `functionName(arg="...", arg="...")` text per spec §9, including nested parens and named-vs-positional args. Covered by unit tests with **≥7 inputs**: structured, text-named, text-positional, mixed-quotes, nested-parens, multi-call, and **one malformed input that must return `[]` without panicking**.
- [ ] ⊕ **Scorecard filename format matches spec §7**: files land at `reports/results/{modelSlug}_{iso}.json` where `modelSlug` replaces each non-`[A-Za-z0-9._-]` character with `_` and collapses runs of `_`, and `{iso}` is ISO-8601 UTC with `:` and `.` replaced by `-`, truncated to seconds. Unit test asserts regex `^[A-Za-z0-9._-]+_\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}\.json$` against the generator.
- [ ] ⊕ **Scorecard `meta` keys match spec §7 exactly**: `model`, `endpoint`, `timestamp`, `queryCount`, `nodeVersion`. Since this is Go, emit key literal `nodeVersion` with value equal to `runtime.Version()` (e.g. `go1.22.7`); this preserves the byte-exact shape and keeps historical comparisons intact. All Go-specific metadata (`runId`, `adapter`, `seed`, tokenizer mode, commit sha) lives in the sibling `manifest.json`, never in `meta`.
- [ ] ⊕ **`summary.tiers` includes entries for T1 through T10** (informational tiers T3 and T9 reported with the same shape; exclusion is only from `summary.thresholds`, not from `summary.tiers`). Golden-test fixture asserts all 10 tier keys present.
- [ ] ✓ Scorecard JSON output matches spec §7 shape **byte-exactly** against a golden fixture derived from the spec example. Golden test gates CI.
- [ ] ✓ Exit codes per spec §8: `0` / `1` / `2`.
- [ ] ⊕ **Five separate gated checks per spec §6, each surfaced as its own row in `summary.thresholds`**: `T1+T2 ≥ 90%`, `T4+T5+T6 ≥ 80%`, `T7 ≥ 60%`, `T8 ≥ 60%`, `T10 ≥ 60%`. T3 and T9 tiers appear in `summary.tiers` only, not in `summary.thresholds`.
- [ ] ✓ Queries run strictly serially; per-query `elapsedMs` recorded; `summary.timing` aggregates total/avg/p50/p95/max/count computed over non-error entries.

### Tier 2 — Multi-turn + Mocked Data Tools
- [ ] ✓ Scenario schema supports `turns: [{ role: user|tool, content | script }]` with scripted tool responses; Executor maintains `[system, ...messages]` across turns.
- [ ] ⊕ **Scenario YAML declares a `fixtures:` field** listing fixture IDs the scenario's mocked tools may draw from. Loader validates every referenced fixture exists under `fixtures/docs/` or `fixtures/graph/`.
- [ ] ✓ Mock tool types: `read_document`, `web_search`, `fetch_api`. Each returns a scripted fixture document sized to the scenario's declared `tokens` budget.
- [ ] ✓ At least one multi-turn scenario ships at `scenarios/tier2-multiturn/progressive-context.yaml` exercising ≥3 turns and ≥2 mock-tool calls.
- [ ] ✓ Per-turn metrics captured: `turn_idx`, `prompt_tokens`, `completion_tokens`, `cached_tokens` (when endpoint reports), `ttft_ms`, `total_ms`, `context_window_tokens`, `tools_called_this_turn`.
- [ ] + Multi-turn scenarios use the same verdict-kind vocabulary as Tier 1 (`tool_call_required`, `tool_call_forbidden`, `tool_call_order`, `regex_match`).

### Tier 2 — Sweep A (Tool Count)
- [ ] ✓ Runner takes an axis list (default `[5, 20, 50, 100, 300]`), runs the same core task at each axis point. Decoys generated from a template list (marketing/finance/HR/CRM) and never match Tier 1 regex rules.
- [ ] ✓ Per-model CSV at `reports/sweeps/{modelSlug}_tool-count_{iso}.csv` (slug + iso format identical to Tier 1 scorecard) with columns `[tool_count, seed, score, tools_called, wrong_tool_count, hallucinated_tool_count, completed]`.
- [ ] ⊕ **`contrib/gates/tool-count.yaml` ships as a reference gate policy** with example rules and rationale comments. AC: `resolver --sweep tool-count --gate contrib/gates/tool-count.yaml --axis "5,20"` runs end-to-end against a replay fixture.
- [ ] ✓ `--gate policy.yaml` supports per-axis-point thresholds. Harness emits PASS/FAIL and exits non-zero on any failed gate.

### Tier 2 — Sweep B (Context Size)
- [ ] ✓ Runner takes an axis list (default `[5000, 40000, 80000, 120000, 200000]` tokens) and assembles each context from the hand-selected fixture pool via mock-tool returns.
- [ ] ✓ At least one needle-in-haystack question per sweep cell; needle position declared in scenario YAML.
- [ ] ⊕ **Needle verdict explicitly defined**: scenario declares `needle.match_regex` (case-insensitive). `needle_found = true` iff the agent's final assistant message OR any `exec`/`graph_query`/`escalate`/`refuse` tool-call argument contains a match. False otherwise. Unit test covers: found-in-message, found-in-tool-arg, not-found-anywhere, case-insensitive match.
- [ ] ✓ Per-model CSV at `reports/sweeps/{modelSlug}_context-size_{iso}.csv` with columns `[context_tokens, seed, needle_found, accuracy, elapsed_ms]`.
- [ ] ✓ Same `--gate` mechanism as Sweep A.
- [ ] + Context assembly respects declared `context_growth_profile` (`flat` / `moderate` / `explosive`). v1 ships `flat` and `moderate`; `explosive` returns a clear "not implemented in v1" error.
- [ ] ⊕ **`contrib/gates/context-size.yaml` ships as a reference gate policy.**

### CLI / Ops
- [ ] ✓ `resolver --endpoint URL --model NAME` matches spec §8; additional flags: `--tier {1,2}`, `--scenario PATH`, `--sweep {tool-count,context-size}`, `-n N` for seeds, `--gate POLICY.yaml`, `--parallel` for sweep seed parallelism, `--dry-run` (lists queries + expected tool + validation rule without hitting network), `--api-key STRING` (stubbed — accepted but unused in v1; documented as pass-through for future auth adapters), `--replay PATH` (replays a canned responses JSON for offline golden testing).
- [ ] ✓ `$RESOLVER_ENDPOINT` / `$RESOLVER_MODEL` override defaults (below flag precedence). Documented precedence: flag > env > built-in default.
- [ ] ⊕ **Manifest schema stable and golden-tested**: `manifest.json` includes `runId` (ULID), `model`, `resolvedRealModel` (best-effort via `/v1/models`), `adapter` (`"openai-chat"`), `tokenizerMode` (`"qwen-bpe"` or `"heuristic"`), `seeds[]`, `scenarioHashes{id: sha256}`, `startedAt`, `finishedAt`, `goVersion`, `commitSha` (from `git rev-parse HEAD` if available, else `"unknown"`). Golden fixture asserts shape stability.
- [ ] ✓ README covers install (`go install ./cmd/resolver`), usage examples, scorecard interpretation **with a worked example scorecard excerpt so users can visually confirm parity with spec §7**, sweep gate authoring, v1→v2 roadmap.
- [ ] ✓ `go test ./...` passes. Coverage minimums: scenario loader (valid + invalid fixtures), fallback parser (≥7 inputs inc. malformed), each spec §5 regex + partial rule (3 cases each), scorecard shape golden, gate-policy evaluator, manifest shape golden, needle verdict.
- [ ] + `contrib/example.service` (systemd) + `contrib/example.cron` ship with example nightly Tier 1 invocation.

### Non-goal clarifications (limit scope creep)
- [ ] ⊕ **Principle #5 caveat documented in README**: scenario YAML currently declares OpenAI `tools` block shape directly — this is a known v1 limitation. Genuine adapter-agnostic abstraction (e.g. Anthropic tool format) is explicit v2 work.
- [ ] ⊕ **HTTP auth noted**: `--api-key` is stubbed and not required against the local `localhost:4000` llm-proxy. Documented in README.

---

## Implementation Steps

### Phase 1 — Bootstrap
**Files:** `go.mod`, `go.sum`, `README.md`, `.gitignore`, `cmd/resolver/main.go`, `internal/adapter/adapter.go`, `internal/scenario/scenario.go`.

1. `git init .` (currently not a repo). `.gitignore` excludes `reports/`, `.omc/state/`, `.omc/sessions/`, `.omc/logs/`, `*.tmp`.
2. `go mod init github.com/wentbackward/resolver` — resolved per ADR.
3. Define shared types in `internal/scenario/scenario.go`. **All v1 fields are declared here in Phase 1, optional where Tier 1 does not use them, so Phase 3 adds data, not schema**:
   - `Scenario { ID, Tier, Query string, Turns []Turn, AvailableTools []ToolDef, SuccessCriteria []SuccessCriterion, ExpectedTool string, Fixtures []string, ContextGrowthProfile string }`.
   - `Turn { Role string ("user"|"tool"|"assistant"), Content string, ScriptForTool string }`.
   - `SuccessCriterion` discriminated on `Kind` (`tool_call_required`, `tool_call_forbidden`, `tool_call_order`, `regex_match`).
4. Define `Adapter` interface in `internal/adapter/adapter.go`:
   ```go
   type Adapter interface {
     Run(ctx context.Context, s *scenario.Scenario, opts RunOpts) (*RunResult, error)
   }
   ```
   with `RunResult { ToolCalls []ToolCall; Content string; ElapsedMs int64; TokensIn/Out/Cached int; TTFTMs int64 }`. V2 note (in code comment): introduce `Turn(ctx, msgs, tools)` split when adding openclaw/anthropic adapters.
5. Implement `internal/adapter/openai_chat.go` with `http.Client` (timeout 180s), `tool_choice: "auto"`, `temperature: 0`, `max_tokens: 1024`. Tolerate `arguments` being string (JSON.parse) or object.
6. CLI skeleton in `cmd/resolver/main.go` using `flag` stdlib: `--endpoint`, `--model`, `--tier`, `--scenario`, `--sweep`, `-n`, `--gate`, `--parallel`, `--dry-run`, `--api-key` (stub), `--replay`. Env-var fallback before flag parsing.

### Phase 2 — Tier 1 Runner
**Files:** `internal/scenario/loader.go`, `internal/verdict/{verdict,regex,tool_call}.go`, `internal/runner/{executor,fallback_parser,metrics}.go`, `internal/report/scorecard.go`, `scenarios/tier1/*.yaml`, `scenarios/tier1/system-prompt.md`, `scenarios/shared/tools/*.yaml`, `golden/scorecard_example.json`.

1. `internal/scenario/loader.go` — `gopkg.in/yaml.v3` parsing into the Phase-1 struct; `Validate()` asserts required fields per tier, regex compile, fixture existence (if declared).
2. `internal/runner/fallback_parser.go` — text → `[]ToolCall`. Walks paren depth; supports named (`key="v"`), positional (`"a", "b"`), mixed quotes, nested parens. Must return `[]` (not panic) on malformed input.
3. `internal/verdict/regex.go` — compile-cache per pattern; `MatchCaseInsensitive(s, pat)` helper. `internal/verdict/tool_call.go` — `required`, `forbidden`, `order` evaluators. Partial-rule helper for spec §5 cases.
4. `internal/runner/executor.go` — serial per-query loop, per-query wall-clock, error → `score: "error"`, `reason: err.Error()`, `toolCalls: []`.
5. `internal/report/scorecard.go` — emits spec §7 JSON:
   - `meta` keys pinned to `model, endpoint, timestamp, queryCount, nodeVersion` (literal key); `nodeVersion` value = `runtime.Version()`.
   - `summary.tiers` populated for T1..T10; `summary.thresholds` holds the five gated rows per spec §6.
   - Filename: `reports/results/{modelSlug}_{iso}.json`; slug collapse + iso format per AC.
   - Indent 2 spaces. No custom marshaller; struct tag order controls field order.
   - Go-specific metadata (runId, adapter, seed, tokenizerMode) NEVER written into the scorecard; goes to `manifest.json` only.
6. Encode all 31 queries + regex rules in `scenarios/tier1/T1-exec.yaml` through `T10-dependency.yaml`, including partial-credit rules. System prompt at `scenarios/tier1/system-prompt.md`. Tool defs at `scenarios/shared/tools/resolver-tools.yaml`.
7. Exit-code wiring: 5 gated checks per spec §6; exit `0` on all pass, `1` on any fail, `2` on uncaught error.
8. Produce `golden/scorecard_example.json` derived from spec §7 example plus a `golden/canned-responses.json` replay file that deterministically reproduces the golden when `resolver --replay golden/canned-responses.json --tier 1` runs.

### Phase 3 — Tier 2 Foundations (populate, don't extend)
**Files:** `internal/runner/multiturn.go`, `internal/adapter/mock_tools.go`, `fixtures/docs/*`, `fixtures/graph/*`, `scenarios/tier2-multiturn/progressive-context.yaml`.

1. **No schema change** — `Turns`, `AvailableTools`, `Fixtures`, `ContextGrowthProfile` are already in the struct from Phase 1. Phase 3 fills them in scenarios.
2. `internal/runner/multiturn.go` — maintains `[]Message`. After each model response, for every tool call: if scenario has a scripted response for that tool/call-signature, inject a `tool` role message; otherwise echo a structured error.
3. `internal/adapter/mock_tools.go` — registry mapping `read_document` / `web_search` / `fetch_api` to fixture-path → content-loader. Fixtures loaded lazily; token-count returned alongside content.
4. `fixtures/docs/` — hand-selected corpus, each file with YAML front-matter `id`, `source_note`, `tokens_estimate`, `suitable_for`. Minimum 8 docs spanning ~500 to ~40K tokens for v1.
5. `fixtures/graph/` — JSON snippets keyed by query pattern so `graph_query` mocks can return realistic responses.
6. Per-turn metric collection extended per AC.
7. Ship `scenarios/tier2-multiturn/progressive-context.yaml`.

### Phase 4 — Sweeps A + B
**Files:** `internal/runner/sweep.go`, `internal/report/csv.go`, `internal/gate/gate.go`, `internal/decoys/decoys.go`, `scenarios/tier2-sweeps/*.yaml`, `contrib/gates/tool-count.yaml`, `contrib/gates/context-size.yaml`.

1. `internal/decoys/decoys.go` — static list of ≥400 plausible-but-irrelevant tool names + one-line fake descriptions; generator picks N deterministically by seed.
2. `internal/runner/sweep.go` — axis × seed grid. `--parallel` enables worker pool sized to `min(runtime.NumCPU(), n)`.
3. Context assembler for Sweep B — reads scenario's `fixtures:` list and `needle:` (position + `match_regex`); constructs a multi-turn script where the agent's first `read_document` call receives concatenated fixture content sized to the axis token budget, with the needle content placed at the declared index.
4. `internal/report/csv.go` — streaming CSV writer with header row per sweep kind.
5. `internal/gate/gate.go` — parses `--gate policy.yaml`; schema: list of rules `{metric, operator, threshold, axis_filter?}`. Emits per-rule PASS/FAIL plus overall. Non-zero exit on any failed gate unless `--gate-report-only`.
6. `contrib/gates/tool-count.yaml` + `contrib/gates/context-size.yaml` ship with example rules and explanatory comments.
7. Scenarios: `scenarios/tier2-sweeps/tool-count.yaml` and `context-size.yaml` ship with default axes + needle examples.

### Phase 5 — Reporting & Polish
**Files:** `internal/tokenizer/tokenizer.go`, `internal/manifest/manifest.go`, `contrib/example.service`, `contrib/example.cron`, README.

1. `internal/tokenizer/tokenizer.go` — **Primary:** `github.com/sugarme/tokenizer` loading a bundled Qwen `tokenizer.json` (vendored under `internal/tokenizer/data/qwen-tokenizer.json`). **Fallback:** word-count × 1.33 heuristic. Manifest records `tokenizerMode: "qwen-bpe" | "heuristic"`; heuristic triggers loud stderr warning. If bundling the Qwen tokenizer blows up binary size past ~50 MB or introduces CGO, fall back to heuristic-only for v1 and note in manifest as `tokenizerMode: "heuristic"` with `approximate: true` on every token count.
2. `internal/manifest/manifest.go` — one manifest per invocation; `runId` = ULID; schema per AC. Golden fixture asserts stability.
3. `contrib/example.service` (systemd) running `resolver --tier 1 --endpoint ... --model gresh-general` nightly, piping scorecard into a retention dir.
4. README — install / usage / interpretation (with worked scorecard excerpt) / gate authoring / v1→v2 roadmap / principle #5 caveat / HTTP-auth note.

### Phase 6 — Verification
**Files:** `internal/**/*_test.go`, `golden/*.json`.

1. Table-driven unit tests: fallback parser (≥7 inputs inc. malformed), each spec §5 regex + partial rule (3 cases: correct/partial/incorrect), scenario loader (valid + invalid), gate evaluator, needle verdict, manifest shape.
2. Golden tests: byte-compare scorecard output against `golden/scorecard_example.json`; byte-compare manifest shape against `golden/manifest_example.json` (field presence only; `runId`/timestamps redacted with a regex normalizer).
3. Smoke test (gated on `$RESOLVER_SMOKE=1` env var): `resolver --tier 1 --model gresh-general` end-to-end against the llm-proxy.
4. Replay test: `resolver --replay golden/canned-responses.json --tier 1 > /tmp/out.json && diff /tmp/out.json golden/scorecard_example.json` is empty.

---

## Risks and Mitigations

| # | Risk | Likelihood | Impact | Mitigation |
|---|------|-----------|--------|------------|
| 1 | Endpoint returns `tool_calls` with schema drift (extra nesting, stringified args) | Medium | High | JSON-or-string parsing branch; fallback text parser per spec §9; golden fixtures for 3 real-world responses committed to `golden/endpoint_samples/` |
| 2 | Qwen tokenizer bundling blows up binary size, fails to load on target platform, or requires CGO | Medium | Medium | Primary `github.com/sugarme/tokenizer` + vendored `qwen-tokenizer.json`; hard fallback to heuristic with `tokenizerMode: "heuristic"` logged in manifest and loud stderr. If binary size exceeds 50 MB or CGO is needed, ship heuristic-only and document in README. Token counts flagged `approximate: true` in heuristic mode. |
| 3 | Fixture pool too small → Sweep B curve is noisy / unreproducible | Medium | Medium | Document minimum pool size per axis; record fixture-set `sha256` in manifest; synthetic generator is explicit v2 escape hatch |
| 4 | Scorecard JSON drifts from spec §7 (trailing newline, key ordering, numeric precision, key renames) | Low | High | Byte-exact golden test; `nodeVersion` literal key retained with Go `runtime.Version()` as value; `encoding/json` default marshaler; no custom MarshalJSON; struct tag order fixed |
| 5 | Gate policy too permissive or too strict → false PASS/FAIL | Medium | Medium | Ship `contrib/gates/*.yaml` with rationale comments; `--gate-report-only` for dry-runs; gate failures print offending axis point + metric value |
| 6 | Parallel sweep seeds collide on same llm-proxy backend → rate-limit / queue artifacts distort timings | High at `-n ≥ 4` | Medium | `--parallel` off by default; warn when combined with sweeps; manifest flags `parallel: true` and records worker count |
| 7 | `go install` binary can't find scenarios/fixtures | High | Low | Embed `scenarios/` and `fixtures/` via `embed.FS`; CLI flag `--data-dir` overrides with external dir |
| 8 | System prompt copy drift from spec §3 | Low | High | Byte-exact embed; unit test asserts `sha256(system-prompt.md) == <pinned constant>` |
| 9 | Manifest schema mutates across runs → downstream tooling breaks | Medium | Medium | Manifest golden test; bumping schema requires explicit `manifestVersion` field increment documented in CHANGELOG |
| 10 | User reverses Round 3 ("Tier 1 only in v1") → Options A/B/C stale | Low | High | Option D documented as user-gated reversal path; ADR follow-up lists the minimal diff to Phase-drop Tier 2 if Round 3 flips |

---

## Verification Steps

Runnable in order. Each gates the next phase.

1. `go vet ./... && go test ./... -count=1` — unit tests including fallback parser, partial rules, regex verdicts, gate evaluator, golden scorecard + manifest shape, needle verdict.
2. `resolver --scenario scenarios/tier1/T1-exec.yaml --dry-run` — lists 5 queries, expected tool, validation rule without hitting network.
3. Replay golden: `resolver --replay golden/canned-responses.json --tier 1 -o /tmp/scorecard.json && diff /tmp/scorecard.json golden/scorecard_example.json` is empty (byte-exact).
4. Tier 1 smoke (gated on `RESOLVER_SMOKE=1`): `RESOLVER_SMOKE=1 resolver --tier 1 --endpoint http://localhost:4000/v1/chat/completions --model gresh-general` finishes, emits scorecard under `reports/results/{modelSlug}_{iso}.json`, exits 0 with all five gated thresholds PASS.
5. Multi-turn smoke: `resolver --scenario scenarios/tier2-multiturn/progressive-context.yaml --model gresh-general` completes three turns, per-turn metrics captured, verdict recorded.
6. Sweep A smoke: `resolver --sweep tool-count --axis "5,20" --model gresh-general -n 2 --gate contrib/gates/tool-count.yaml` emits `reports/sweeps/*.csv` and gate verdict.
7. Sweep B smoke: `resolver --sweep context-size --axis "5000,40000" --model gresh-general -n 2 --gate contrib/gates/context-size.yaml` emits a CSV with `needle_found` per row.

---

## ADR — Architectural Decision Record

**Decision:** Build a Go 1.22+ single-binary test harness at `/home/code/hacking/resolver/` that ports the 31-query resolver-validation spec as Tier 1 with byte-exact scorecard parity, and extends it with multi-turn scenarios, mocked data-producing tools, tool-count sweep (A), and context-size sweep (B) in v1. Only the `openai-chat` adapter ships. Scenario schema is unified in Phase 1 so Tier 1 is a degenerate 1-turn form of the Tier 2 schema. Verdicts are pattern-based; no LLM judge in v1. Module path `github.com/wentbackward/resolver`.

**Drivers:**
1. Time-to-useful-sweep — the tool-count + context-size curves are the headline deliverable.
2. Byte-exact spec parity — scorecard `meta` keys, filename format, exit codes locked to `RESOLVER-VALIDATION-SPEC.md` §7–§8 so historical runs stay comparable indefinitely.
3. Extensibility — adapter + verdict interfaces stable enough that Anthropic / openclaw / hf-serverless slot in later without scenario rewrites.

**Alternatives considered:**
- **Option B — multi-turn-first.** Rejected: no Tier 1 regression baseline during Tier 2 development. Option B's one valid insight (unified schema from day one) was adopted as principle #5, so the v1 plan eliminates the steelman without adopting the option.
- **Option C — parallel tracks.** Rejected: single-operator context; guarantees interface churn.
- **Option D — Tier 1 parity only in v1, sweeps in v1.1.** Rejected *by the user* at deep-interview Round 3, not on technical grounds. Documented as the canonical reversal path if scope pressure demands it.
- **Option E — LLM-as-judge in v1.** Rejected: violates principle #4 (verdict determinism); deferred per deep-interview Round 3.

**Why chosen:** Option A is the only path that (a) produces a working regression baseline early, (b) ships the user's headline deliverable within v1, and (c) adopts Option B's valid insight as a principle rather than a scope item. Reviewer consensus (Architect REVISE-then-APPROVE, Critic APPROVE-WITH-IMPROVEMENTS) reached after folding in schema unification, meta/manifest separation, per-tier gate-row split, partial-rule encoding, and Option D documentation.

**Consequences:**
- Positive: byte-exact Tier 1 parity is a testable CI gate. Tier 2 multi-turn and sweep work sits on a runner that has already been proven. Adapter interface is forward-looking (v2 `Turn()` split is noted in code).
- Negative: Phase 1 is heavier than strictly needed — scenario schema must carry Tier 2 fields even though only Phase 3 populates them. Manifest + scorecard are two files per run instead of one. Qwen tokenizer bundling has a fallback path that will downgrade token-counting quality silently unless users watch the manifest.
- Known v1 limitations: principle #5 (framework-agnostic scenario format) is partially aspirational — scenario YAML declares OpenAI `tools` shape directly; true adapter-agnostic abstraction is v2.

**Follow-ups (v1.1 and v2):**
- v1.1: synthetic fixture generator; `explosive` context growth profile; gate-policy CI preset library; tokenizer upgrade if `sugarme/tokenizer` + Qwen bundling proves shaky.
- v2: Anthropic, openclaw, hf-serverless adapters (triggers adapter interface split into `Turn()` + `Execute()`); HITL approval flows; LLM-as-judge verdicts; cross-adapter consistency reports; sweeps C (growth pattern), D (tool diversity), E (HITL frequency); adapter-agnostic scenario abstraction layer.
- Reversal: if user flips Round 3 to Option D (Tier 1 only), the minimal-diff action is to drop Phase 3–4 from the Phase ordering and ship v1 after Phase 2 + Phase 5 (reporting) + Phase 6 (verification subset).

---

## Changelog

- **v1 (Planner draft, iteration 1):** initial plan derived from deep-interview spec.
- **v2 (iteration 2, current — CONSENSUS APPROVED):** folded Architect + Critic improvements:
  - Principle #3 restated to "stdlib-plus-yaml-plus-tokenizer" (honest about deps).
  - Principle #5 elevated: unified scenario schema declared in Phase 1; Phase 3 populates, does not extend. Removed the "extend Scenario" step in Phase 3.
  - Added Option D (Tier-1-only v1) and Option E (LLM-judge v1), both rejected with explicit rationale.
  - Scorecard AC split: five separate gated-threshold rows per spec §6; `summary.tiers` includes T1–T10 (informational reported, excluded from gating only); scorecard file path + slug + ISO-timestamp format pinned.
  - Scorecard `meta` keys pinned to spec §7 literal set (`nodeVersion` retained, value = `runtime.Version()`); Go-specific metadata quarantined to sibling `manifest.json`.
  - Partial-credit rules explicitly encoded and tested (T2.2, T7, T8, T9.1, T10.3, etc.).
  - Fallback parser test includes malformed-input negative case.
  - Tier 2 scenario YAML gains explicit `fixtures:` field; loader validates references.
  - Sweep B needle verdict defined precisely (regex on message OR tool-call arg; case-insensitive).
  - `contrib/gates/tool-count.yaml` + `context-size.yaml` ship as reference policies.
  - Tokenizer library pinned (primary: `sugarme/tokenizer` + vendored Qwen json; fallback: heuristic) with explicit bail-out condition.
  - Manifest schema formalized with golden-test AC; includes `goVersion`, `commitSha`, `tokenizerMode`, `manifestVersion`.
  - CLI AC gains `--dry-run`, `--api-key` (stubbed), `--replay`; precedence doc added.
  - Module path resolved to `github.com/wentbackward/resolver`.
  - README AC now requires worked scorecard excerpt + principle #5 caveat + HTTP-auth note.
  - Risks 9 (manifest drift) and 10 (Round 3 reversal) added.
  - Full ADR section added.
