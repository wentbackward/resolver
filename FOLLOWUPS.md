# v2.0.1 follow-ups

Captured from the Phase 4 validator reviews at the end of v2 autopilot
(commit `fc2c92f`, 2026-04-19). None of these blocked shipping v2;
they're here so a later session can pick them up without re-running the
validators.

Validator verdicts (all APPROVE / APPROVE-WITH-* ; none REJECT):
- Architect: APPROVE-WITH-FOLLOWUPS (items under "Append-only" below)
- Security: APPROVE (items under "Security hardening")
- Code reviewer: APPROVE-WITH-IMPROVEMENTS (items under "Code quality" —
  one MAJOR already fixed in `fc2c92f`; the rest remain)

## Append-only additions (architect)

- [ ] **Jupyter notebooks** — `tools/analyze/notebooks/quickstart.ipynb`
      + `reproducibility.ipynb`. Plan AC called for these in Phase 5.
      Code already supports them; just missing the `.ipynb` files.
      Low effort, medium impact for data-science onramp.
- [ ] **Forward-compat ingest test** — assert the aggregator's
      `manifestVersion > SchemaVersion` warning path (currently at
      `internal/aggregate/ingest.go:231-234`) fires cleanly against a
      synthetic `manifestVersion=3` fixture. Warning-path code exists;
      test does not.
- [ ] **Community-benchmark alias map** — plan AC mentions
      case-insensitive / dash-variant matching (e.g.
      `Qwen3.6-35B-A3B-FP8` ≡ `qwen3.6-35b-a3b`). Currently
      `internal/aggregate/community.go` validates schema but doesn't
      alias-match on model. Medium impact for comparison-view quality
      when community rows use different casing than resolver runs.
- [ ] **darwin/arm64 CI leg** — plan Phase 7 AC asked for Linux AND
      macOS. Current matrix is ubuntu-only. Adds CGO-on-macOS
      complexity; low practical impact until a Mac user reports a
      build regression.
- [ ] **Python filesystem-boundary test** — a unit test that asserts
      `build_report` / `analyze report` never writes under `reports/`.
      The behavior is correct (default out is `tools/analyze/out/`);
      only the explicit negative test is missing.

## Security hardening

- [ ] **(M1) SSRF guard on `--endpoint`** — `endpointOrigin`
      (`internal/adapter/openai_chat.go:348-355`) returns the input
      unchanged if `/v1/` isn't present, which could let a typo hit
      AWS metadata (`http://169.254.169.254/…`) or similar. Parse with
      `net/url`, reject non-http(s) schemes, or note in
      `docs/manifest-schema.md` that the harness assumes operator-
      supplied endpoints are trusted.
- [ ] **(M2) YAML DoS hardening** — `gopkg.in/yaml.v3` decoders for
      manifest.RunConfig, community-benchmarks, run-config sidecars,
      and gate-thresholds currently unconstrained. Wrap with
      `yaml.NewDecoder(r)` + a size cap (e.g. 1 MB) before
      `os.ReadFile`. Best-practice only under current trust model.
- [ ] **(L1) Prompt redaction wording** —
      `docs/prompts/run-benchmark.md` instructs an AI to SSH and
      `cat` llm-proxy / vLLM recipes. Add a sentence:
      "Redact `api_key`, `bearer`, or `authorization` lines from any
      config before showing it to the AI, even transiently."
- [ ] **(L2) `govulncheck` in CI** — add a job that runs
      `govulncheck ./...` against the module graph (tracks go-duckdb's
      bundled DuckDB C library CVEs automatically).
- [ ] **(L3) `pip-audit` in CI** — same idea for Python deps in
      `tools/analyze/pyproject.toml`. Pre-release check.

## Code quality

- [ ] **(MAJOR) Cross-language schema-drift test** — Python
      `tools/analyze/src/analyze/db.py` hand-codes column names of
      the `run_summary` / `comparison` views. A rename in
      `internal/aggregate/schema.go` blows up at runtime rather than
      CI. Add a Go `TestViewColumnsStable` that snapshots
      `PRAGMA table_info(...)` → golden list + a Python test that
      `SELECT * LIMIT 0` matches those names.
- [ ] **(MINOR) `endpointOrigin` useless bool** —
      `internal/adapter/openai_chat.go:348-355` returns `(string, bool)`
      where the bool is always `true`. Kill it, or actually validate
      the URL.
- [ ] **(MINOR) `self_eval_guard` double-queries** —
      `tools/analyze/src/analyze/report.py:173-174` calls
      `store.run_summaries()` twice per report. Cache on the Store or
      thread the list through.
- [ ] **(MINOR) `findScorecard` comment vs code** —
      `internal/aggregate/ingest.go:207-216` comment says "closest
      timestamp" but code returns `candidates[len-1]`. Either
      implement the closest-ts heuristic or update the comment.
- [ ] **(MINOR) `scenario.SetGatedTiers` parallel-test caveat** — note
      in the package godoc: not safe for `t.Parallel()`. Snapshot +
      restore contract is already correct in the one test that needs
      it; the note is for future contributors.
- [ ] **(MINOR) `-rep1` scorecard vs golden anchor** — the current
      `TestTier1RepeatRun` compares summary bytes across repeats but
      not against `golden/scorecard_example.json`. A silent scorecard-
      shape regression *only on the `-rep{k}` path* would slip past.
      Add one assertion.
- [ ] **(NIT) `rootsOrDefault` dup string** —
      `internal/aggregate/ingest.go` has `["reports",
      "research/captures"]` in both the function and the `Options`
      godoc. Extract a package const.
- [ ] **(NIT) `community.go` double date check** — regex + `time.Parse`
      both validate `YYYY-MM-DD`. Pick one.
- [ ] **(NIT) Double-JSON-encode tool-call args** — current adapter
      code at `openai_chat.go:170-184` double-encodes correctly. Worth
      a single-line test proving the round-trip isn't accidentally
      `"{\\\"a\\\": 1}"`.

## Architecture notes captured during review (informational, no action)

- `RunConfig` has 30+ fields; the v2 plan's original list was ~11. The
  architect flagged this as "schema-lock-in creep" but noted
  principle #2 (additive-only) is honoured; all fields are optional.
  Watch for ossification pressure when the list grows again.
- `ResolveRealModel` now prefers matching `forModel → root` then falls
  back to `data[0]`, which is a strict improvement over the plan text
  ("pluck data[0]"). Documented as deliberate in the function godoc.
- Single-shot runs now always emit `runConfig.repeat_group` (self-
  referential to runId). Intentional — it makes `repeat_group` always
  queryable, so single-shot vs batch is just cardinality. Called out
  for documentation in `docs/manifest-schema.md` if it isn't already.
