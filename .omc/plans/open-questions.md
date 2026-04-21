  # Open Questions Tracker

Centralised list of unresolved questions and deferred decisions across plans. Append-only; mark items `[x]` when resolved and move to `### Resolved`.

---

## resolver v2.2 (v2.1 follow-ups) — opened 2026-04-20

- [ ] **hitl role threshold** — Spec left this implicit; v2.1 shipped 60% informational. Firm it up once enough hitl captures exist to argue for a harder floor. (See `resolver-v2-1-plan.md` §9 Follow-ups.)
- [ ] **reducer-sexp port** — v2.1 ships a 0-scenario placeholder. Port sshwarm's `live-sexp-suite-*.json` into `roles/reducer-sexp/` and wire derived rates. (See `resolver-v2-1-plan.md` §9 Follow-ups.) v2.1.1 handles the empty-scenarios case via harness inline-skip + INFO verdict (see `resolver-v2-1-1-plan.md` §5.5); the authorship gap remains open.
- [ ] **Reducer-json true 5-rate aggregation** — v2.1 shipped a stopgap: per-scenario verdict carries one number, `parse_validity` proxied as correct/total. Replace with independent derivation of all 5 rates (parse_validity, schema_validity, envelope_purity, locality_compliance, status_correctness) from the matcher boolean vectors. (See `RELEASE-NOTES-v2.1.md` "Known gaps".)
- [ ] **"Harness ships N" cosmetic warning** — `internal/aggregate/ingest.go:244` fires spuriously against v3 manifests during some ingest paths; ingest still succeeds best-effort. Root-cause + clean up. No data integrity impact. (See `RELEASE-NOTES-v2.1.md` "Known gaps".)
- [ ] **Sshwarm schema drift automation** — `cmd/resolver/data/shared/schemas/reducer-envelope.json` is a point-in-time mirror. Add a GitHub Action that diffs it against sshwarm's live contract weekly. (See `resolver-v2-1-plan.md` §9 Follow-ups.)
- [ ] **Re-score archived captures through v2.1 scorers** — Would let the heat-map carry a historical line for the 8 archived models. Not shipped in v2.1. (See `resolver-v2-1-plan.md` §9 Follow-ups.)

---

## resolver v2.2 (v2.1.1 follow-ups) — opened 2026-04-21

- [ ] **Long-context fixture/needle context-assembly** — v2.1.1 hard-fails scenarios declaring `fixtures`/`needle` when `RunTier1` has no context-assembly path. The long-context role's single needle scenario FAILs intentionally in v2.1.1 captures (confirmed in the sweep at commit `075fc84`). v2.2 must add a context-assembly runner (or teach `RunTier1`) so the needle test actually exercises context. (See `resolver-v2-1-1-plan.md` §5.2 + ADR 8.6.)
- [ ] **`--sweep` CLI cleanup** — `tier2-sweeps/` has been empty since v2.1; the legacy `--sweep tool-count|context-size` path at `cmd/resolver/main.go:343-438` is dead code. Decide: delete, deprecation-warn, or repurpose to point at `roles/{tool-count-survival,long-context}/*.yaml`. (See `resolver-v2-1-1-plan.md` ADR 8.6.)
- [ ] **`Expected*` field consolidation** — v2.1.1 adds `ExpectedLabel` alongside existing `ExpectedTool` on the Scenario struct. If a third `Expected*` field arrives (e.g., `ExpectedStatus`, `ExpectedEnvelope`), consolidate into `Expected map[string]any`. (See `resolver-v2-1-1-plan.md` ADR 8.6 and R8.)
- [ ] **Reducer-json 5-rate derivation upgrade** — v2.1.1 continues the v2.1 stopgap of `parse_validity = correct/total`. `pct` (added uniformly in v2.1.1) is also `correct/total` for reducer-json, so the two are redundant in v2.1.1. v2.2 replaces with independent derivation of all 5 rates. (Duplicate of earlier v2.2 item, now cross-referenced from `resolver-v2-1-1-plan.md` R7.)
- [ ] **Scenario-quality drops surfaced by v2.1.1 sweep** — The 2026-04-21 sweep produced 16 legitimate threshold FAILs (excluding 3 long-context hard-fails). Sample: `gresh-general/agentic-toolcall`, `gresh-coder/safety-escalate`, `gresh-reasoner/reducer-json`, etc. These are scenario-quality signals that v2.1.1 was built to surface, not harness bugs. Root-cause each in v2.2 (role-by-role review). See `RELEASE-NOTES-v2.1.1.md` "Sweep results".

---

## classifier-matcher-foundation — opened 2026-04-21

Source plan: `.omc/plans/classifier-matcher-foundation.md` (§5–§6). Escalated from deep-interview spec `.omc/specs/deep-interview-classifier-matcher-foundation.md` for Architect/Critic review.

- [ ] **OD-1 / Q1 — Verdict-time vs post-hoc classifier calls** — Spec chose verdict-time (F1a) for single-command UX. Architect asked to steelman post-hoc: is there a latency regime (large sweeps × model counts × classifier call counts) where inline dispatch becomes prohibitive, or a hybrid (capture content inline, classify in a queued post-pass) that preserves UX? — Matters because this is the central dispatch-shape decision and drives B3, B4, B5 design.
- [ ] **OD-2 — `PerQuery` twin-fields vs nested `AlternateVerdict` struct** — Spec chose flat twin-fields (F2a) for minimal PR blast radius. Forward-compatibility question: if phase N+2 adds a 3rd matcher engine, is the twin-field unwind cost higher than doing the nested shape now? — Matters because `metrics_json` shape is what external tooling (`analyze report`, dashboards) reads.
- [ ] **OD-3 / Q3 — Gold-set location (embedded vs external repo) + A/B-parity JSON shape** — Spec embeds under `cmd/resolver/data/gold-sets/`. Questions: should gold sets live in a separately-versioned repo so external contributors can ship their own? Is flat `{classifier_pct, classifier_correct, ...}` in VARCHAR forward-compatible with 3+ engines, or should we move to `matchers: {regex: {...}, classifier-qwen: {...}}` now? — Matters because any reshape later is a one-shot migration of all archived scorecards.
- [ ] **OD-4 / Q4 — Track A + Track B parallelisation safety** — Plan chose file-disjoint parallel tracks (F3a) with Track A on `data/roles/classifier/**` and Track B on `internal/**` + `cmd/resolver/main.go`. Is there a hidden coupling through per-role system-prompt loading at `cmd/resolver/main.go:620` that requires a shared-refactor PR first? Fallback is F3b (B→A→C sequential). — Matters for merge-order risk across two concurrent executors.
- [ ] **Q2 — Gold-set tripwire threshold + warn-vs-fail semantics** — Spec says warn loudly on <95%. Should a `--strict-gold-set` flag hard-abort the sweep? What is the denominator — raw agreement, or macro-averaged per label class (matters if the gold set is class-imbalanced, e.g. 18 refuse-positive vs 2 refuse-negative pairs)? — Matters because this is the tripwire for silent drift; the wrong metric gives a false sense of safety.

---

## classifier-matcher-foundation — Architect review feedback trail (2026-04-21)

Applied revisions (Architect → Planner → this iteration):

- **Revision 1 (applied):** `ClassifierInputSnapshot` added to `PerQuery` (B5), plus new task B8 for a read-only replay helper. Dissolves the F1a-vs-F1b fork; OD-1 disposition updated to "F1a with F1b-style replay path — best of both." Principle 1 vs Principle 5 tension noted as dissolved in §1.
- **Revision 2 (applied):** A1 lifted to a pre-refactor PR landing before Track B; A2/A3 + Track B then run in true file-disjoint parallel. §1 Fork F3 disposition updated to "F3a-prime"; §2 task-graph, critical path, parallelisation, and must-sequence lists updated.

Newly-added open questions for Critic (from Architect recommendations) — **all three resolved in iteration 2, see Resolved section below**.

---

### Resolved

- [x] **OD-5 — Gold-set metric shape (Critic ruling, 2026-04-21, classifier-matcher-foundation iteration 2)** — **Ruling: macro-averaged per-class accuracy with dual floors: per-class ≥90% AND macro ≥95%. Raw accuracy is OUT.** Gold-set construction rule is now binding: ≥5 entries per class (YES and NO); imbalance >70/30 rejected by the loader. Enforced in B4 preflight (loader refuses to run the gold set when the balance rule is violated; abort/warn on either floor breach). See `classifier-matcher-foundation.md` §5 OD-5 row, §7 Risks "Gold-set class imbalance" row, B4 description, C1 description, and §11.1 pre-mortem scenario 2.
- [x] **OD-6 — Deliberate-mode escalation (Critic ruling, 2026-04-21, classifier-matcher-foundation iteration 2)** — **Ruling: deliberate mode FIRES.** §11 (pre-mortem + expanded test plan) promoted from "Optional" framing to live binding numbered sections (§11.1, §11.2, §11.3) with acceptance gates that block consensus closure. Classifier weight-digest fetch-time verification added to B4 preflight against a new pinned-digests file `cmd/resolver/data/gold-sets/classifier-pins.yaml` (closes the silent-re-pull gap). See `classifier-matcher-foundation.md` §5 OD-6 row, §7 Risks new "Classifier weights silently re-pulled" row, B4 description, and §11.
- [x] **OD-1 residual — Replay snapshot completeness (Critic-driven edit, 2026-04-21, classifier-matcher-foundation iteration 2)** — **Resolved:** `ClassifierInputSnapshot` extended from 3 to 4 fields by adding `ClassifierParamsHash string` covering sha256 of canonicalised JSON `{temperature, top_p, max_tokens, seed}` as passed to the classifier adapter on each call. Without it, replay is not bit-reproducible. See `classifier-matcher-foundation.md` B5 description and §11.2 unit-test gate for snapshot serialisation including `ClassifierParamsHash`.
- [x] **Tool-calling preamble placement (2026-04-20)** — Decision: **per-role**, not a shared global preamble. Rationale: some roles (reducer-json, reducer-sexp, classifier) do NOT call tools at all; putting the tool-calling hints in their system prompts would be incorrect guidance. Each role's `cmd/resolver/data/roles/<role>/system-prompt.md` includes the 3 hints ONLY when that role exercises tool-calling. The three hints to embed verbatim:
  1. "Complete your thought process entirely within `<think>` tags before providing the tool call in the required format. Do not nest tags or repeat the thought process after the tool call is generated."
  2. "Keep reasoning concise and focused on the immediate next step. Move to the tool call as soon as the plan is clear."
  3. "End your response immediately after the tool call. Do not provide post-call explanations."
  The shared `cmd/resolver/data/shared/system-prompts/tools-preamble.md` file mentioned in the plan **is not created**. Instead, the hints are baked into the tool-calling roles' own `system-prompt.md`. Manifest `prompt_rev` hashes the role-specific prompt body (not a shared preamble that doesn't exist).
- [x] **Classifier label inventory (2026-04-20)** — Decision: `graph_query` is a **separate label**, not an exec-subtype. Rationale: graph_query is fundamentally about memory/recency — it tests whether the model uses latest info instead of making assumptions from prior context. Conflating it with `exec` loses that signal. Final label set: `{exec, diagnose, refuse, escalate, hitl, graph_query}`. Classifier scenarios exercise each label with at least one prompt that is "decoy-tempted" toward another label.
- [x] **Archived-scorecard root-key rewrite (2026-04-20)** — Decision: **rewrite `summary` → `summary_v2_legacy`** on archive. Rationale: prevents naive cross-directory jq merges between v1-era and v2.1-era scorecards. Not a lot of data; trivial to reproduce if anything breaks. Phase 8 archive step applied this rewrite; see `research/captures-v1/README.md` for the shape-detector one-liner. (Previously flagged as optional mitigation to R10; promoted to required and shipped.)
- [x] **min_p sidecar handling (2026-04-20)** — Decision: **add `MinP *float64` to the `RunConfig` sidecar struct** in manifest v3. Shipped in `d088e38` (Phase 4). Archived gresh-reasoner captures predate the field and are documented as a known reproducibility gap in `research/captures-v1/README.md`.
