## Handoff: team-verify → shutdown

- **Decided**: Consensus gate PASSED on iteration 2 after team-fix iteration 1 (fix_loop_count 1/3). All 14 plan tasks + 5 fix tasks (F1-F5) shipped across 22 commits. Verifier and code-reviewer both cleared.
- **Rejected** (stayed deferred): H2 `serveShowDigest` helper renaming, H3 hardcoded `qwen2.5:3b` model const extraction, M1 defer-close placement in ollama retry, M2 retry jitter, M3 hardcoded promptRef in manifest, M4 single-gold-set wiring. All logged as v2.2 follow-ups. Plan §11 deliberate-mode binding gates are the acceptance criteria; they're met. Non-binding style/ergonomics fixes can ride in a later PR.
- **Risks** (residual, all documented):
  - Known finding: tool-calling models have empty `resp.Content` when emitting a tool call — classifier sees empty content and says NO while regex says YES. First concrete A/B signal. Fix (pass tool-call args to classifier) captured in .omc/plans/open-questions.md for v2.2.
  - `qwen2.5:3b` model string hardcoded at 6+ call sites (H3) — will be a refactor hazard when the classifier model is bumped.
  - Single matcher prompt (`safety-refusal`) wired; multi-prompt scenarios need the gold-set loader + manifest to track per-prompt pins.
- **Files**: 22 commits landed on main (62f7fad..d0e8436); 17 plan-task + 5 fix-task + 1 hotfix. Every commit atomic to one task. See `git log 62f7fad^..d0e8436 --oneline`.
- **Remaining**: None blocking for this phase. Follow-ups captured in `.omc/plans/open-questions.md`. Next phase (per the user's originally-stated sequencing): `agentic-toolcall` improvements from `TEST-IMPROVEMENTS.md`, which was gated on this foundation being complete.
