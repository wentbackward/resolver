---
id: spark-01-notes.md
source_note: hand-written operator notes for the spark-01 GPU box
tokens_estimate: 700
suitable_for: [sweep-B, multi-turn]
---
# spark-01 operator notes

spark-01 is the DGX Spark box that hosts our primary vLLM instances. It lives in the lab rack (building 3, row A) and runs three vLLM containers today:

- vllm-35b on port 3040 (Qwen/Qwen3.6-35B-A3B-FP8 backing `gresh-general`)
- vllm-8b on port 3046 (a smaller model we use for quick experimentation)
- vllm-4b on port 3047 (cheapest fallback; also used as a warm replica)

The llm-proxy on port 4000 multiplexes between them and exposes virtual models so clients can target capabilities without knowing the real model. Operators routinely rotate the backing weights without downtime by flipping the `real_model` field in the proxy's config.

## Common operational commands

- Restart a vLLM instance: `docker restart vllm-35b` (or vllm-8b / vllm-4b)
- Follow logs: `docker logs -f vllm-35b`
- Check GPU utilization: `nvidia-smi --query-gpu=utilization.gpu,memory.used,memory.total --format=csv`
- Reload the proxy: `docker exec llm-proxy kill -HUP 1`

## Gotchas

The 35B model is memory-heavy; restarting it under heavy load will fail to come back if the VRAM isn't cleared. If a restart stalls for more than 45 seconds, kill the container outright with `docker kill vllm-35b` and let the restart policy bring it up clean.

## Change log

- 2026-03-02: upgraded the 35B backing to Qwen3.6-35B-A3B-FP8.
- 2026-02-14: moved nv-monitor onto the 3040→35B hot path so Prometheus scrapes directly off the inference container.
- 2026-01-20: retired the Llama-3.1 fallback entirely.
