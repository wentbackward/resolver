"""Typer CLI. `analyze report` is the primary command.

Principle #3 of the v2 plan: Python never writes under `reports/` — the
default out dir is `tools/analyze/out/` (created if missing).
"""
from __future__ import annotations

import sys
from datetime import datetime, timezone
from pathlib import Path

import typer

from .report import (
    DEFAULT_ENDPOINT,
    DEFAULT_PROMPT_TEMPLATE,
    DEFAULT_REPORTER_MODEL,
    build_report,
)

app = typer.Typer(
    name="analyze",
    help="Cross-run analyzer for resolver benchmark data.",
    no_args_is_help=True,
)


def _default_out_path() -> Path:
    ts = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H-%M-%S")
    return Path("tools/analyze/out") / f"analysis-{ts}.md"


@app.command()
def report(
    db: Path = typer.Option(
        Path("reports/resolver.duckdb"),
        "--db",
        help="DuckDB file produced by `resolver aggregate`.",
    ),
    out: Path = typer.Option(
        None,
        "--out",
        help="Markdown output path (default: tools/analyze/out/analysis-{ts}.md).",
    ),
    reporter_model: str = typer.Option(
        DEFAULT_REPORTER_MODEL,
        "--reporter-model",
        help="Virtual model to invoke on the llm-proxy for the write-up.",
    ),
    endpoint: str = typer.Option(
        DEFAULT_ENDPOINT,
        "--endpoint",
        help="OpenAI-compatible chat-completions endpoint for the reporter LLM.",
    ),
    api_key: str = typer.Option(
        None,
        "--api-key",
        help="Bearer token for the reporter endpoint (not needed for local llm-proxy).",
    ),
    template: Path = typer.Option(
        Path(DEFAULT_PROMPT_TEMPLATE),
        "--template",
        help="Jinja prompt template. Default: docs/prompts/compare-models.md.",
    ),
    dry_run: bool = typer.Option(
        False,
        "--dry-run",
        help="Render prompt + source-data summary to stdout. Skip the LLM call.",
    ),
) -> None:
    """Render the comparison prompt and (unless --dry-run) ask the reporter
    LLM to author a Markdown analysis."""
    out_path = out or _default_out_path()
    try:
        path = build_report(
            db_path=db,
            out_path=out_path,
            template=template,
            reporter_model=reporter_model,
            endpoint=endpoint,
            api_key=api_key,
            dry_run=dry_run,
        )
    except FileNotFoundError as e:
        typer.secho(f"error: {e}", fg=typer.colors.RED, err=True)
        raise typer.Exit(code=2)
    if dry_run:
        return
    typer.secho(f"wrote {path}", fg=typer.colors.GREEN)


@app.command()
def query(
    db: Path = typer.Argument(Path("reports/resolver.duckdb")),
    sql: str = typer.Argument(..., help="Ad-hoc SQL to run against the store."),
) -> None:
    """Run arbitrary SQL against the DuckDB store (sanity helper)."""
    import duckdb

    if not db.exists():
        typer.secho(f"error: {db} not found; run `resolver aggregate` first.", fg=typer.colors.RED, err=True)
        raise typer.Exit(code=2)
    conn = duckdb.connect(str(db), read_only=True)
    try:
        cur = conn.execute(sql)
        cols = [d[0] for d in cur.description] if cur.description else []
        if cols:
            print("\t".join(cols))
        for row in cur.fetchall():
            print("\t".join("" if v is None else str(v) for v in row))
    finally:
        conn.close()


if __name__ == "__main__":
    sys.exit(app())
