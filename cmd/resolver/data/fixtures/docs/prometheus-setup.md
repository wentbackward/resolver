---
id: prometheus-setup.md
source_note: the prometheus / grafana stack living on claw
tokens_estimate: 600
suitable_for: [sweep-B, multi-turn]
---
# Prometheus + Grafana on claw

The prometheus instance on claw scrapes three targets today:

- nv-monitor on spark-01 (port 9011) — GPU utilisation, VRAM, power, temperature
- llm-proxy metrics on spark-01 (port 4000 `/metrics`) — latency, token/s, queue depth
- vllm-35b directly on spark-01 (port 3040 `/metrics`) — per-model token throughput

Retention is 30 days, which is the default; change `--storage.tsdb.retention.time` on the compose file if you need longer.

Grafana reads from the same prometheus instance on port 9090. The dashboards live in `grafana/dashboards/` and are version-controlled separately. The "Inference Latency" board is the one oncall watches.

## Scrape gotchas

If vllm-35b restarts, the scrape job will intermittently see 503s for ~20 seconds while the model reloads weights. Prometheus tolerates this but alerts based on `up{}` may fire falsely. The relevant alert has a 2-minute `for:` clause to avoid that.

## Alerts

- `VLLMDown` — vllm-* targets down for >2m.
- `GPUThermal` — GPU temp >80°C for >5m.
- `LLMProxyLatencyP99High` — p99 > 5s over a 5m window.

## Change log

- 2026-02-28: added the direct vllm-35b scrape (previously went through llm-proxy only).
- 2026-01-11: retired the Llama-3.1 scrape job entirely.
