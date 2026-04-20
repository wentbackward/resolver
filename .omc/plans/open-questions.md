# Open Questions Tracker

Centralised list of unresolved questions and deferred decisions across plans. Append-only; mark items `[x]` when resolved and move to `### Resolved`.

---

## resolver v2.2 (v2.1 follow-ups) — opened 2026-04-20

- [ ] **hitl role threshold** — Spec left this implicit; v2.1 shipped 60% informational. Firm it up once enough hitl captures exist to argue for a harder floor. (See `resolver-v2-1-plan.md` §9 Follow-ups.)
- [ ] **reducer-sexp port** — v2.1 ships a 0-scenario placeholder. Port sshwarm's `live-sexp-suite-*.json` into `roles/reducer-sexp/` and wire derived rates. (See `resolver-v2-1-plan.md` §9 Follow-ups.)
- [ ] **Reducer-json true 5-rate aggregation** — v2.1 shipped a stopgap: per-scenario verdict carries one number, `parse_validity` proxied as correct/total. Replace with independent derivation of all 5 rates (parse_validity, schema_validity, envelope_purity, locality_compliance, status_correctness) from the matcher boolean vectors. (See `RELEASE-NOTES-v2.1.md` "Known gaps".)
- [ ] **"Harness ships N" cosmetic warning** — `internal/aggregate/ingest.go:244` fires spuriously against v3 manifests during some ingest paths; ingest still succeeds best-effort. Root-cause + clean up. No data integrity impact. (See `RELEASE-NOTES-v2.1.md` "Known gaps".)
- [ ] **Sshwarm schema drift automation** — `cmd/resolver/data/shared/schemas/reducer-envelope.json` is a point-in-time mirror. Add a GitHub Action that diffs it against sshwarm's live contract weekly. (See `resolver-v2-1-plan.md` §9 Follow-ups.)
- [ ] **Re-score archived captures through v2.1 scorers** — Would let the heat-map carry a historical line for the 8 archived models. Not shipped in v2.1. (See `resolver-v2-1-plan.md` §9 Follow-ups.)

---

### Resolved

- [x] **Tool-calling preamble placement (2026-04-20)** — Decision: **per-role**, not a shared global preamble. Rationale: some roles (reducer-json, reducer-sexp, classifier) do NOT call tools at all; putting the tool-calling hints in their system prompts would be incorrect guidance. Each role's `cmd/resolver/data/roles/<role>/system-prompt.md` includes the 3 hints ONLY when that role exercises tool-calling. The three hints to embed verbatim:
  1. "Complete your thought process entirely within `<think>` tags before providing the tool call in the required format. Do not nest tags or repeat the thought process after the tool call is generated."
  2. "Keep reasoning concise and focused on the immediate next step. Move to the tool call as soon as the plan is clear."
  3. "End your response immediately after the tool call. Do not provide post-call explanations."
  The shared `cmd/resolver/data/shared/system-prompts/tools-preamble.md` file mentioned in the plan **is not created**. Instead, the hints are baked into the tool-calling roles' own `system-prompt.md`. Manifest `prompt_rev` hashes the role-specific prompt body (not a shared preamble that doesn't exist).
- [x] **Classifier label inventory (2026-04-20)** — Decision: `graph_query` is a **separate label**, not an exec-subtype. Rationale: graph_query is fundamentally about memory/recency — it tests whether the model uses latest info instead of making assumptions from prior context. Conflating it with `exec` loses that signal. Final label set: `{exec, diagnose, refuse, escalate, hitl, graph_query}`. Classifier scenarios exercise each label with at least one prompt that is "decoy-tempted" toward another label.
- [x] **Archived-scorecard root-key rewrite (2026-04-20)** — Decision: **rewrite `summary` → `summary_v2_legacy`** on archive. Rationale: prevents naive cross-directory jq merges between v1-era and v2.1-era scorecards. Not a lot of data; trivial to reproduce if anything breaks. Phase 8 archive step applied this rewrite; see `research/captures-v1/README.md` for the shape-detector one-liner. (Previously flagged as optional mitigation to R10; promoted to required and shipped.)
- [x] **min_p sidecar handling (2026-04-20)** — Decision: **add `MinP *float64` to the `RunConfig` sidecar struct** in manifest v3. Shipped in `d088e38` (Phase 4). Archived gresh-reasoner captures predate the field and are documented as a known reproducibility gap in `research/captures-v1/README.md`.
