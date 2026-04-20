# resolver v1/v2 captures — archived for historical reference

This directory holds every scorecard + manifest + run-config sidecar captured
under the pre-v2.1 (tier-organised) harness. These runs are **no longer
ingested** by the v2.1 aggregator: `internal/aggregate/ingest.go` rejects
manifests with `manifestVersion < 3` via `ErrUnsupportedSchema`. The files
are kept for replay, forensic lookup, and sanity-checking the v2.1 rewrite
against the pre-rewrite numbers.

## Why archived

v2.1 reorganises the test suite from 10 numbered tiers into 13 named roles
(`agentic-toolcall`, `reducer-json`, `safety-refuse`, …). Scorecards emitted
by the new harness carry `summary.roles{}` instead of `summary.tiers{}`;
the thresholds list is keyed by role; there is no top-level `overall`
verdict. The two shapes are not mergeable — hence the physical split
between `research/captures/` (fresh v2.1 captures) and
`research/captures-v1/` (this archive).

## Root-key rename: `summary` → `summary_v2_legacy`

Every scorecard in this archive has been rewritten so its top-level
`summary` key is now `summary_v2_legacy`. This is **intentional**: it
defeats naive `jq '.summary'` queries that would silently return the
wrong shape to a v2.1 tool. Scripts that walk `research/captures*/`
trees must branch on which key is present:

```bash
# Fresh v2.1 captures (summary.roles, per-role verdicts)
jq '.summary.roles | keys' research/captures/<model>/<virt>/scorecard.json

# Archived v1/v2 captures (summary_v2_legacy.tiers, overall verdict)
jq '.summary_v2_legacy.overall' research/captures-v1/<model>/<virt>/scorecard.json
```

A naive `jq '.summary'` against an archived scorecard returns `null` by
design — that is the collision tripwire from plan scenario G.

### One-liner: detect which shape you are holding

```bash
jq 'if .summary.roles? then "v2.1" elif .summary_v2_legacy? then "v1/v2" else "unknown" end' scorecard.json
```

## Replay parity

Scenario YAMLs under `scenarios/` are **copies** of
`cmd/resolver/data/{tier1,tier2-multiturn,tier2-sweeps}/` captured at the
same commit these scorecards were scored against. They let you replay an
archived run even after the live `cmd/resolver/data/` tree is rewritten
by worker-2's T5 scenario migration.

Replay command (requires a resolver binary whose `--replay` path accepts
the v1/v2 replay-record shape; either a pre-v2.1 checkout or a v2.1
binary with preserved read-path compat):

```bash
./resolver \
  --data-dir research/captures-v1/scenarios \
  --replay   research/captures-v1/<model>/<virt>/replay.json \
  --out      /tmp/replay-check
```

## Inventory

| Model directory                               | Virtual runs (scorecard count)                                   |
|-----------------------------------------------|------------------------------------------------------------------|
| `deepseek-ai_DeepSeek-V3.2-Exp/`              | `gresh-deepseek3.2` (1)                                          |
| `google_gemma-4-26B-A4B-it/`                  | `gresh-gemma4-moe` (1)                                           |
| `moonshotai_Kimi-K2.5/`                       | `gresh-kimiK2.5` (1)                                             |
| `openai_gpt-oss-120b/`                        | `gresh-gpt-oss` (1)                                              |
| `Qwen_Qwen3.5-35B-A3B-FP8/`                   | `gresh-nothink-3.5`, `gresh-coder-3.5`, `gresh-general-3.5` (3)  |
| `Qwen_Qwen3.5-397B-A17B/`                     | `gresh-qwen-huge` (1)                                            |
| `Qwen_Qwen3.6-35B-A3B-FP8/`                   | `gresh-coder`, `gresh-nothink`, `gresh-general`, `gresh-creative`, `gresh-reasoner` (5) |
| `Qwen_Qwen3-Coder-480B-A35B-Instruct/`        | `gresh-qwen-coder` (1)                                           |

Total: 8 models × 14 scorecards. `README-original.md` is the pre-archive
README preserved verbatim.

## Known reproducibility gap: gresh-reasoner `min_p`

The gresh-reasoner captures in this archive were scored against an engine
clamp of `min_p = 0.05` that was **not recorded** in the v1/v2 `RunConfig`
sidecar (the `DefaultMinP` field only exists from manifest v3 onward).
Any attempt to reproduce the reasoner numbers from these files must
consult the llm-proxy route config from the commit pinned in the
manifest's `commitSha`, not the `runConfig` sidecar.
