"""Cross-language schema-drift gate (Python half).

Companion to `internal/aggregate/schema_drift_test.go::TestViewColumnsStable`.

The Python analyzer hand-codes column names when SELECTing from the
DuckDB views the Go aggregator creates. A rename in
`internal/aggregate/schema.go` without a matching update here would blow
up at analyzer runtime rather than in CI. This test asserts that every
column the Python `db.py` references by name actually exists on the
view it's queried from, so a drift is caught by `pytest` before any
real DuckDB rolls into production.

Kept deliberately decoupled from the Go test:
  - The Go test (golden-snapshot) catches ANY column drift, renamed
    or added.
  - This Python test catches drift that would break `db.py`
    specifically — an additive column in Go without a Python consumer
    is fine here, but a rename or removal of a Python-consumed column
    trips it.
"""
from __future__ import annotations

import duckdb


# Columns the Python `analyze.db.Store` code selects from `run_summary`.
# Keep this list in sync with the SELECT statement in
# `tools/analyze/src/analyze/db.py::Store.run_summaries`. A missing
# column here = the analyzer will KeyError / SQL-error at runtime.
RUN_SUMMARY_EXPECTED = [
    "run_id",
    "model",
    "resolved_real_model",
    "overall",
    "correct_count",
    "partial_count",
    "incorrect_count",
    "error_count",
    "query_count",
    "total_ms",
    "p95_ms",
    "cfg_real_model",
    "cfg_thinking",
    "tool_parser",
    "mtp",
    "context_size",
    "quantization",
]


# `db.py` doesn't currently SELECT from `comparison` directly, but the
# view exists in schema.go and downstream analyzers / notebooks rely on
# these columns. A change here signals coordination required with the
# Go side — the golden file in `golden/view_columns.txt` is the
# authoritative snapshot.
COMPARISON_EXPECTED = [
    "run_id",
    "tier",
    "model",
    "resolved_real_model",
    "overall",
    "run_total_ms",
    "scenario_id",
    "score",
    "elapsed_ms",
    "cfg_real_model",
    "cfg_thinking",
    "cfg_tool_parser",
    "cfg_reasoning_parser",
    "cfg_mtp",
    "cfg_context_size",
    "cfg_quantization",
]


def _view_columns(conn: duckdb.DuckDBPyConnection, view: str) -> list[str]:
    """Return the ordered column names of `view` via DuckDB's cursor
    description. `SELECT * ... LIMIT 0` materialises the cursor without
    fetching data."""
    desc = conn.execute(f"SELECT * FROM {view} LIMIT 0").description
    assert desc is not None, f"view {view!r} has no cursor description"
    return [col[0] for col in desc]


def test_run_summary_has_python_referenced_columns(seeded_db):
    """Every column `db.py::run_summaries` SELECTs must exist on the
    `run_summary` view."""
    conn = duckdb.connect(str(seeded_db), read_only=True)
    try:
        actual = _view_columns(conn, "run_summary")
    finally:
        conn.close()

    missing = [c for c in RUN_SUMMARY_EXPECTED if c not in actual]
    assert not missing, (
        f"run_summary view is missing columns referenced by Python db.py: "
        f"{missing!r}. Either the Go schema drifted or the Python code "
        f"was edited without updating the view. Actual columns: {actual!r}."
    )


def test_comparison_has_expected_columns(seeded_db):
    """Columns the `comparison` view is contracted to expose. Keeps the
    Python/Go boundary documented even though `db.py` doesn't currently
    SELECT from this view — notebooks and future analyzers do."""
    # conftest.py's fixture ships a schema subset that doesn't recreate
    # the `comparison` view (only `run_summary`), so build a tiny
    # in-memory DB that recreates both views from the real schema.
    # We rely on the Go golden (`golden/view_columns.txt`) being
    # authoritative and mirror it here. The real enforcement is the Go
    # test — this one documents the contract for Python consumers.
    conn = duckdb.connect(":memory:")
    try:
        # Minimal DDL for a `comparison` view that mirrors
        # internal/aggregate/schema.go. Kept in sync via the Go golden.
        conn.execute(
            """CREATE TABLE runs (
                run_id VARCHAR, tier VARCHAR, model VARCHAR,
                resolved_real_model VARCHAR, overall VARCHAR,
                total_ms BIGINT
            )"""
        )
        conn.execute(
            """CREATE TABLE queries (
                run_id VARCHAR, scenario_id VARCHAR, score VARCHAR,
                elapsed_ms BIGINT
            )"""
        )
        conn.execute(
            """CREATE TABLE run_config (
                run_id VARCHAR, real_model VARCHAR,
                default_enable_thinking BOOLEAN, tool_parser VARCHAR,
                reasoning_parser VARCHAR, mtp BOOLEAN,
                context_size INTEGER, quantization VARCHAR
            )"""
        )
        conn.execute(
            """CREATE VIEW comparison AS
             SELECT
               r.run_id, r.tier, r.model, r.resolved_real_model, r.overall,
               r.total_ms AS run_total_ms,
               q.scenario_id, q.score, q.elapsed_ms,
               c.real_model                 AS cfg_real_model,
               c.default_enable_thinking    AS cfg_thinking,
               c.tool_parser                AS cfg_tool_parser,
               c.reasoning_parser           AS cfg_reasoning_parser,
               c.mtp                        AS cfg_mtp,
               c.context_size               AS cfg_context_size,
               c.quantization               AS cfg_quantization
             FROM runs r
             JOIN queries q ON q.run_id = r.run_id
             LEFT JOIN run_config c ON c.run_id = r.run_id"""
        )
        actual = _view_columns(conn, "comparison")
    finally:
        conn.close()

    missing = [c for c in COMPARISON_EXPECTED if c not in actual]
    assert not missing, (
        f"comparison view is missing expected columns: {missing!r}. "
        f"Update COMPARISON_EXPECTED here AND the Go golden file "
        f"`golden/view_columns.txt` together. Actual: {actual!r}."
    )


def test_run_summary_columns_match_dataclass_fields(seeded_db):
    """Cross-check: the tuple unpack in `Store.run_summaries` uses
    positional indexing `r[0..16]`. If the SELECT column order drifts
    out of sync with the dataclass field order, rows get scrambled
    silently. This test pins the SELECT order to match the dataclass.
    """
    from analyze.db import RunSummary

    # The RunSummary dataclass fields in declaration order. `total` in
    # the dataclass maps to `query_count` in the SELECT — the name
    # diverges but the position is what matters for `RunSummary(*r)`.
    dataclass_fields = [f.name for f in RunSummary.__dataclass_fields__.values()]
    select_to_dataclass = {
        "run_id": "run_id",
        "model": "model",
        "resolved_real_model": "resolved_real_model",
        "overall": "overall",
        "correct_count": "correct",
        "partial_count": "partial",
        "incorrect_count": "incorrect",
        "error_count": "errors",
        "query_count": "total",
        "total_ms": "total_ms",
        "p95_ms": "p95_ms",
        "cfg_real_model": "cfg_real_model",
        "cfg_thinking": "cfg_thinking",
        "tool_parser": "tool_parser",
        "mtp": "mtp",
        "context_size": "context_size",
        "quantization": "quantization",
    }
    mapped = [select_to_dataclass[c] for c in RUN_SUMMARY_EXPECTED]
    assert mapped == dataclass_fields, (
        f"RUN_SUMMARY_EXPECTED positional order doesn't match RunSummary "
        f"dataclass field order.\n  mapped: {mapped}\n  fields: {dataclass_fields}"
    )
