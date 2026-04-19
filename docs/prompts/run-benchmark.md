# AI-orchestrated run protocol

This prompt tells an AI CLI (Claude Code, Codex, Gemini, etc.) how to
run the resolver benchmark against a model served by llm-proxy + vLLM,
producing a properly annotated capture in `research/captures/`.

The procedure is the same for any stack; it's just a template. Where it
says `spark-01` you can substitute your own host. Where it says `llm-proxy`
you can substitute any OpenAI-compatible router you run. Where it says
`vLLM` you can substitute any inference engine whose recipe is
declarative.

---

## Task

Capture a Tier 1 resolver run against the virtual model **`{VIRTUAL_MODEL}`**
on the llm-proxy host **`{HOST}`** (default `spark-01`), emit a
deterministic replay fixture, annotate it with proxy + engine metadata,
and commit the result.

## You have access to

- SSH to `{HOST}` (read-only is sufficient).
- A built `resolver` binary (both default and `-tags duckdb` variants).
- The repo working tree at `/home/code/hacking/resolver` (or wherever it
  lives on your machine — the aggregator commands below assume the repo
  root).

## Steps

### 1. Discover the proxy + engine state

SSH to `{HOST}` and read the llm-proxy config:

```bash
ssh {HOST} 'cat /path/to/llm-proxy/config.yaml'
```

Locate the route for `{VIRTUAL_MODEL}`. Record:

- `real_model` (the backing model path).
- `backend_port`.
- Route `defaults` block (temperature / top_p / top_k / max_tokens /
  enable_thinking / presence_penalty / frequency_penalty — whatever is set).
- Route `clamp` block (which of those the proxy forces regardless of
  client).

Then read the vLLM recipe for the backend:

```bash
ssh {HOST} 'cat /path/to/vllm-recipes/{RECIPE_FILE}'
```

From the recipe, record:

- `container`, `max_model_len` (→ `context_size`), `max_num_batched_tokens`,
  `gpu_memory_utilization`, `tensor_parallel`.
- From the `vllm serve` command line: `--reasoning-parser`,
  `--tool-call-parser` (→ `tool_parser`), `--kv-cache-dtype` (→
  `quantization` inferred; also a separate `kv_cache_dtype` field),
  `--enable-prefix-caching`, `--enable-auto-tool-choice`,
  `--load-format`, `--attention-backend`, `--chat-template`, any
  `--speculative-config` (→ `mtp`, `mtp_method`,
  `mtp_num_speculative_tokens`).

For **HuggingFace-hosted** models (routes with `backend: hf-serverless`):
no recipe exists. Leave engine-level fields unset — the sidecar schema
treats absence as "unknown" per principle #4 of the v2 plan.

### 2. Build the sidecar YAML

Create `/tmp/run-config-{VIRTUAL_MODEL}.yaml` with the captured state.
Template:

```yaml
virtual_model: {VIRTUAL_MODEL}
real_model: {REAL_MODEL}            # Skip for HF-hosted (leave absent)
backend_port: {PORT}                # Skip for HF-hosted
backend: hf-serverless              # only for HF-hosted routes

# Proxy layer
default_temperature: {T}
default_top_p: {P}
default_top_k: {K}                  # optional
default_max_tokens: {MAX}
default_enable_thinking: {BOOL}     # optional
clamp_enable_thinking: {BOOL}       # optional

# Engine layer (skip for HF-hosted)
container: {CONTAINER}
tensor_parallel: {TP}
gpu_memory_utilization: {UTIL}
context_size: {CTX}
max_num_batched_tokens: {BATCH}
kv_cache_dtype: {KV}
attention_backend: {ATTN}
prefix_caching: {BOOL}
enable_auto_tool_choice: {BOOL}
tool_parser: {PARSER}
reasoning_parser: {REASONER}
chat_template: {TPL}
mtp: {BOOL}
mtp_method: {METHOD}
mtp_num_speculative_tokens: {N}
load_format: {FMT}
quantization: {Q}

# Capture meta (always present)
captured_at: {TODAY-YYYY-MM-DD}
proxy_recipe_path: /path/to/llm-proxy/config.yaml@{GIT_SHA_IF_KNOWN}
vllm_recipe_path: /path/to/vllm-recipes/{RECIPE_FILE}@{GIT_SHA_IF_KNOWN}
notes: "{ONE LINE describing the capture}"
```

**Do not put secrets here.** Bearer tokens, API keys, and private URLs
do not belong in the sidecar — the file is captured verbatim into the
manifest, which is committable. The `--api-key` flag is for auth; it is
never recorded.

### 3. Run the benchmark

```bash
cd /home/code/hacking/resolver

resolver --tier 1 \
  --endpoint http://{HOST}:4000/v1/chat/completions \
  --model {VIRTUAL_MODEL} \
  --run-config /tmp/run-config-{VIRTUAL_MODEL}.yaml \
  --emit-replay /tmp/{VIRTUAL_MODEL}.replay.json \
  --out /tmp/{VIRTUAL_MODEL}-capture
```

Expected runtime: ~3 minutes for a local 35B model, up to ~10 for a
large HF-hosted model on cold cache.

Verify the output triplet exists:

- `/tmp/{VIRTUAL_MODEL}-capture/*.json` — the Tier 1 scorecard.
- `/tmp/{VIRTUAL_MODEL}-capture/manifests/*.json` — the manifest with
  `runConfig` embedded.
- `/tmp/{VIRTUAL_MODEL}.replay.json` — deterministic replay envelope.

### 4. Move into `research/captures/`

Derive a real-model slug (replace `/` with `_`):

```bash
REAL_MODEL_SLUG=$(echo "{REAL_MODEL}" | tr / _)
DEST=research/captures/$REAL_MODEL_SLUG/{VIRTUAL_MODEL}

mkdir -p $DEST
mv /tmp/{VIRTUAL_MODEL}-capture/*.json       $DEST/scorecard.json
mv /tmp/{VIRTUAL_MODEL}-capture/manifests    $DEST/manifests
mv /tmp/{VIRTUAL_MODEL}.replay.json           $DEST/replay.json
cp /tmp/run-config-{VIRTUAL_MODEL}.yaml      $DEST/run-config.yaml
```

For HF models (no real_model determined), use the virtual name as both:
`research/captures/{VIRTUAL_MODEL}/{VIRTUAL_MODEL}/`.

### 5. Verify the aggregator ingests it

```bash
resolver aggregate --dry-run
```

The new `runId` should appear in the output.

### 6. Commit as a triplet

```bash
git add research/captures/$REAL_MODEL_SLUG/
git commit -m "Capture: {VIRTUAL_MODEL} against {REAL_MODEL}"
git push
```

## Handling "unknown" fields

If you genuinely can't discover a field (e.g., HF-hosted engine
configuration), **omit** it rather than writing `unknown` as a value.
The schema treats absence as the canonical "unknown" signal. The
aggregator's join still works; the analyzer just sees a NULL for that
column.

## Common mistakes

- **Committing a sidecar with a stale `real_model`.** If the proxy has
  been re-routed since you captured, the sidecar's `real_model` will
  mislead every downstream consumer. Re-derive from the current config
  before the commit, or rerun the capture.
- **Letting the `--out` directory persist across models.** Use a fresh
  temp directory per model so manifests don't collide.
- **Forgetting to commit `run-config.yaml` alongside the scorecard.**
  The manifest embeds the sidecar at run time, so scorecards without
  sidecars lose that provenance in the DuckDB table.
