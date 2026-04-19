"""Regression guard: build_report must never write under the repo's
reports/ directory. The correct output target is tools/analyze/out/ (or
any caller-supplied path outside reports/).

The test snapshots every file's mtime under reports/ before and after
each build_report invocation and asserts nothing changed."""
from __future__ import annotations

import time
from pathlib import Path

import pytest

from analyze.report import build_report


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _repo_root() -> Path:
    """Walk up from this file's location until we find a .git directory."""
    p = Path(__file__).resolve()
    for ancestor in [p, *p.parents]:
        if (ancestor / ".git").exists():
            return ancestor
    raise RuntimeError("Could not locate repo root from %s" % p)


def _snapshot_reports(reports_dir: Path) -> dict[Path, float]:
    """Return {absolute_path: mtime} for every non-symlink file under
    reports_dir, or an empty dict if the directory does not exist."""
    if not reports_dir.exists():
        return {}
    return {
        f: f.stat().st_mtime
        for f in reports_dir.rglob("*")
        if f.is_file() and not f.is_symlink()
    }


def _assert_reports_untouched(before: dict[Path, float], after: dict[Path, float]) -> None:
    new_files = set(after) - set(before)
    assert not new_files, f"build_report created new file(s) under reports/: {new_files}"

    for path, mtime_before in before.items():
        mtime_after = after.get(path)
        if mtime_after is None:
            continue  # file was deleted — not our concern here
        assert mtime_after <= mtime_before, (
            f"build_report modified {path} (mtime moved from {mtime_before} to {mtime_after})"
        )


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------

def test_build_report_dry_run_does_not_touch_reports(seeded_db, prompt_template, tmp_path):
    """dry_run=True: no files written anywhere, reports/ is unmodified."""
    reports_dir = _repo_root() / "reports"
    before = _snapshot_reports(reports_dir)

    out = tmp_path / "out" / "analysis.md"
    result = build_report(
        db_path=seeded_db,
        out_path=out,
        template=prompt_template,
        reporter_model="gresh-unused",
        endpoint="http://never-called.example/v1/chat/completions",
        dry_run=True,
    )

    after = _snapshot_reports(reports_dir)
    _assert_reports_untouched(before, after)

    # Output must not have landed under reports/ either
    assert result == "-"
    assert not out.exists(), "dry-run must not write the output file"


def test_build_report_live_does_not_touch_reports(seeded_db, prompt_template, tmp_path, monkeypatch):
    """dry_run=False with mocked reporter: output goes to tmp_path, not
    reports/."""
    reports_dir = _repo_root() / "reports"
    before = _snapshot_reports(reports_dir)

    # Monkeypatch call_reporter so no real network call is made.
    import analyze.report as report_mod
    monkeypatch.setattr(
        report_mod,
        "call_reporter",
        lambda *_args, **_kwargs: "## Mocked analysis\n\n1. ModelA\n",
    )

    out = tmp_path / "out" / "analysis.md"
    result = build_report(
        db_path=seeded_db,
        out_path=out,
        template=prompt_template,
        reporter_model="gresh-external",
        endpoint="http://mock-proxy.test/v1/chat/completions",
        dry_run=False,
    )

    after = _snapshot_reports(reports_dir)
    _assert_reports_untouched(before, after)

    # Output must be under tmp_path, not under reports/
    result_path = Path(result)
    assert result_path == out, f"Expected output at {out}, got {result_path}"
    assert result_path.is_relative_to(tmp_path), (
        f"build_report wrote to {result_path}, which is outside tmp_path {tmp_path}"
    )
    assert not result_path.is_relative_to(reports_dir), (
        f"build_report wrote to {result_path}, which is inside reports/ {reports_dir}"
    )
    assert out.exists(), "output file must exist after non-dry-run"
