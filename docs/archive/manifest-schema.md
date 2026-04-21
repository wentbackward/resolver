# Manifest schema

Every `resolver` run writes a sibling `manifest.json` alongside the scorecard so the scorecard can stay byte-identical to [`RESOLVER-VALIDATION-SPEC.md`](../RESOLVER-VALIDATION-SPEC.md) §7 while still carrying the Go-specific reproducibility metadata that matters for cross-run comparisons.

## Field reference

| Field | Type | Required | Notes |
|---|---|---|---|
| `manifestVersion` | int | yes | `1` pre-v2; `2` v2; `3` v2.1 (role-organised) |
| `runId` | string | yes | ULID-ish `{yyyymmddThhmmss}-{hex8}`; sortable by time |
| `model` | string | yes | Virtual model name sent in the chat request |
| `resolvedRealModel` | string | no | Backing model id from `/v1/models` probe; `"unknown"` if the probe failed |
| `adapter` | string | yes | `"openai-chat"` for v1 |
| `tokenizerMode` | string | yes | `"heuristic"` or `"qwen-bpe"` |
| `endpoint` | string | yes | Full URL sent the chat request |
| `tier` | string | optional | `"1"` or `"2"` when set; archival-only in v3 |
| `role` | string | optional (v3+) | v2.1 role bucket (`agentic-toolcall`, `reducer-json`, …) |
| `promptRev` | string | optional (v3+) | First 12 hex chars of sha256 over the composed system prompt (shared preamble + role body) for prompt-content reproducibility |
| `sweep` | string | optional | `"tool-count"` / `"context-size"` in sweep mode |
| `seeds` | int[] | optional | Seeds used in sweep mode |
| `parallel` | bool | yes | Whether sweep seeds ran in parallel |
| `scenarioHashes` | map[string]string | yes | sha256 of each scenario YAML at run time |
| `startedAt` / `finishedAt` | RFC3339 UTC | yes | Wall-clock bounds of the run |
| `goVersion` | string | yes | `runtime.Version()` of the binary |
| `commitSha` | string | yes | `git rev-parse HEAD` or `"unknown"` |
| `hostName` | string | no | Captured best-effort at write time |
| `runConfig` | object | no (v2+) | Stack-state sidecar (see below) |

## `runConfig` sidecar (v2)

An optional block describing the **stack behind** the run — both the llm-proxy route (clamps, sampling defaults) and the underlying engine recipe (vLLM flags, quantization, MTP, parsers). Loaded from a YAML file passed as `--run-config PATH` and embedded verbatim into the manifest.

Every field is optional. **Unknown values stay unset** (the JSON key is omitted) — the schema deliberately does not hallucinate. For remote-hosted models (HuggingFace serverless, Anthropic, etc.) where engine-level state is not queryable, leave those fields out or record them as the literal string `"unknown"`.

| Group | Fields |
|---|---|
| Backing model | `real_model`, `backend_port`, `backend` |
| Proxy route defaults | `default_temperature`, `default_top_p`, `default_top_k`, `default_min_p` (v3+; JSON key `defaultMinP`), `default_presence_penalty`, `default_frequency_penalty`, `default_max_tokens`, `default_enable_thinking`, `clamp_enable_thinking` |
| vLLM engine | `container`, `tensor_parallel`, `gpu_memory_utilization`, `context_size`, `max_num_batched_tokens`, `kv_cache_dtype`, `attention_backend`, `prefix_caching`, `enable_auto_tool_choice`, `tool_parser`, `reasoning_parser`, `chat_template`, `mtp`, `mtp_method`, `mtp_num_speculative_tokens`, `load_format`, `quantization` |
| Capture meta | `virtual_model`, `captured_at`, `proxy_recipe_path`, `vllm_recipe_path`, `repeat_group`, `notes` |

### Example sidecar

See [`testdata/run-config-example.yaml`](../testdata/run-config-example.yaml) for a worked example. Real sidecars already live under `research/captures/*/*/run-config.yaml`.

## Do not put secrets in the sidecar

The sidecar is captured verbatim into `manifest.json`, which is committable. Bearer tokens, API keys, or private URLs do not belong here. The harness accepts a `--api-key` flag for that; it is never recorded in the manifest.

## Version compatibility

- **v1 → v2/v3 read path**: a v1-shaped manifest (`manifestVersion=1`, no `runConfig`) unmarshals cleanly into the current struct; missing fields stay at their zero value.
- **v2 → v3 read path**: a v2 manifest (`runConfig` present, no `role`/`promptRev`) unmarshals cleanly; the new fields stay empty and are omitted when the manifest is re-written.
- **v3 write path**: v2.1 runs stamp `role`, optional `promptRev`, and — for gresh-reasoner-style clamps — `runConfig.defaultMinP`. `tier` is archival-only and no longer written by the live harness; it remains in the struct so archived v1/v2 manifests keep round-tripping.
- **Aggregator policy** (see `.omc/plans/resolver-v2-1-plan.md` §Phase 5): the v2.1 aggregator ingests only `manifestVersion == 3`. v1/v2 captures live under `research/captures-v1/` and are read forensically, not aggregated.
- **Forward-compat**: consumers should warn on unknown `manifestVersion` and continue ingesting the known subset of fields rather than failing.
