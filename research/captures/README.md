# Research captures

Real multi-model scorecards + replay fixtures + per-run config sidecars used
to seed the v2 aggregator (Phase 2) and Python analyzer (Phase 5). These are
**not** the v1 golden regression fixtures — those live in
[`../../golden/`](../../golden/). These are research corpus: real numbers
from real runs against the llm-proxy, useful for developing cross-model
comparison tooling.

## Directory convention

```
research/captures/
└── {real_model_slug}/            ← primary key: what was actually measured
    └── {virtual_model}/          ← disambiguator: routing / sampling clamp variant
        ├── scorecard.json        ← spec §7 shape, deterministic under replay
        ├── replay.json           ← emit-replay envelope; feed back via --replay
        ├── run-config.yaml       ← captured proxy route + vLLM recipe state
        └── manifests/{runId}.json
```

**Why real-model as primary key:** the virtual-model name is an llm-proxy
routing artefact that can be remapped under you — four virtuals can back
onto the same real model (they differ only in sampling clamps), and one
virtual name can be redirected to a different real model tomorrow. The
*backing model* is what the benchmark is actually measuring; the *virtual
alias* only disambiguates runs of the same real model with different params.

**run-config.yaml** is the v2-Phase-1-shape `RunConfig` sidecar pre-authored
for these captures. Once the `/v1/models` probe lands in the harness
(Phase 1), new captures will include this data in their `manifest.json`
natively.

## Current corpus (2026-04-19)

### Local — Qwen3.6-35B-A3B-FP8 on spark-01:3040

All four variants back the same real model; the only behavioural axis is
`enable_thinking` (clamped by the proxy). The harness forces
`temperature=0`, so proxy sampling defaults are captured for completeness
but don't actually influence behaviour.

| Virtual | Thinking | T1+T2 | T4+T5+T6 | T7 | T8 | T10 | Overall |
|---|---|---|---|---|---|---|---|
| gresh-general | on | 94% | 50% | 100% | 83% | 100% | FAIL |
| gresh-coder | on | 83% | 50% | 100% | 83% | 100% | FAIL |
| gresh-creative | off | 100% | 38% | 100% | 83% | 100% | FAIL |
| gresh-nothink | off | 100% | 38% | 100% | 83% | 100% | FAIL |

Pattern: thinking-off wins **core routing** (T1+T2) but loses **safety
calibration** (T4+T5+T6). Same backing weights — the delta is the thinking
toggle.

### Local — Qwen3.5-35B-A3B-FP8 on spark-01:3041

Older Qwen release, served in the `vllm-node` container (vs 3.6's
`vllm-node-tf5`), generic MTP (vs 3.6's `qwen3_next_mtp`).

| Virtual | Thinking | T1+T2 | T4+T5+T6 | T7 | T8 | T10 | Overall |
|---|---|---|---|---|---|---|---|
| gresh-general-3.5 | on | 100% | 88% | 100% | 100% | 100% | **PASS** |
| gresh-coder-3.5 | on | 100% | 88% | 100% | 100% | 100% | **PASS** |
| gresh-nothink-3.5 | off | 89% | 50% | 100% | 83% | 100% | FAIL |

3.5 beats 3.6 on every tier with thinking on. Thinking-off regression is
more pronounced here — a jumping-off point for investigating whether 3.5's
thinking delta is stronger than 3.6's, or whether the container/vLLM
version matters.

### HuggingFace serverless

Engine-level parameters (MTP, tool parser, reasoning parser, context
window, quantization) are not exposed by HF's serving infrastructure —
sidecars mark those fields `unknown` per v2 plan principle #4.

| Real model | Virtual | T1+T2 | T4+T5+T6 | T7 | T8 | T10 | Overall |
|---|---|---|---|---|---|---|---|
| `deepseek-ai/DeepSeek-V3.2-Exp` | gresh-deepseek3.2 | 67% | 50% | 50% | 33% | 100% | FAIL |
| `Qwen/Qwen3.5-397B-A17B` | gresh-qwen-huge | 100% | 75% | 100% | 100% | 100% | FAIL |
| `Qwen/Qwen3-Coder-480B-A35B-Instruct` | gresh-qwen-coder | 72% | 75% | 83% | 50% | 100% | FAIL |
| `moonshotai/Kimi-K2.5` | gresh-kimiK2.5 | 100% | 100% | 100% | 100% | 67% | **PASS** |
| `openai/gpt-oss-120b` | gresh-gpt-oss | 78% | 38% | 83% | 50% | 100% | FAIL |
| `google/gemma-4-26B-A4B-it` | gresh-gemma4-moe | 89% | 100% | 83% | 33% | 100% | FAIL |

Kimi K2.5 is the only HF model that clears all five gates — notable because
it's the only one that passes safety calibration at 100% *and* doesn't
collapse on any other gate. Its sub-threshold score is T10 at 67% (the
floor is 60%), so it's narrowly through.

## Reading individual captures

```bash
# Scorecard (spec §7 shape, byte-identical on replay)
jq .summary research/captures/Qwen_Qwen3.5-35B-A3B-FP8/gresh-general-3.5/scorecard.json

# Which scenarios failed, and why
jq '.results[] | select(.score != "correct") | {id, score, reason}' \
  research/captures/moonshotai_Kimi-K2.5/gresh-kimiK2.5/scorecard.json

# Re-run offline (deterministic)
resolver --tier 1 --replay research/captures/moonshotai_Kimi-K2.5/gresh-kimiK2.5/replay.json
```

## Adding a new capture

1. Run the harness with `--emit-replay`:
   ```bash
   resolver --tier 1 \
     --endpoint http://spark-01:4000/v1/chat/completions \
     --model <virtual> \
     --emit-replay /tmp/replay.json \
     --out /tmp/capture
   ```
2. Identify the backing real model (`grep real_model /tmp/config.yaml`).
3. Move into `research/captures/{real_model_slug}/{virtual}/`:
   ```bash
   DEST=research/captures/Real_Model_Slug/<virtual>
   mkdir -p "$DEST"
   mv /tmp/capture/*.json  "$DEST/scorecard.json"
   mv /tmp/capture/manifests "$DEST/manifests"
   mv /tmp/replay.json "$DEST/replay.json"
   ```
4. Author a `run-config.yaml` describing the proxy clamps + vLLM recipe.
   Use an adjacent capture as a template.
