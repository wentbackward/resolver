# AI-assisted community-benchmarks refresh

This prompt tells an AI CLI (Claude Code, Codex, Gemini, etc.) how to
append new rows to
[`reports/community-benchmarks.yaml`](../../reports/community-benchmarks.yaml)
by discovering current published values on official leaderboards. The
file is append-only — existing rows are never mutated.

---

## Task

Given **`{MODEL}`** (a real model identifier like
`meta-llama/Llama-3.1-70B-Instruct`) and optionally a **`{BENCHMARK}`**
name, discover the current score(s) from the canonical source(s) and
append one YAML entry per `(benchmark, metric)` pair to
`reports/community-benchmarks.yaml`.

## Sources (use in this priority order)

1. **Model card on HuggingFace** (`https://huggingface.co/{MODEL}`) —
   lab-authored numbers. Preferred when present.
2. **Official leaderboards:**
   - BFCL (Berkeley Function-Calling Leaderboard):
     https://gorilla.cs.berkeley.edu/leaderboard.html
   - tau-bench (Sierra Research):
     https://github.com/sierra-research/tau-bench
   - RULER (NVIDIA):
     https://github.com/NVIDIA/RULER
   - GPQA (Rein et al.):
     https://github.com/idavidrein/gpqa
   - Open LLM Leaderboard v2 (HuggingFace):
     https://huggingface.co/spaces/open-llm-leaderboard/open_llm_leaderboard
   - HELM (Stanford):
     https://crfm.stanford.edu/helm/
3. **Original paper / preprint** if #1 and #2 don't list the metric.
4. **Vendor announcement blog** (Anthropic, OpenAI, Google) — typically
   has a MMLU / GPQA / benchmark-family table.

## Procedure

1. **Existence check:** Does
   `reports/community-benchmarks.yaml` already contain an entry for
   `(model, benchmark, metric)` with an `as_of` within the last 90
   days? If yes, skip — the existing entry is still current enough.
2. **Discover the number** using the source priority above. Prefer the
   most canonical source for the metric; don't scrape third-party
   aggregators unless the primary source is unavailable.
3. **Verify the metric scale.** BFCL reports accuracies as decimals
   (0.0–1.0). MMLU is often quoted as a percentage (0–100). RULER is a
   decimal. Use the leaderboard's native scale — do not convert.
4. **Record the `as_of` date** as the date you verified (today), not
   the date the leaderboard entry was posted. If the leaderboard page
   surfaces a "last updated" field, prefer that.
5. **Append to the YAML** — add a new record at the end of the
   `entries:` list. Do **not** edit any existing row. Remove any `notes`
   field saying "seed — verify" if you've just verified that row.
6. **Re-run `resolver aggregate`** to validate the schema:

   ```bash
   resolver aggregate --dry-run
   ```

   The aggregator rejects future `as_of` dates and missing required
   fields. Fix any error before committing.

## Entry format

```yaml
- model: "{MODEL}"              # exact casing + slashes as on HF
  benchmark: {benchmark-name}   # lowercase, e.g. bfcl / tau-bench / ruler / mmlu / gpqa
  metric: {metric-name}         # e.g. overall / 32k / retail / 5shot / diamond
  value: {NUMBER}               # use source's native scale
  source_url: "{URL}"           # canonical page you verified against
  as_of: YYYY-MM-DD             # today's date in UTC
  notes: "{optional}"           # if the source has a caveat, capture it
```

## Handling uncertainty

- **"I can't find this metric for this model."** Don't invent a number.
  Append nothing, and surface the gap in your commit message so future
  attempts can try again.
- **Multiple sources disagree.** Use the lab's own number (model card
  or paper). If that too is ambiguous, pick the most-cited source and
  note the discrepancy in `notes`.
- **Benchmark was superseded** (e.g., an eval tool shipped a breaking
  revision). Record the number with a `notes: "pre-v2 eval"` or similar
  so downstream consumers see the historical context.

## Commit protocol

One commit per refresh session. Example:

```
Refresh community-benchmarks for {MODEL}

- BFCL overall: 0.84 (verified 2026-04-19)
- MMLU 5shot:   0.89 (verified from model card)
- RULER 32k:    0.92 (verified from NVIDIA/RULER repo)

Skipped: tau-bench retail — no current published number for this model.
```

Never include the full `reports/community-benchmarks.yaml` diff in the
commit message itself; the diff is in the commit content.

## What not to do

- **Don't overwrite a row.** Append a newer `as_of` row with the
  updated value. The append-only contract is what makes the join stable
  for historical citations.
- **Don't normalize scales.** If BFCL's leaderboard reports 84.2%, store
  `84.2` (or `0.842` if they express it as a decimal — whichever is
  native to the source).
- **Don't import from third-party aggregators** (Papers With Code,
  model-directory blogs) unless you've verified the underlying number
  on the lab's own materials. Those aggregators fall out of sync.
