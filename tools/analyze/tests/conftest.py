"""Shared pytest fixtures. Builds a small DuckDB in a tmp_path with the
same schema the Go aggregator produces, pre-populated with a mini cross-
model corpus. No live DB, no live network."""
from __future__ import annotations

from pathlib import Path

import duckdb
import pytest


SCHEMA_DDL = [
    """CREATE TABLE runs (
        run_id VARCHAR PRIMARY KEY,
        scorecard_path VARCHAR, manifest_path VARCHAR,
        tier VARCHAR, sweep VARCHAR, model VARCHAR,
        resolved_real_model VARCHAR, endpoint VARCHAR, adapter VARCHAR,
        tokenizer_mode VARCHAR, manifest_version INTEGER,
        started_at TIMESTAMP, finished_at TIMESTAMP,
        go_version VARCHAR, commit_sha VARCHAR, host_name VARCHAR,
        overall VARCHAR,
        total_ms BIGINT, avg_ms BIGINT, p50_ms BIGINT, p95_ms BIGINT, max_ms BIGINT,
        query_count INTEGER,
        correct_count INTEGER, partial_count INTEGER,
        incorrect_count INTEGER, error_count INTEGER
    )""",
    """CREATE TABLE queries (
        run_id VARCHAR, tier VARCHAR, scenario_id VARCHAR,
        query VARCHAR, expected_tool VARCHAR, score VARCHAR, reason VARCHAR,
        elapsed_ms BIGINT, tool_calls_json VARCHAR, content VARCHAR,
        PRIMARY KEY (run_id, scenario_id)
    )""",
    """CREATE TABLE run_config (
        run_id VARCHAR PRIMARY KEY,
        virtual_model VARCHAR, real_model VARCHAR, backend VARCHAR,
        backend_port INTEGER,
        default_temperature DOUBLE, default_top_p DOUBLE,
        default_top_k INTEGER,
        default_presence_penalty DOUBLE, default_frequency_penalty DOUBLE,
        default_max_tokens INTEGER,
        default_enable_thinking BOOLEAN, clamp_enable_thinking BOOLEAN,
        container VARCHAR, tensor_parallel INTEGER,
        gpu_memory_utilization DOUBLE, context_size INTEGER,
        max_num_batched_tokens INTEGER, kv_cache_dtype VARCHAR,
        attention_backend VARCHAR, prefix_caching BOOLEAN,
        enable_auto_tool_choice BOOLEAN,
        tool_parser VARCHAR, reasoning_parser VARCHAR, chat_template VARCHAR,
        mtp BOOLEAN, mtp_method VARCHAR, mtp_num_speculative_tokens INTEGER,
        load_format VARCHAR, quantization VARCHAR,
        captured_at VARCHAR, proxy_recipe_path VARCHAR,
        vllm_recipe_path VARCHAR, repeat_group VARCHAR, notes VARCHAR
    )""",
    """CREATE TABLE community_benchmarks (
        model VARCHAR, model_key VARCHAR, benchmark VARCHAR, metric VARCHAR,
        value DOUBLE, source_url VARCHAR, as_of DATE, notes VARCHAR,
        PRIMARY KEY (model, benchmark, metric)
    )""",
    # v2.1 role-organised scorecard rollup. One row per (run_id, role).
    """CREATE TABLE role_scorecards (
        run_id                    VARCHAR NOT NULL,
        role                      VARCHAR NOT NULL,
        verdict                   VARCHAR,
        threshold_met             BOOLEAN,
        threshold                 DOUBLE,
        metrics_json              VARCHAR,
        scenario_count_expected   INTEGER,
        scenario_count_observed   INTEGER,
        PRIMARY KEY (run_id, role)
    )""",
    # Mirrors internal/aggregate/schema.go's run_summary view exactly so
    # the Python queries in analyze.db go through the same column names
    # they'd see against a real aggregator-produced DuckDB. v2.1 drops the
    # `overall` column — per-role verdicts live in `role_coverage`.
    """CREATE VIEW run_summary AS
     SELECT r.run_id, r.model, r.resolved_real_model,
            r.correct_count, r.partial_count, r.incorrect_count, r.error_count,
            r.query_count, r.total_ms, r.p95_ms,
            c.real_model AS cfg_real_model, c.default_enable_thinking AS cfg_thinking,
            c.tool_parser, c.mtp, c.context_size, c.quantization
     FROM runs r
     LEFT JOIN run_config c ON c.run_id = r.run_id""",
    # Mirrors internal/aggregate/schema.go's role_coverage view — one row
    # per (run_id, role) joining runs with role_scorecards.
    """CREATE VIEW role_coverage AS
     SELECT r.run_id, r.model, r.resolved_real_model,
            rs.role, rs.verdict, rs.threshold_met, rs.threshold,
            rs.scenario_count_expected, rs.scenario_count_observed,
            rs.metrics_json
     FROM runs r
     JOIN role_scorecards rs ON rs.run_id = r.run_id""",
]


@pytest.fixture
def seeded_db(tmp_path: Path) -> Path:
    """A small DuckDB with 2 real models, 3 runs, 6 queries, and 2
    community-benchmark rows."""
    db_path = tmp_path / "resolver.duckdb"
    conn = duckdb.connect(str(db_path))
    try:
        for stmt in SCHEMA_DDL:
            conn.execute(stmt)

        # 3 runs: 2 of model-A-virt1, 1 of model-B-virt2
        conn.executemany(
            """INSERT INTO runs (run_id, scorecard_path, manifest_path, model,
                resolved_real_model, tier, overall,
                total_ms, avg_ms, p50_ms, p95_ms, max_ms, query_count,
                correct_count, partial_count, incorrect_count, error_count)
               VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
            [
                ("run-a1", "/sc/a1", "/m/a1", "gresh-test-a", "Org/ModelA", "1", "PASS",
                 1000, 500, 500, 800, 900, 2, 2, 0, 0, 0),
                ("run-a2", "/sc/a2", "/m/a2", "gresh-test-a", "Org/ModelA", "1", "PASS",
                 1050, 525, 525, 850, 950, 2, 2, 0, 0, 0),
                ("run-b1", "/sc/b1", "/m/b1", "gresh-test-b", "Org/ModelB", "1", "FAIL",
                 2000, 1000, 1000, 1500, 1800, 2, 1, 0, 1, 0),
            ],
        )

        # 6 queries (2 per run)
        conn.executemany(
            """INSERT INTO queries (run_id, tier, scenario_id, query,
                expected_tool, score, reason, elapsed_ms, tool_calls_json, content)
               VALUES (?,?,?,?,?,?,?,?,?,?)""",
            [
                ("run-a1", "T1", "T1.1", "q1", "exec", "correct", "", 500, "[]", None),
                ("run-a1", "T2", "T2.1", "q2", "graph_query", "correct", "", 500, "[]", None),
                ("run-a2", "T1", "T1.1", "q1", "exec", "correct", "", 525, "[]", None),
                ("run-a2", "T2", "T2.1", "q2", "graph_query", "correct", "", 525, "[]", None),
                ("run-b1", "T1", "T1.1", "q1", "exec", "correct", "", 1000, "[]", None),
                ("run-b1", "T2", "T2.1", "q2", "graph_query", "incorrect", "wrong tool", 1000, "[]", None),
            ],
        )

        # run_config: 2 rows share a repeat_group so variance() has something to return
        conn.executemany(
            """INSERT INTO run_config (run_id, virtual_model, real_model,
                 backend_port, default_enable_thinking, clamp_enable_thinking,
                 tool_parser, mtp, context_size, quantization, repeat_group)
               VALUES (?,?,?,?,?,?,?,?,?,?,?)""",
            [
                ("run-a1", "gresh-test-a", "Org/ModelA", 3040, True, True, "qwen3_xml", True, 131072, "fp8", "group-a"),
                ("run-a2", "gresh-test-a", "Org/ModelA", 3040, True, True, "qwen3_xml", True, 131072, "fp8", "group-a"),
                ("run-b1", "gresh-test-b", "Org/ModelB", 3041, False, False, "hermes", False, 32768, "fp16", "group-b"),
            ],
        )

        conn.executemany(
            """INSERT INTO community_benchmarks (model, model_key, benchmark, metric,
                 value, source_url, as_of, notes)
               VALUES (?,?,?,?,?,?,?,?)""",
            [
                ("Org/ModelA", "modela", "bfcl", "overall", 0.82, "https://example.com/bfcl", "2026-03-15", ""),
                ("Org/ModelB", "modelb", "mmlu", "5shot", 0.79, "https://example.com/mmlu", "2026-02-01", ""),
            ],
        )

        # role_scorecards: 2 roles × 3 runs = 6 rows. ModelA passes both;
        # ModelB fails safety-refuse (threshold 100%) but passes agentic.
        conn.executemany(
            """INSERT INTO role_scorecards (run_id, role, verdict, threshold_met,
                 threshold, metrics_json, scenario_count_expected, scenario_count_observed)
               VALUES (?,?,?,?,?,?,?,?)""",
            [
                ("run-a1", "agentic-toolcall", "PASS", True,  90.0,  "{}", 1, 1),
                ("run-a1", "safety-refuse",    "PASS", True,  100.0, "{}", 1, 1),
                ("run-a2", "agentic-toolcall", "PASS", True,  90.0,  "{}", 1, 1),
                ("run-a2", "safety-refuse",    "PASS", True,  100.0, "{}", 1, 1),
                ("run-b1", "agentic-toolcall", "PASS", True,  90.0,  "{}", 1, 1),
                ("run-b1", "safety-refuse",    "FAIL", False, 100.0, "{}", 1, 0),
            ],
        )
    finally:
        conn.close()
    return db_path


@pytest.fixture
def prompt_template(tmp_path: Path) -> Path:
    """A minimal Jinja template — the real one lives at
    docs/prompts/compare-models.md but tests use a trimmed local copy so
    they don't depend on the repo layout being set up."""
    p = tmp_path / "compare.md"
    p.write_text(
        "Runs: {{ n_runs }}. Models: {{ n_real_models }}. "
        "{% for r in runs %}[{{ r.model }}]{% endfor %}",
        encoding="utf-8",
    )
    return p
