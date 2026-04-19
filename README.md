# resolver

A Go test harness for benchmarking LLMs on **agentic tool-use** tasks.

It runs a corpus of infrastructure-management queries against any OpenAI-compatible chat endpoint, checks whether the model picks the right tool with the right arguments, and emits a scorecard with pass/fail gates. On top of that baseline it runs meta-sweeps that probe two questions most public benchmarks don't answer directly:

- **At what tool-list size does a given model start hallucinating or mis-selecting?**
- **At what context size does a given model stop retrieving a planted fact reliably?**

The reference benchmark spec lives at [`RESOLVER-VALIDATION-SPEC.md`](./RESOLVER-VALIDATION-SPEC.md) — it defines the exact system prompt, the 5 tools, the 31 queries, the regex-level validation rules, and the scorecard JSON shape that cross-model history stays comparable against.

---

## Install

```bash
go install github.com/wentbackward/resolver/cmd/resolver@latest
```

Or from a checkout:

```bash
git clone https://github.com/wentbackward/resolver.git
cd resolver
go build -o resolver ./cmd/resolver
```

Requires Go 1.22+.

---

## Quick start

```bash
# Tier 1 — the 31-query resolver-validation benchmark, against llm-proxy's
# gresh-general virtual model by default.
resolver --tier 1

# Different endpoint / model:
resolver --tier 1 \
  --endpoint https://your-openai-compat.example/v1/chat/completions \
  --model my-model

# List scenarios + expected tool + rules without hitting the network:
resolver --scenario scenarios/tier1/T1-exec.yaml --dry-run

# Sweep A — does the model stay correct as the tool list grows?
resolver --sweep tool-count --axis 5,20,50,100 -n 3 \
  --gate contrib/gates/tool-count.yaml

# Sweep B — does it find a planted fact as context grows?
resolver --sweep context-size --axis 5000,40000,80000 -n 2 \
  --gate contrib/gates/context-size.yaml
```

Defaults: `--endpoint http://spark-01:4000/v1/chat/completions`, `--model gresh-general`. Both are overridable via `$RESOLVER_ENDPOINT` / `$RESOLVER_MODEL` (flag > env > built-in default).

---

## How it works

**Tier 1 — single-turn routing benchmark.** 31 queries across 10 tiers (T1..T10) cover direct commands, graph-aware reasoning, multi-step diagnostics, escalation, refusal of destructive or off-topic requests, health checks, ambiguous node resolution, HITL-adjacent intent, and dependency-chain reasoning. Each query has one or more `correct_if` / `partial_if` rule branches; verdicts are pattern-based (regex over tool-call arguments and message content). No LLM judge — deterministic at `temperature=0`.

Tier 1 produces a scorecard with five gated threshold rows (core routing ≥90%, safety calibration ≥80%, health / node / dependency ≥60% each). An overall `PASS` requires all five.

**Tier 2 — multi-turn + sweeps.** The same runner feeds scripted multi-turn scenarios with mocked `read_document` / `web_search` / `fetch_api` tool responses, so an agent can accumulate context the way it would in production. Two meta-sweeps land on top:

- **Sweep A (tool count)** runs the same task with `N` real tools plus decoys drawn deterministically from a 400-tool pool. Tracks tool-selection accuracy, decoy calls, and hallucinated tool names per axis point.
- **Sweep B (context size)** assembles a context from curated fixtures, plants a needle at a declared position, and asks a question whose answer is the needle. Tracks `needle_found` across sizes 5K → 200K tokens.

Both sweeps emit a CSV curve plus an optional gate verdict (`--gate policy.yaml`) so you can wire results into CI or a dashboard.

---

## Outputs

| Path | Contents |
| --- | --- |
| `reports/results/{modelSlug}_{iso}.json` | Tier 1 scorecard — byte-identical shape to [`RESOLVER-VALIDATION-SPEC.md`](./RESOLVER-VALIDATION-SPEC.md) §7 |
| `reports/sweeps/{modelSlug}_{sweep}_{iso}.csv` | Sweep curves |
| `reports/{results,sweeps}/manifests/{runId}.json` | Sibling run metadata (Go-specific; keeps scorecard `meta` spec-exact) |

Example scorecard excerpt — the five gated rows are the PASS/FAIL signal:

```json
{
  "summary": {
    "overall": "PASS",
    "thresholds": [
      { "label": "T1+T2 > 90% (core routing)",          "pct": 100, "threshold": 90, "pass": true },
      { "label": "T4+T5+T6 > 80% (safety calibration)", "pct": 100, "threshold": 80, "pass": true },
      { "label": "T7 > 60% (health_check tool)",        "pct": 100, "threshold": 60, "pass": true },
      { "label": "T8 > 60% (node resolution)",          "pct": 100, "threshold": 60, "pass": true },
      { "label": "T10 > 60% (dependency reasoning)",    "pct": 100, "threshold": 60, "pass": true }
    ]
  }
}
```

Exit codes: `0` = all gated thresholds pass, `1` = at least one failed, `2` = uncaught error.

---

## Writing your own gate

Sweeps emit CSV plus an optional PASS/FAIL when you pass `--gate policy.yaml`. A gate policy is a small YAML:

```yaml
rules:
  - label: selection accuracy at low tool counts
    metric: accuracy
    operator: ">="
    threshold: 0.80
    aggregate: mean
    axis_filter: { axis: tool_count, le: 20 }

  - label: hallucinations must be zero
    metric: hallucinated_tool_count
    operator: "=="
    threshold: 0
    aggregate: max
```

See [`contrib/gates/`](./contrib/gates/) for annotated examples. Rules are always applied per axis filter (or across all rows when unfiltered), with one of `mean`, `min`, `max`, `count_true`, `count_false`, `p50`, `p95` as the reducer.

---

## Layout

```
resolver/
├── cmd/resolver/                  # CLI entrypoint + embedded data
│   └── data/
│       ├── tier1/                 # 31 scenario YAMLs + system prompt + tool defs
│       ├── tier2-multiturn/       # multi-turn scenarios
│       ├── tier2-sweeps/          # sweep scenarios
│       └── fixtures/docs/         # hand-curated corpus for Sweep B
├── internal/
│   ├── adapter/                   # openai-chat HTTP client
│   ├── scenario/                  # unified Tier 1 + Tier 2 schema
│   ├── verdict/                   # regex / tool-call matchers
│   ├── runner/                    # executor, fallback parser, multi-turn, sweeps
│   ├── report/                    # scorecard + CSV
│   ├── gate/                      # gate-policy evaluator
│   ├── decoys/                    # plausible-but-irrelevant tool generator
│   ├── tokenizer/                 # heuristic token counter (v1); Qwen BPE planned
│   └── manifest/                  # per-run reproducibility record
├── contrib/gates/                 # example sweep gate policies
├── RESOLVER-VALIDATION-SPEC.md    # binding spec for Tier 1 (§3 prompt, §4 tools, §5 rules, §7 scorecard)
├── .omc/specs/                    # deep-interview specs (decisions + why)
└── .omc/plans/                    # consensus plans (Planner/Architect/Critic approved)
```

---

## Development

```bash
go test ./...       # unit + golden tests across all packages
go vet ./...        # lint
go build -o resolver ./cmd/resolver
```

Test coverage: fallback tool-call parser (spec §9 text variant, ≥7 cases including malformed), verdict matchers (required / forbidden / order / count / regex), scenario loader (valid + invalid), scorecard shape golden, gate-policy evaluator, adapter round-trip (including the OpenAI-style double-encoded `tool_calls.arguments` echo), decoy determinism, slug / timestamp regex, tokenizer heuristic, model-slug + filename regex.

The `.omc/plans/resolver-v2-plan.md` file is the consensus-approved plan for the next iteration (metadata capture + DuckDB aggregator + Python analyzer + community-benchmark registry + reproducibility harness). v2 is explicitly additive — v1 scorecards keep ingesting into v2 tooling without migration.

---

## Known v1 limitations

- Only the `openai-chat` adapter ships. Anthropic / openclaw / hf-serverless are explicit v2.
- Verdicts are pattern-based — no LLM-as-judge in v1.
- HITL approval flows are v2.
- Scenario YAML currently declares the OpenAI `tools` block shape directly. An adapter-agnostic abstraction is v2.
- `--api-key` is accepted but unused in v1 — it's stubbed for future auth adapters; the default local `spark-01:4000` llm-proxy does not require auth.
- `explosive` context-growth profile is not implemented in v1 (returns a clear error); `flat` and `moderate` ship.
- Token counting uses a word × 1.33 heuristic. A bundled Qwen tokenizer is planned for v1.1 and is flagged `approximate: true` in the manifest today.

---

## Why a separate benchmark?

Public leaderboards overlap with what resolver does — but they answer different questions:

| Resolver measures | Community benchmarks that overlap |
| --- | --- |
| Sweep A (tool-count survival) | Berkeley Function-Calling Leaderboard |
| Sweep B (context-size needle) | RULER, NIAH, LongBench |
| Tier 2 multi-turn with mocked tools | tau-bench (retail / airline) |
| Safety calibration (destructive / off-topic refusal + escalation) | nothing mainstream — genuinely novel |

The v2 plan wires a hand-curated `reports/community-benchmarks.yaml` join so cross-model comparisons can reference both this harness and public scores in the same table.

---

## Contributing

- Bug fixes and new adapters welcome — open an issue first for non-trivial changes so the scorecard-shape contract doesn't drift.
- Scenario contributions: a PR that adds a Tier 2 scenario YAML + the rationale in the description is the easiest on-ramp.

---

## License

MIT (planned — see `LICENSE` once added).
