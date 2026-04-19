"""DuckDB connector + fixed query set.

The aggregator (Go, `-tags duckdb`) populates tables:
  runs, queries, sweep_rows, run_config, community_benchmarks,
plus views: comparison, run_summary.

This module exposes typed helpers over those — the analyzer's prompt
template renders from the dataclasses below.
"""
from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Any

import duckdb


@dataclass
class RunSummary:
    run_id: str
    model: str
    resolved_real_model: str | None
    overall: str
    correct: int
    partial: int
    incorrect: int
    errors: int
    total: int
    total_ms: int
    p95_ms: int
    cfg_real_model: str | None
    cfg_thinking: bool | None
    tool_parser: str | None
    mtp: bool | None
    context_size: int | None
    quantization: str | None


@dataclass
class TierPct:
    run_id: str
    tier: str
    correct: int
    partial: int
    incorrect: int
    total: int
    pct: float


@dataclass
class VarianceRow:
    """One row per (repeat_group, scenario_id) showing stddev of the
    normalized score across repeats. See `variance()` below."""

    repeat_group: str
    scenario_id: str
    n_runs: int
    mean_score: float
    stddev_score: float
    all_correct: bool


@dataclass
class CommunityRow:
    model: str
    benchmark: str
    metric: str
    value: float
    source_url: str
    as_of: str


class Store:
    """Thin DuckDB wrapper. All query methods return lists of dataclass
    rows — materialised up front because the typical result sets are
    small (≤100 rows)."""

    def __init__(self, path: str | Path):
        self.path = str(path)
        self._conn: duckdb.DuckDBPyConnection | None = None

    def __enter__(self) -> "Store":
        self._conn = duckdb.connect(self.path, read_only=True)
        return self

    def __exit__(self, *_exc: Any) -> None:
        if self._conn is not None:
            self._conn.close()
            self._conn = None

    @property
    def conn(self) -> duckdb.DuckDBPyConnection:
        assert self._conn is not None, "Store used outside of `with` block"
        return self._conn

    # ---------- fixed query set ----------

    def run_summaries(self) -> list[RunSummary]:
        # Column names track internal/aggregate/schema.go's run_summary view.
        rows = self.conn.execute("""
            SELECT run_id, model, resolved_real_model, overall,
                   correct_count, partial_count, incorrect_count, error_count,
                   query_count,
                   total_ms, p95_ms,
                   cfg_real_model, cfg_thinking, tool_parser, mtp, context_size, quantization
            FROM run_summary
            ORDER BY resolved_real_model NULLS LAST, model, run_id
        """).fetchall()
        return [
            RunSummary(
                run_id=r[0], model=r[1], resolved_real_model=r[2], overall=r[3],
                correct=r[4], partial=r[5], incorrect=r[6], errors=r[7], total=r[8],
                total_ms=r[9], p95_ms=r[10],
                cfg_real_model=r[11], cfg_thinking=r[12], tool_parser=r[13],
                mtp=r[14], context_size=r[15], quantization=r[16],
            )
            for r in rows
        ]

    def tier_pcts(self) -> list[TierPct]:
        rows = self.conn.execute("""
            SELECT run_id, tier,
                   SUM(CASE WHEN score = 'correct'   THEN 1 ELSE 0 END) AS c,
                   SUM(CASE WHEN score = 'partial'   THEN 1 ELSE 0 END) AS p,
                   SUM(CASE WHEN score = 'incorrect' THEN 1 ELSE 0 END) AS i,
                   COUNT(*) AS total,
                   100.0 * (SUM(CASE WHEN score = 'correct' THEN 1.0
                                     WHEN score = 'partial' THEN 0.5
                                     ELSE 0.0 END) / COUNT(*)) AS pct
            FROM queries
            GROUP BY run_id, tier
            ORDER BY run_id, tier
        """).fetchall()
        return [TierPct(r[0], r[1], r[2], r[3], r[4], r[5], r[6]) for r in rows]

    def variance(self) -> list[VarianceRow]:
        """Per-scenario stddev across runs that share a repeat_group.
        Used by the reproducibility notebook and the analyzer report."""
        rows = self.conn.execute("""
            WITH scored AS (
              SELECT rc.repeat_group, q.scenario_id,
                     CASE WHEN q.score = 'correct' THEN 1.0
                          WHEN q.score = 'partial' THEN 0.5
                          ELSE 0.0 END AS numeric_score
              FROM queries q
              JOIN run_config rc ON rc.run_id = q.run_id
              WHERE rc.repeat_group IS NOT NULL AND rc.repeat_group != ''
            )
            SELECT repeat_group, scenario_id,
                   COUNT(*) AS n,
                   AVG(numeric_score) AS mean,
                   COALESCE(STDDEV_SAMP(numeric_score), 0) AS stddev,
                   MIN(numeric_score) = 1.0 AS all_correct
            FROM scored
            GROUP BY repeat_group, scenario_id
            HAVING COUNT(*) > 1
            ORDER BY stddev DESC, scenario_id
        """).fetchall()
        return [VarianceRow(r[0], r[1], r[2], r[3], r[4], bool(r[5])) for r in rows]

    def community_for(self, real_models: list[str]) -> list[CommunityRow]:
        if not real_models:
            return []
        placeholders = ",".join(["?"] * len(real_models))
        rows = self.conn.execute(
            f"""
            SELECT model, benchmark, metric, value, source_url, CAST(as_of AS VARCHAR)
            FROM community_benchmarks
            WHERE model IN ({placeholders})
            ORDER BY model, benchmark, metric
            """,
            real_models,
        ).fetchall()
        return [CommunityRow(*r) for r in rows]


def open_store(path: str | Path) -> Store:
    p = Path(path)
    if not p.exists():
        raise FileNotFoundError(
            f"DuckDB file not found at {p}. Produce one with "
            f"`resolver aggregate` (requires -tags duckdb build)."
        )
    return Store(p)
