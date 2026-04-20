# Open Questions Tracker

Centralised list of unresolved questions and deferred decisions across plans. Append-only; mark items `[x]` when resolved and move to `### Resolved`.

---

## resolver v2.1 role-organised test suite — 2026-04-20

- [ ] **Tool-calling preamble wording** — Exact verbatim text of the 3 user-validated tool-calling hints to paste into `cmd/resolver/data/shared/system-prompts/tools-preamble.md`. Blocks Phase 3 step 11. User to provide. (See `resolver-v2-1-plan.md` §12 item 1.)
- [ ] **Classifier label inventory** — Confirm `{exec, diagnose, refuse, escalate, hitl}` covers all intents; in particular whether `graph_query` is a separate class or an exec-subtype. (See `resolver-v2-1-plan.md` §12 item 2.)
- [ ] **hitl role threshold** — Spec leaves this implicit. Planner proposes 60% informational. Confirm or mark role ungated. (See `resolver-v2-1-plan.md` §12 item 3.)
- [ ] **reducer-sexp directory shape** — Ship as 0-scenario placeholder in v2.1, or defer role-dir creation entirely to v2.2? Current plan ships the placeholder. (See `resolver-v2-1-plan.md` §12 item 4.)
- [ ] **min_p sidecar handling** (Critic scenario H) — Add `MinP *float64` to the `RunConfig` sidecar struct now, or document as a known gap in `research/captures-v1/README.md`? Planner leans toward adding the field (small schema addition, durable audit). Architect to confirm before Phase 8 starts. (See `resolver-v2-1-plan.md` §12 item 5 + §6 scenario H.)

---

### Resolved

(none yet)
