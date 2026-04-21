# resolver

[![Go](https://github.com/wentbackward/resolver/actions/workflows/go.yml/badge.svg)](https://github.com/wentbackward/resolver/actions/workflows/go.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wentbackward/resolver.svg)](https://pkg.go.dev/github.com/wentbackward/resolver)
[![Go Report Card](https://goreportcard.com/badge/github.com/wentbackward/resolver)](https://goreportcard.com/report/github.com/wentbackward/resolver)
[![Go 1.22+](https://img.shields.io/badge/go-1.22%2B-blue)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

**A harness for comparing how LLMs behave on *your* agentic workload —
across models, across parameter settings, against criteria you write.**

Public leaderboards tell you averages over generic prompts. They don't
tell you what serving parameters were used, and they don't tell you how
a model will do on the prompts *you* care about. resolver is the piece
in between: a small, reproducible harness that runs a role-organised
corpus against any OpenAI-compatible endpoint and emits a per-capability
scorecard you can compare side-by-side.

It ships with a sysadm-over-SSH corpus as a working example — 44
scenarios, 12 capability roles, ready to run — but the corpus is just
YAML. Replace the system prompt, tool definitions, and scenarios with
your own domain (clinical, SCADA, customer-service, document review,
whatever) and the same harness tells you which model + parameter combo
wins on your problem.

- **Install** → [below](#install)
- **Full benchmark specification** → [RESOLVER-VALIDATION-SPEC.md](./RESOLVER-VALIDATION-SPEC.md)
- **Outputs & layout** → [below](#outputs)

---

## What resolver measures

Twelve **capability roles**. Each role has its own scenario set, its own
threshold, and its own PASS/FAIL verdict. There is no single overall
score — cross-model comparison is a role × model heat-map, so the
signal you get back is *"model A is great at tool selection but falls
over on safety refusal; model B is the inverse."*

| Role | Scenarios | Threshold | What it probes |
|---|--:|---|---|
| `agentic-toolcall` | 13 | 90% | Tool selection, argument correctness, diagnostic chains — does the model pick the right tool with the right arguments? |
| `safety-refuse` | 5 | 100% | Refuses destructive (`rm -rf /`) and off-topic (`weather in London`) requests outright. |
| `safety-escalate` | 3 | 80% | Escalates complex multi-step changes (migrations, provisioning) instead of executing directly. |
| `tool-count-survival` | 1 (×axis) | 80% | Stays accurate when surrounded by 5 → 100 decoy tools. Answers *"at what tool-list size does the model start hallucinating?"* |
| `classifier` | 6 | 80% | Label-only intent routing — emits one label, no tool call. |
| `health-check` | 3 | 60% | Prefers the right tool for liveness/health probes. |
| `node-resolution` | 3 | 60% | Resolves an entity name (service) to its location (node) from declared topology. |
| `dep-reasoning` | 3 | 60% | Uses graph queries for cross-entity impact questions. |
| `hitl` | 1 | 60% | Escalates for human confirmation before a destructive-but-actionable op. |
| `multiturn` | 1 | 60% | Accumulates context across turns via mocked tools. |
| `long-context` | 1 (×axis) | 60% | Retrieves a planted needle across growing context windows. Answers *"at what context size does retrieval break down?"* |
| `reducer-json` | 4 | 0.90 parse-validity | Reduces a structured event stream to valid JSON. |
| `reducer-sexp` | 0 (placeholder) | — | Same corpus, S-expression output. No scenarios yet. |

Scenarios are declarative YAML. Validation rules are pattern-based
(regex and tool-call matchers) — no LLM-as-judge — so results are
deterministic at `temperature=0`. Full rules for every scenario live in
[`RESOLVER-VALIDATION-SPEC.md`](./RESOLVER-VALIDATION-SPEC.md) §5.

**Bringing your own domain:** drop YAMLs under
`cmd/resolver/data/roles/<role>/`, edit
`cmd/resolver/data/roles/<role>/system-prompt.md`, and the harness runs
them through the same matcher DSL, the same scoring, the same scorecard
shape. The shipped sysadm corpus is an example — not a lock-in.

---

## Run it

Enter the project shell (builds binaries, sets up a venv, drops you
into a shell with `resolver` + helpers on `PATH`, starts Jupyter in
the background):

```bash
scripts/shell.sh
```

From there, point resolver at any **OpenAI-compatible chat endpoint**:

```bash
# Ollama on localhost
resolver --endpoint http://localhost:11434/v1/chat/completions \
         --model llama3:70b

# LM Studio / LocalAI / vLLM direct / anything speaking /v1/chat/completions
resolver --endpoint http://localhost:1234/v1/chat/completions \
         --model my-local-model

# A commercial provider
resolver --endpoint https://api.openai.com/v1/chat/completions \
         --model gpt-4o-mini --api-key "$OPENAI_API_KEY"
```

For multi-model comparison where you want to vary the serving
parameters (temperature, top-p, reasoning on/off, speculative decoding,
KV-cache dtype, …) alongside the model choice, a router like
[llm-proxy](https://github.com/wentbackward/llm-proxy) is the path of least
resistance: you alias each (model + parameter set) combination as a
named virtual model, then resolver treats them as independent entries
in the heat-map. Ollama's `Modelfile` or vLLM's launch flags work too
— any mechanism that makes the parameter combo addressable by name.

### Run one role

```bash
resolver --role classifier --model my-model
```

### Run the full corpus across several models

```bash
scripts/sweep.sh --models "model-a model-b model-c"
```

Each `(model, role)` pair produces a scorecard + manifest under
`research/captures/<real_model>/<virtual_model>/<role>/`. With `--n 3`
(the default), every scenario runs three times so you can see
scenario-level variance.

### Compare results in Jupyter

The project shell starts Jupyter in the background and prints its URL.
Open `quickstart.ipynb` → **Kernel → Restart & Run All**. You get:

- a **role-coverage heat-map**: rows are (real_model, virtual_model),
  columns are roles, cells are PASS (green) / FAIL (red) / ERROR (amber)
- per-role thresholds and `community_benchmarks` joined in for context
- per-scenario variance so you can tell *"did the model flip between
  runs?"*

Everything in the heat-map is queryable SQL against a DuckDB file at
`reports/resolver.duckdb` — edit any cell, add your own pivots, nothing
hidden behind pandas magic.

Type `exit` in the shell to stop Jupyter and leave.

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

Requires **Go 1.22+**. The DuckDB aggregator used by the project shell
is behind a build tag and needs a C toolchain:

```bash
go build -tags duckdb -o .reporting/resolver-duckdb ./cmd/resolver
```

For the notebook side, install [uv](https://github.com/astral-sh/uv).
`scripts/shell.sh` handles both binaries and the Python venv on first
run.

---

## Outputs

resolver writes JSON. Scorecards, manifests, and sweep CSVs land under
`reports/` (per-run) and `research/captures/` (committed captures from
`scripts/sweep.sh`). The aggregator loads everything into
`reports/resolver.duckdb` for the notebook.

| Path | Contents |
|---|---|
| `reports/results/{modelSlug}_{iso}.json` | Per-role scorecard |
| `reports/results/manifests/{runId}.json` | Sibling manifest — which proxy route, which engine params, which model actually answered |
| `reports/sweeps/{modelSlug}_{sweep}_{iso}.csv` | Sweep curves |
| `research/captures/<real_model>/<virtual>/<role>/` | Committed captures |

Exit codes: `0` = all gated roles PASS, `1` = at least one failed,
`2` = uncaught error. Full scorecard shape and field definitions are in
[`RESOLVER-VALIDATION-SPEC.md`](./RESOLVER-VALIDATION-SPEC.md) §8.

---

## Running on a remote host

SSH-forward the Jupyter port so the notebook server (bound to
`localhost`) is reachable from your browser:

```bash
# On your laptop
ssh -L 8888:localhost:8888 remote-host

# On the remote shell
cd ~/path/to/resolver
scripts/shell.sh
```

---

## Layout

```
resolver/
├── cmd/resolver/
│   └── data/
│       ├── roles/<role>/*.yaml        # scenarios + per-role system prompts
│       ├── shared/gate-thresholds.yaml
│       └── fixtures/docs/             # corpus for long-context + multiturn
├── internal/
│   ├── adapter/                       # openai-chat HTTP client
│   ├── scenario/                      # scenario + matcher schema
│   ├── verdict/                       # matcher evaluator
│   ├── runner/                        # executor, fallback parser, multi-turn, sweeps
│   ├── report/                        # scorecard + CSV
│   ├── gate/                          # sweep-gate policy evaluator
│   ├── decoys/                        # decoy-tool generator
│   ├── tokenizer/                     # heuristic token counter
│   ├── aggregate/                     # DuckDB ingest (build tag: duckdb)
│   └── manifest/                      # per-run reproducibility record
├── scripts/
│   ├── shell.sh                       # project shell (everyday entry point)
│   ├── sweep.sh                       # run the full role sweep across models
│   └── report.sh                      # Jupyter only, no subshell
├── tools/analyze/                     # Python analyzer + notebooks + prompts
├── contrib/gates/                     # example sweep gate policies
├── research/captures/                 # committed sweep captures
├── RESOLVER-VALIDATION-SPEC.md        # benchmark spec (source of truth)
└── docs/archive/                      # posterity: release notes, ADRs, superseded schemas
```

---

## Development

```bash
go test ./...                # unit + golden tests across all packages
go test -tags duckdb ./...   # adds aggregator tests
go vet ./...
make build                   # pure-Go default build
make build-duckdb            # aggregator build (requires CGO)
```

---

## Citation

If you use resolver in research or engineering work, please cite it.
GitHub auto-generates a "Cite this repository" button from
[`CITATION.cff`](./CITATION.cff); a BibTeX entry:

```bibtex
@software{gresham_resolver_2026,
  author       = {Gresham, Paul},
  title        = {resolver: a Go test harness for benchmarking LLMs on agentic tool-use tasks},
  year         = {2026},
  version      = {0.1.0},
  url          = {https://github.com/wentbackward/resolver},
  organization = {Paul Gresham Advisory LLC},
  license      = {MIT}
}
```

## License

[MIT](./LICENSE) — Copyright (c) 2026 Paul Gresham Advisory LLC.
