# resolver

Go test harness for benchmarking LLMs on agentic tool-use tasks. Ports the 31-query resolver-validation spec as a regression baseline (Tier 1) and extends it with multi-turn scenarios plus tool-count and context-size sweeps (Tier 2).

## Install

```
go install github.com/gresham/resolver/cmd/resolver@latest
```

Or from a checkout:

```
go build -o resolver ./cmd/resolver
```

## Quick start

```
# Tier 1 (the 31-query benchmark) — runs against llm-proxy's gresh-general virtual model by default
resolver --tier 1

# Point at a different endpoint / model
resolver --tier 1 --endpoint http://spark-01:4000/v1/chat/completions --model gresh-huge

# Dry-run: list scenarios without hitting the network
resolver --scenario scenarios/tier1/T1-exec.yaml --dry-run

# Replay recorded responses against the runner (for goldens / offline)
resolver --replay golden/canned-responses.json --tier 1

# Sweep the tool count axis
resolver --sweep tool-count --axis 5,20,50 -n 3 \
  --gate contrib/gates/tool-count.yaml

# Sweep context size with a needle-in-haystack check
resolver --sweep context-size --axis 5000,40000,80000 -n 2 \
  --gate contrib/gates/context-size.yaml
```

Defaults: `--endpoint http://spark-01:4000/v1/chat/completions`, `--model gresh-general` (llm-proxy virtual → `Qwen/Qwen3.6-35B-A3B-FP8` @ port 3040). Both overridable via `$RESOLVER_ENDPOINT` and `$RESOLVER_MODEL`; flag wins over env wins over built-in default.

## Outputs

| Path | Contents |
| --- | --- |
| `reports/results/{modelSlug}_{iso}.json` | Tier 1 scorecard (byte-identical shape to `RESOLVER-VALIDATION-SPEC.md` §7) |
| `reports/sweeps/{modelSlug}_{sweep}_{iso}.csv` | Sweep curve per run |
| `reports/{results,sweeps}/manifests/{runId}.json` | Sibling run metadata (Go-specific; keeps scorecard `meta` spec-exact) |

Example Tier 1 scorecard excerpt (the five gated threshold rows are the PASS/FAIL signal):

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

Exit codes: `0` = all gated thresholds pass; `1` = at least one failed; `2` = uncaught error.

## Gate policies

Sweeps emit CSV curves and a PASS/FAIL when you pass `--gate policy.yaml`. See `contrib/gates/*.yaml` for example policies.

## Known v1 limitations

- Only the `openai-chat` adapter ships in v1 (via llm-proxy). Anthropic / openclaw / hf-serverless adapters are explicit v2.
- Verdicts are pattern-based — no LLM-as-judge in v1.
- HITL approval flows are v2.
- Scenario YAML currently declares OpenAI `tools` block shape directly; adapter-agnostic tool abstraction is v2.
- `--api-key` is accepted but unused — it's stubbed for future auth adapters; the default local `spark-01:4000` llm-proxy does not require auth.
- `explosive` context-growth profile is not implemented in v1 (returns a clear error); `flat` and `moderate` ship.

## Roadmap

See `.omc/plans/resolver-harness-v1-plan.md` for the consensus-approved plan and `.omc/specs/deep-interview-resolver-agentic-harness-v1.md` for requirements provenance.

## License

See `LICENSE` (or ask the maintainer).
