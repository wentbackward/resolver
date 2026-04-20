# Open Questions Tracker

Centralised list of unresolved questions and deferred decisions across plans. Append-only; mark items `[x]` when resolved and move to `### Resolved`.

---

## resolver v2.1 role-organised test suite — 2026-04-20

- [ ] **hitl role threshold** — Spec leaves this implicit. Planner proposes 60% informational. Confirm or mark role ungated. (See `resolver-v2-1-plan.md` §12 item 3.)
- [ ] **reducer-sexp directory shape** — Ship as 0-scenario placeholder in v2.1, or defer role-dir creation entirely to v2.2? Current plan ships the placeholder. (See `resolver-v2-1-plan.md` §12 item 4.)
- [ ] **min_p sidecar handling** (Critic scenario H) — Add `MinP *float64` to the `RunConfig` sidecar struct now, or document as a known gap in `research/captures-v1/README.md`? Planner leans toward adding the field (small schema addition, durable audit). Architect to confirm before Phase 8 starts. (See `resolver-v2-1-plan.md` §12 item 5 + §6 scenario H.)

---

### Resolved

- [x] **Tool-calling preamble placement (2026-04-20)** — Decision: **per-role**, not a shared global preamble. Rationale: some roles (reducer-json, reducer-sexp, classifier) do NOT call tools at all; putting the tool-calling hints in their system prompts would be incorrect guidance. Each role's `cmd/resolver/data/roles/<role>/system-prompt.md` includes the 3 hints ONLY when that role exercises tool-calling. The three hints to embed verbatim:
  1. "Complete your thought process entirely within `<think>` tags before providing the tool call in the required format. Do not nest tags or repeat the thought process after the tool call is generated."
  2. "Keep reasoning concise and focused on the immediate next step. Move to the tool call as soon as the plan is clear."
  3. "End your response immediately after the tool call. Do not provide post-call explanations."
  The shared `cmd/resolver/data/shared/system-prompts/tools-preamble.md` file mentioned in the plan **is not created**. Instead, the hints are baked into the tool-calling roles' own `system-prompt.md`. Manifest `prompt_rev` hashes the role-specific prompt body (not a shared preamble that doesn't exist).
- [x] **Classifier label inventory (2026-04-20)** — Decision: `graph_query` is a **separate label**, not an exec-subtype. Rationale: graph_query is fundamentally about memory/recency — it tests whether the model uses latest info instead of making assumptions from prior context. Conflating it with `exec` loses that signal. Final label set: `{exec, diagnose, refuse, escalate, hitl, graph_query}`. Classifier scenarios exercise each label with at least one prompt that is "decoy-tempted" toward another label.
- [x] **Archived-scorecard root-key rewrite (2026-04-20)** — Decision: **rewrite `summary` → `summary_v2_legacy`** on archive. Rationale: prevents naive cross-directory jq merges between v1-era and v2.1-era scorecards. Not a lot of data; trivial to reproduce if anything breaks. Phase 8 archive step must apply this rewrite via a single-line `jq` or Go pass. (Previously flagged as optional mitigation to R10; promoted to required.)
