# resolver-analyze

Cross-run analyzer for [resolver](../../README.md) benchmark data. Reads
the DuckDB file produced by `resolver aggregate` (tagged build),
optionally joins the community-benchmarks YAML, and asks a reporter LLM
to author an opinionated Markdown report comparing the models.

For most users the one-command entry point is [`scripts/report.sh`](../../scripts/report.sh)
at the repo root — venv + build + aggregate + Jupyter in a single
command. See the [top-level README](../../README.md#v2-comparing-models)
for usage and the SSH-tunnel recipe. The sections below cover the raw
CLI for power users and scripting.

**Principle #3 of the [v2 plan](../../.omc/plans/resolver-v2-plan.md):**
Go produces, Python consumes. This package never writes under
`reports/` — all analyzer output lives under `tools/analyze/out/`.

## Install

```bash
pip install -e tools/analyze        # editable install
# or with notebook + test extras:
pip install -e 'tools/analyze[notebook,test]'
```

Requires Python 3.10+. The `tests` extra brings `pytest` + `respx` (httpx
mock transport), the `notebook` extra brings Jupyter.

## Usage

```bash
# Produce + read a DuckDB first with the tagged Go binary:
resolver aggregate --db reports/resolver.duckdb

# Then either render + POST to the reporter LLM...
analyze report --db reports/resolver.duckdb

# ...or preview the prompt + data without any LLM call:
analyze report --db reports/resolver.duckdb --dry-run

# Override the reporter model / endpoint:
analyze report --reporter-model gresh-qwen-huge \
               --endpoint http://spark-01:4000/v1/chat/completions
```

### Ad-hoc SQL

```bash
analyze query reports/resolver.duckdb \
  "SELECT model, overall, correct_count, query_count FROM runs"
```

## Layout

```
tools/analyze/
├── pyproject.toml
├── src/analyze/
│   ├── __init__.py
│   ├── cli.py         # Typer CLI (`analyze report`, `analyze query`)
│   ├── db.py          # DuckDB connector + fixed query set
│   └── report.py      # Jinja render + LLM call + Markdown writer
├── tests/
│   └── test_*.py      # respx-mocked LLM tests, no live network
└── notebooks/
    ├── quickstart.ipynb
    └── reproducibility.ipynb
```

The prompt template lives at
[`tools/analyze/prompts/compare-models.md`](prompts/compare-models.md)
(version-controlled so "opinionated" is inspectable + tunable without
code changes).

## Reporter-vs-model-under-test guard

If the reporter model coincides with any `model` in the dataset, the
analyzer emits a stderr warning before the call — the benchmark's
purpose is undermined if a model grades its own run. Override with
`--reporter-model <different-one>`.

## Tests

```bash
pytest tools/analyze/tests
```

No live network: httpx.MockTransport via `respx` intercepts the reporter
LLM call. The fixture DuckDB is built in-memory from a small Go-emitted
sample.
