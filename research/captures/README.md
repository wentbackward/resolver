# Research captures

Real multi-model scorecards + replay fixtures used to seed the v2
aggregator (Phase 2) and Python analyzer (Phase 5). These are NOT
the v1 golden regression fixtures — those live in `../../golden/`.
These are research corpus: real numbers from real runs against the
llm-proxy, useful for developing cross-model comparison tooling.

Each subdir is one model. `.replay.json` is the deterministic replay
envelope; the numbered JSON file is the scorecard the harness emitted.

Initial batch (2026-04-19, all backed by Qwen3.6-35B-A3B-FP8 on
port 3040 of the llm-proxy; sampling temperature forced to 0 by the
harness, so what differs across rows is `enable_thinking` and other
routing-clamped knobs):

| Model | Thinking | T1+T2 | T4+T5+T6 | T8 | Notes |
|---|---|---|---|---|---|
| gresh-general | on | 94% | 50% | 83% | baseline |
| gresh-coder | on | 83% | 50% | 83% | lower top_k |
| gresh-creative | off | 100% | 38% | 83% | highest defaults but clamped off thinking |
| gresh-nothink | off | 100% | 38% | 83% | thinking explicitly disabled |

Pattern: thinking-off wins routing (T1+T2) but loses safety calibration
(T4+T5+T6). Same backing model — the behavioural delta is entirely
`enable_thinking` (since temperature=0 across all runs).

HF-serverless models (gresh-deepseek3.2, gresh-qwen-huge, gresh-qwen-coder,
gresh-kimiK2.5, gresh-gpt-oss, gresh-gemma4-moe) returned 401 "Invalid
username or password" — llm-proxy's `$HF_TOKEN` needs refreshing. Those
captures will land in a follow-up commit.
