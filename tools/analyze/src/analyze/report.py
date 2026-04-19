"""Render the comparison prompt and (optionally) ask an LLM to author an
opinionated analysis of the aggregated data.

The prompt template lives at docs/prompts/compare-models.md (Jinja). The
reporter-LLM call goes to any OpenAI-compatible chat endpoint — default
is the llm-proxy's `gresh-general`, overridable via --reporter-model /
--endpoint.

Safeguards:
  - If the model under test equals the reporter model, a stderr warning
    is emitted before the call — the benchmark's purpose is undermined
    if a model grades itself.
  - On any reporter failure (timeout, 4xx, 5xx, parse error) the markdown
    report is still written but the analysis body is `(analysis failed:
    <reason>)` followed by the raw data tables — the run is never wasted.
  - `--dry-run` renders the prompt + data and exits 0 without any POST.
"""
from __future__ import annotations

import os
import sys
from dataclasses import asdict
from datetime import datetime, timezone
from pathlib import Path

import httpx
from jinja2 import Environment, FileSystemLoader, StrictUndefined
from openai import OpenAI

from .db import Store, CommunityRow, RunSummary, TierPct, VarianceRow


DEFAULT_REPORTER_MODEL = "gresh-general"
DEFAULT_ENDPOINT = os.environ.get(
    "RESOLVER_REPORTER_ENDPOINT",
    "http://localhost:4000/v1/chat/completions",
)
DEFAULT_PROMPT_TEMPLATE = "docs/prompts/compare-models.md"
REPORTER_TIMEOUT_S = 60.0


def render_prompt(template_path: Path, ctx: dict) -> str:
    env = Environment(
        loader=FileSystemLoader(template_path.parent),
        undefined=StrictUndefined,
        keep_trailing_newline=True,
    )
    return env.get_template(template_path.name).render(**ctx)


def gather_context(store: Store) -> dict:
    """Run the fixed query set against the store and return a prompt
    context dictionary. Dataclasses are converted to dicts so the Jinja
    template can use attribute-style access either way."""
    runs = store.run_summaries()
    tiers = store.tier_pcts()
    variance = store.variance()
    real_models = sorted({r.resolved_real_model or r.model for r in runs})
    community = store.community_for(real_models)

    return {
        "generated_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "runs": [asdict(r) for r in runs],
        "tier_pcts": [asdict(t) for t in tiers],
        "variance": [asdict(v) for v in variance],
        "community_benchmarks": [asdict(c) for c in community],
        "n_runs": len(runs),
        "n_real_models": len({r.resolved_real_model or r.model for r in runs}),
        "n_variance_rows": len(variance),
    }


def summarize_data(ctx: dict) -> str:
    """Plain-text data summary prefixed to every report (auditable source
    for whatever the LLM ends up writing below it)."""
    lines = []
    lines.append(f"## Source data summary ({ctx['generated_at']})\n")
    lines.append(f"- {ctx['n_runs']} run(s) across {ctx['n_real_models']} distinct real model(s).")
    lines.append(f"- {ctx['n_variance_rows']} per-scenario variance row(s) from repeat groups.")
    lines.append(f"- {len(ctx['community_benchmarks'])} community-benchmark row(s) joined.\n")

    if ctx["runs"]:
        lines.append("### Runs\n")
        lines.append("| run_id | model | real_model | overall | thinking | tool_parser | total |")
        lines.append("|---|---|---|---|---|---|---|")
        for r in ctx["runs"][:50]:
            lines.append(
                f"| {r['run_id']} | {r['model']} | {r['cfg_real_model'] or r['resolved_real_model'] or '-'} "
                f"| {r['overall']} | {r['cfg_thinking']} | {r['tool_parser'] or '-'} | {r['total']} |"
            )
        lines.append("")

    if ctx["community_benchmarks"]:
        lines.append("### Community benchmarks\n")
        lines.append("| model | benchmark | metric | value | as_of |")
        lines.append("|---|---|---|---|---|")
        for c in ctx["community_benchmarks"]:
            lines.append(f"| {c['model']} | {c['benchmark']} | {c['metric']} | {c['value']} | {c['as_of']} |")
        lines.append("")

    if ctx["variance"]:
        lines.append("### Per-scenario stddev (repeats)\n")
        lines.append("| repeat_group | scenario | n | mean | stddev | all_correct |")
        lines.append("|---|---|---|---|---|---|")
        for v in ctx["variance"][:20]:
            lines.append(
                f"| {v['repeat_group']} | {v['scenario_id']} | {v['n_runs']} "
                f"| {v['mean_score']:.3f} | {v['stddev_score']:.3f} | {v['all_correct']} |"
            )
        lines.append("")

    return "\n".join(lines)


def self_eval_guard(runs: list[RunSummary], reporter_model: str) -> None:
    """Stderr warn if the reporter coincides with any model under test."""
    under_test = {r.model for r in runs}
    if reporter_model in under_test:
        print(
            f"warn: reporter model {reporter_model!r} is also in the set of models under test; "
            f"analysis may be self-biased. Pass --reporter-model <different-model> to fix.",
            file=sys.stderr,
        )


def call_reporter(
    prompt: str,
    *,
    model: str,
    endpoint: str,
    api_key: str | None,
    timeout: float = REPORTER_TIMEOUT_S,
) -> str:
    """POST the prompt to an OpenAI-compatible chat endpoint and return
    the assistant message content. Raises on any failure."""
    base_url = endpoint
    if base_url.endswith("/chat/completions"):
        # OpenAI SDK wants the /v1/ root, not the completions path.
        base_url = base_url.rsplit("/chat/completions", 1)[0]
    client = OpenAI(
        base_url=base_url,
        api_key=api_key or "sk-unused-local-proxy",
        timeout=httpx.Timeout(timeout, connect=5.0),
    )
    resp = client.chat.completions.create(
        model=model,
        messages=[{"role": "user", "content": prompt}],
        temperature=0.2,
    )
    content = resp.choices[0].message.content or ""
    return content.strip()


def build_report(
    *,
    db_path: Path,
    out_path: Path,
    template: Path,
    reporter_model: str,
    endpoint: str,
    api_key: str | None = None,
    dry_run: bool = False,
) -> str:
    """Full pipeline: open DB → gather data → render prompt → (LLM call
    unless --dry-run) → write Markdown. Returns the written path as a
    string for logging."""
    if not Path(db_path).exists():
        raise FileNotFoundError(
            f"DuckDB file not found at {db_path}. Produce one with "
            f"`resolver aggregate` (requires -tags duckdb build)."
        )
    with Store(db_path) as store:
        ctx = gather_context(store)
        self_eval_guard(store.run_summaries(), reporter_model)

    prompt = render_prompt(template, ctx)
    data_summary = summarize_data(ctx)

    if dry_run:
        # Print to stdout instead of writing; skip the LLM call.
        out = f"{data_summary}\n\n---\n\n## Rendered prompt (dry-run)\n\n```\n{prompt}\n```\n"
        print(out)
        return "-"

    try:
        analysis = call_reporter(prompt, model=reporter_model, endpoint=endpoint, api_key=api_key)
    except Exception as e:
        analysis = f"(analysis failed: {type(e).__name__}: {e})"

    full = (
        f"# Resolver cross-model analysis\n"
        f"_Reporter model: `{reporter_model}` @ `{endpoint}` — {ctx['generated_at']}_\n\n"
        f"{analysis}\n\n"
        f"---\n\n"
        f"{data_summary}\n"
    )
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(full, encoding="utf-8")
    return str(out_path)
