"""Test the report pipeline with a mocked LLM endpoint. No live network."""
from __future__ import annotations

from pathlib import Path

import pytest
import respx
import httpx

from analyze.report import build_report, gather_context, summarize_data, self_eval_guard
from analyze.db import Store, RunSummary


def test_gather_context_populates_counts(seeded_db):
    with Store(seeded_db) as s:
        ctx, runs = gather_context(s)
    assert ctx["n_runs"] == 3
    assert ctx["n_real_models"] == 2
    # variance should have 2 rows (group-a × 2 scenarios)
    assert ctx["n_variance_rows"] == 2
    # community join picked up both real models
    assert len(ctx["community_benchmarks"]) == 2


def test_summarize_data_has_section_headers(seeded_db):
    with Store(seeded_db) as s:
        ctx, _ = gather_context(s)
    md = summarize_data(ctx)
    for section in ["Source data summary", "Runs", "Community benchmarks", "Per-scenario stddev"]:
        assert section in md, f"missing section: {section}"


def test_self_eval_guard_warns_on_reporter_match(seeded_db, capsys):
    with Store(seeded_db) as s:
        runs = s.run_summaries()
    # gresh-test-a IS a model in the dataset, so selecting it as reporter
    # must print a stderr warning.
    self_eval_guard(runs, "gresh-test-a")
    err = capsys.readouterr().err
    assert "self-biased" in err

    # gresh-not-in-set does not match → no warning.
    self_eval_guard(runs, "gresh-not-in-set")
    err = capsys.readouterr().err
    assert err == ""


def test_build_report_dry_run_writes_nothing(seeded_db, prompt_template, tmp_path, capsys):
    out = tmp_path / "out" / "analysis.md"
    result = build_report(
        db_path=seeded_db,
        out_path=out,
        template=prompt_template,
        reporter_model="gresh-unused",
        endpoint="http://never-called.example/v1/chat/completions",
        dry_run=True,
    )
    assert result == "-"
    assert not out.exists(), "dry-run must not write the output file"
    captured = capsys.readouterr().out
    assert "Source data summary" in captured
    assert "Runs: 3" in captured  # from the minimal template


@respx.mock
def test_build_report_calls_reporter_llm(seeded_db, prompt_template, tmp_path):
    # OpenAI SDK strips /chat/completions and issues POST /chat/completions to base_url.
    endpoint = "http://mock-proxy.test/v1/chat/completions"
    respx.post("http://mock-proxy.test/v1/chat/completions").mock(
        return_value=httpx.Response(
            200,
            json={
                "choices": [{"message": {"role": "assistant", "content": "## Ranking\n\n1. ModelA\n"}}],
                "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
            },
        )
    )
    out = tmp_path / "out" / "analysis.md"
    result = build_report(
        db_path=seeded_db,
        out_path=out,
        template=prompt_template,
        reporter_model="gresh-external",
        endpoint=endpoint,
        dry_run=False,
    )
    assert result == str(out)
    assert out.exists()
    body = out.read_text(encoding="utf-8")
    assert "Ranking" in body
    assert "ModelA" in body
    # The data-summary block is appended after the LLM output.
    assert "Source data summary" in body


@respx.mock
def test_build_report_tolerates_reporter_failure(seeded_db, prompt_template, tmp_path):
    endpoint = "http://broken.test/v1/chat/completions"
    respx.post("http://broken.test/v1/chat/completions").mock(
        return_value=httpx.Response(500, json={"error": "upstream is down"})
    )
    out = tmp_path / "out" / "analysis.md"
    result = build_report(
        db_path=seeded_db,
        out_path=out,
        template=prompt_template,
        reporter_model="gresh-broken",
        endpoint=endpoint,
        dry_run=False,
    )
    # Still writes the report; analysis body is a readable failure note +
    # the data summary (so the run is never wasted).
    assert out.exists()
    body = out.read_text(encoding="utf-8")
    assert "analysis failed" in body
    assert "Source data summary" in body


def test_missing_db_raises_filenotfound(tmp_path):
    from analyze.report import build_report

    with pytest.raises(FileNotFoundError):
        build_report(
            db_path=tmp_path / "nope.duckdb",
            out_path=tmp_path / "out.md",
            template=tmp_path / "t.md",
            reporter_model="x",
            endpoint="http://nope",
        )
