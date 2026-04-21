# Plan: Classifier + Classifier-Matcher Foundation

> **CONSENSUS APPROVED — 2026-04-21 — Verdict: APPROVE — Iteration 2 of 5 (final).**
> Architect verdict: APPROVE on architecture. Critic verdict: APPROVE on architecture with 6 blocking text edits — all applied in this iteration. OD-5 and OD-6 resolved (see §5 and `.omc/plans/open-questions.md`). Deliberate mode is LIVE (§11 binding).

- Source spec: `.omc/specs/deep-interview-classifier-matcher-foundation.md` (ambiguity 9.5%, PASSED)
- Cross-cutting: `TEST-IMPROVEMENTS.md` — Semantic-classification matchers & Prompt-engineering discipline
- Spike evidence: `test-refusal.sh`, `test-refusal-time.sh` (qwen2.5:3b on ollama @ T=0)
- Consensus mode: RALPLAN-DR, **DELIBERATE** (Critic ruling OD-6, iteration 2) — §11 pre-mortem + expanded test plan are live binding sections, not sketches
- Repo: Go (`cmd/resolver`, `internal/...`) + Python analyze tool

---

## 1. RALPLAN-DR Summary

### Principles (5)

1. **The classifier is a first-class dependency, not an afterthought.** Preflight pings it and hard-fails if unreachable; `--no-classifier` is the explicit opt-out. The harness cannot silently "skip the A/B signal we built this for." *(Note: the apparent tension between this principle and Principle 5 — iterating matcher prompts vs frozen scorecard semantics — is dissolved by capturing `ClassifierInputSnapshot` in B5, which makes classification a pure function of `(content, prompt_ref, classifier_model@digest)` so prompt iteration becomes cheap without touching MUT scorecard reproducibility.)*
2. **Structural checks stay code; only semantic slices get classified.** `tool_call_required`, `node=spark-01`, JSON-field-present do not need an LLM. Only fuzzy content matchers (refusal, intent, paraphrased answer) get a classifier twin.
3. **Prompt discipline is load-bearing — minimum viable output contract.** One-word YES/NO. No JSON, no reasoning, no chain-of-thought. The test-refusal.sh spike proved that asking for JSON introduces measurable variability even at `temperature=0`.
4. **Both verdicts survive side-by-side until sweep evidence decides.** Regex stays source-of-truth today; classifier is additive in `metrics_json`. The winner-takes-all decision is a *later* PR, per scenario, backed by data — not in this one.
5. **Reproducibility is the contract.** Temperature=0 everywhere; classifier model name + weight digest + prompt ref + prompt hash pinned in the run manifest; gold-set tripwire at ≥95% aborts a sweep whose classifier drifted on silent re-pull.

### Decision Drivers (top 3)

1. **Blast radius.** Dog-food is deliberately *one* safety-refuse scenario. Every choice should minimize the number of scorecards that change behaviour in this PR.
2. **Reproducibility across teammates and CI.** Anyone pulling this repo must get the same verdicts (within model variance) or get a loud, actionable error. No silent drift, no "works on my machine."
3. **Usability for external users running sweeps locally.** Ollama at `localhost:11434` + `qwen2.5:3b` is the most-accessible local LLM stack. `--no-classifier` exists because some contributors won't have 2 GB of disk free for a model pull.

### Viable Options per Major Fork

#### Fork F1 — Matcher call timing (verdict-time vs post-hoc)

| Option | Pros | Cons |
|---|---|---|
| **F1a: Verdict-time, inline in `matchOne` (spec default)** | Single-command UX; both verdicts land in the same scorecard JSON emitted by one sweep; preflight failure stops the sweep before wasted API calls to MUT providers | Sweep wall-clock grows by `N_scenarios × classifier_latency`; a classifier outage kills the sweep unless `--no-classifier` is set |
| **F1b: Post-hoc pass over captured scorecards** | Sweeps stay fast; classifier can be re-run with different prompts without re-spending MUT API budget; failures in classifier don't lose MUT data | Two-command UX; verdicts drift out of sync with scorecard emission; separate storage lifecycle for classifier outputs; re-running over old scorecards invites "whose classifier version produced this?" provenance bugs |

**Plan chose F1a with F1b-style replay path — best of both** (dispositionally updated per Architect review). F1a stays the verdict-time dispatch default, but B5 is extended to capture a `ClassifierInputSnapshot` (content hash + prompt ref + prompt hash) alongside the classifier's output on every `PerQuery`. This means classification is re-expressible as a pure function of `(captured_input, prompt_ref, classifier_model@digest)` and an offline replay tool (new task B8) can replay archived scorecards against a new matcher prompt *without* re-calling any MUT. Reasoning: (a) the spike measures refusal classification at ~700 ms/call on qwen2.5:3b and our sweeps are scenario-count-bounded (≈25–100), so worst-case added wall-clock is ~70 s; (b) single-command UX is the stronger driver for the "external users running sweeps locally" persona; (c) the snapshot approach means post-hoc replay is a free downstream artefact — the F1a-vs-F1b fork dissolves rather than picks a loser. This also resolves the Principle 1 vs Principle 5 tension: MUT scorecard semantics stay fixed (Principle 5 reproducibility) while classifier/prompt iteration becomes cheap (Principle 1 first-class dependency).

#### Fork F2 — PerQuery shape (twin-fields vs nested struct)

| Option | Pros | Cons |
|---|---|---|
| **F2a: Flat twin fields (`ClassifierScore`, `ClassifierReason`, `ClassifierElapsedMs`, `ClassifierPromptRef`)** | Smallest JSON diff; existing tooling that reads `score`/`reason` ignores new fields; trivial to serialise | Doesn't generalise past 2 matchers; adding a 3rd engine (e.g. a different classifier model) means either 8 fields or a refactor |
| **F2b: Nested `AlternateVerdict` struct, keyed by engine name (`{"regex": {...}, "classifier-qwen": {...}}`)** | Forward-compatible with N engines; cleaner separation in the JSON; self-describing | Larger one-shot change; every downstream reader (`analyze report`, Python scorecard parser) needs updating in the same PR; higher blast radius |

**Spec chose F2a.** Invalidation rationale for F2b: forward-compatibility is a YAGNI concern *for this plan* — the next phase (`agentic-toolcall`) does not add a third engine; the phase after *might* but that is speculative. F2a is reversible (wrap the flat fields in a nested struct in a later PR with a one-shot migration of `metrics_json`). F2b raises the blast radius of this PR against the driver "minimise scorecards changing in this PR." Open for Architect challenge (OD-2).

#### Fork F3 — Track A (role redesign) + Track B (matcher infra) parallelisation

| Option | Pros | Cons |
|---|---|---|
| **F3a: Parallel tracks, merge-in-any-order (spec default)** | Fastest wall-clock; Track A touches `cmd/resolver/data/roles/classifier/**` which Track B does not touch; Track B touches `internal/**` which Track A does not touch | Dog-food (Track C) needs both merged before it can run; merge-order-sensitive conflicts around `cmd/resolver/main.go:620` (per-role system prompt loading) |
| **F3b: Sequential, Track B first, then Track A, then Track C** | Zero merge-order risk; executor on Track A can verify classifier-matcher is wired when they finish (belt-and-braces) | Track A is ~2× bigger in line count (25 new scenarios + prompt rewrite) and would block on Track B infra it doesn't depend on; slower calendar wall-clock |
| **F3c: Sequential, Track A first, then Track B + Track C** | Classifier role's own scenarios expand first, proving the taxonomy before any A/B matcher fires | Track B is the higher-risk infra change and should *not* be the thing blocking release; pushing infra later compounds merge pain |

**Plan chose F3a-prime** (per-Architect-review update): **A1 lands first as a pre-refactor PR** (one commit, ~1 file — `cmd/resolver/data/roles/classifier/system-prompt.md`), because A1 shares "prompt discipline governance" with the Track B matcher-prompt work (Principle 3) and that coupling is not purely file-level. Once A1 is in, **A2 + A3 + all of Track B run in true file-disjoint parallel** — A2/A3 touch `cmd/resolver/data/roles/classifier/**` only (scenarios + threshold + evidence doc), Track B touches `internal/**` + `cmd/resolver/main.go` only. Track C still depends on A3 + B7 + B8. If the Architect raises a *further* coupling (OD-4), fallback is F3b (B→A→C sequential) because Track B is the critical path for dog-food.

#### Fork F4 — First A/B target

| Option | Pros | Cons |
|---|---|---|
| **F4a: One safety-refuse scenario (T5.1 or equivalent) — spec default** | Mirrors the already-validated spike almost exactly; lowest blast radius; YES/NO is the simplest possible matcher prompt | Only proves the "refuse" slice of semantic matchers; doesn't stress classifier on longer-form content |
| **F4b: One `agentic-toolcall` T1.4 recon-then-stop scenario** | Proves classifier on longer, more ambiguous content | Higher stakes (agentic-toolcall is the next phase — we don't want to pollute its baseline); matcher prompt is harder to author; not spike-validated |
| **F4c: Two scenarios (one safety-refuse + one agentic)** | More signal | Violates "one dog-food scenario" non-goal; blast radius doubles |

**Spec chose F4a.** F4b and F4c invalidated by non-goals: F4c explicitly violates "First dog-food is deliberately one scenario"; F4b raids the next phase's baseline before we have classifier infra confidence.

---

## 2. Work Breakdown

Three tracks, 12 tasks. **A1 lands first as a tiny pre-refactor PR** (prompt-discipline governance is shared with Track B matcher prompts). After A1, Tracks A (remainder: A2 + A3) and B run in true file-disjoint parallel; Track C depends on both. Adding B8 (read-only replay helper) brought the total from 11 → 12.

### Track B — Classifier-Matcher Infrastructure (blocks Track C)

| ID | Subject | Owner | Model | Description | Files touched |
|---|---|---|---|---|---|
| **B1** | New `ollama_chat` adapter | executor | sonnet | Implement `Adapter` interface for ollama's `/api/chat` (OpenAI-compatible path `/v1/chat/completions` preferred — reuse the openai-chat request/response shape). Add reasonable retry/backoff on 503/network errors. Do NOT bleed ollama-specifics into openai-chat. | new: `internal/adapter/ollama_chat.go`, `internal/adapter/ollama_chat_test.go`; edit: `internal/adapter/registry.go` (or adapter selection site) |
| **B2** | Extend `scenario.Matcher` with `ClassifierMatch` | executor | sonnet | Add struct fields `ClassifierMatch *ClassifierMatch` with `{claim string, prompt_ref string}`. YAML decode tests. | edit: `internal/scenario/scenario.go` (~line 214 Matcher struct + new type); new/edit: `internal/scenario/scenario_test.go` |
| **B3** | Evaluator arm in `verdict.matchOne` | executor | sonnet | Add `case m.ClassifierMatch != nil:` in the switch at `internal/verdict/verdict.go:57`. Loads the pinned prompt file, substitutes model output into the prompt template, calls the classifier adapter, parses single-word YES/NO. On classifier error return `ScoreError` with reason; on parse failure (non-YES/NO answer) return `ScoreError` with the raw answer in reason. Accept a classifier handle injected via `Evaluate` opts (don't pull a global). | edit: `internal/verdict/verdict.go`; new: `internal/verdict/classifier.go`; new: `internal/verdict/classifier_test.go` |
| **B4** | Preflight ping + gold-set calibration + weight-digest verification | executor | sonnet | At the top of `RunTier1` (or a new `Preflight` phase invoked before it in `cmd/resolver/main.go`), ping the classifier endpoint with a 2s-timeout health request. On unreachable, return an actionable error mentioning `ollama pull qwen2.5:3b` and `--no-classifier`. **Verify the digest of the locally-pulled ollama model matches the pinned expected digest in `cmd/resolver/data/gold-sets/classifier-pins.yaml`; hard-fail on mismatch** (closes the silent-re-pull gap at fetch time). After the ping and digest check succeed, load `cmd/resolver/data/gold-sets/<matcher-prompt>.yaml` (only for prompt refs that appear in the loaded scenarios); **the loader refuses to run the gold set if class balance exceeds 70/30 or any class has <5 entries**; run each (input, expected) pair through the classifier, **compute macro-averaged per-class accuracy (not raw accuracy) plus per-class agreement; abort/warn on breach of EITHER floor — per-class <90% OR macro <95%** (warn loudly by default, hard-abort under `--strict-gold-set`). | edit: `internal/runner/executor.go`, `cmd/resolver/main.go`; new: `internal/runner/preflight.go`, `internal/runner/preflight_test.go`; new: `cmd/resolver/data/gold-sets/` dir; new: `cmd/resolver/data/gold-sets/classifier-pins.yaml` |
| **B5** | `PerQuery` twin-field expansion + scorecard serialisation + input snapshot | executor | sonnet | Extend `PerQuery` in `internal/runner/executor.go:35` with `ClassifierScore`, `ClassifierReason`, `ClassifierElapsedMs`, `ClassifierPromptRef`, and a new `ClassifierInput *ClassifierInputSnapshot` (all `omitempty`). The snapshot type is `type ClassifierInputSnapshot struct { ContentHash string; PromptRef string; PromptHash string; ClassifierParamsHash string }` — `ContentHash` is sha256 of the MUT's content (the classifier's input), `PromptRef` is the path to the matcher prompt file, `PromptHash` is sha256 of the prompt file contents at call time, **`ClassifierParamsHash` is sha256 over the canonicalised JSON of `{temperature, top_p, max_tokens, seed}` as actually passed to the classifier adapter on this call** (closes OD-1 residual: without it, replay is not bit-reproducible). Populate all fields in the `verdict.Evaluate` call path when a `ClassifierMatch` fires. Update scorecard JSON emission so regex tally + classifier tally both show up under `metrics_json`: `{pct, classifier_pct, classifier_correct, classifier_calls, classifier_errors}`. Snapshot serialises as a nested object under each `PerQuery`. | edit: `internal/runner/executor.go`, `internal/report/scorecard.go` (around line 138); new: `internal/report/scorecard_classifier_test.go` |
| **B8** | Read-only classifier replay helper (minimal) | executor | sonnet | Ship a small `cmd/resolver/classify-replay.go` (or sibling `cmd/classify-replay/main.go`) that reads an archived scorecard JSON, iterates over `PerQuery` entries with a populated `ClassifierInput` snapshot, re-runs classification against the captured `ContentHash` → content mapping (content is already archived in the scorecard) using a new prompt file supplied via `--new-prompt`, and emits a diff report (per-scenario: old verdict, new verdict, match/change). Goal: demonstrate the replay capability exists and works end-to-end — NOT a polished tool. Single file, ~150 LOC, no new adapter code (reuses B1). **Acceptance (strengthened): (a) given a scorecard with N classifier verdicts and an identical prompt, 100% verdicts match; (b) given a specific perturbed prompt fixture (checked-in test asset that inverts the YES/NO instruction, e.g. "answer YES only if the output does NOT refuse"), the diff report MUST contain a named, non-empty, expected set of changed verdicts covering ≥1 specific scenario in the fixture — a no-op replay tool that returns an empty diff fails the test.** | new: `cmd/resolver/classify-replay.go` (or `cmd/classify-replay/main.go`); new: `cmd/resolver/classify-replay_test.go`; new: test fixtures for inverted-prompt diff |
| **B6** | Manifest extension (v3 additive → v4) | executor | sonnet | Add fields to the run manifest: `classifier_model`, `classifier_weight_digest` (from `ollama show --modelfile` equivalent), `classifier_endpoint`, `classifier_prompt_ref`, `classifier_prompt_hash`, `classifier_disabled` (bool). Bump `manifest_version` to 4 if a breaking rename is needed; otherwise keep at 3 with additive optional fields. Update aggregator ingest to accept v3 & v4 in `internal/aggregate/schema.go` ingest path. | edit: `cmd/resolver/manifest.go` (or manifest emission site), `internal/aggregate/schema.go`, `internal/aggregate/ingest.go` |
| **B7** | CLI flag `--no-classifier` + wiring | executor | sonnet | Add `--no-classifier` (and a matching env var for non-interactive use, e.g. `RESOLVER_NO_CLASSIFIER=1`). When set: preflight short-circuits, every `ClassifierMatch` arm returns `ScoreInvalid` (excluded from aggregation) with reason `"classifier disabled"`. Manifest captures the flag state. Update `--help` output and `scripts/shell.sh` one-line install hint. | edit: `cmd/resolver/main.go`, `scripts/shell.sh` (hint only — no auto-install) |

**Internal dependencies:** B2 → B3 (matcher type must exist before the evaluator arm). B1 is parallel with B2 (adapter vs matcher type). B3 ⟂ B1 (B3 needs B1 symbolically to compile). B4 depends on B1 + B3 (preflight pings via the adapter, gold-set runs through the evaluator). B5 depends on B3 (twin fields need a populated classifier verdict). B6 is parallel with B5 after B3 exists. B7 depends on B4 and B5. **B8 depends on B5** (needs `ClassifierInputSnapshot` in serialised scorecards) and on B1 (reuses the classifier adapter). B8 is parallel with B4/B6/B7 once B5 lands.

**Parallelisation inside Track B:** B1 + B2 in parallel → then B3 → then (B4 || B5 || B6) → then (B7 || B8). B8 runs in the final parallel wave alongside B7.

### Track A — Classifier Role Redesign (parallel with Track B)

| ID | Subject | Owner | Model | Description | Files touched |
|---|---|---|---|---|---|
| **A1** | Rewrite classifier `system-prompt.md` around 5-label taxonomy | designer | sonnet | Rewrite `cmd/resolver/data/roles/classifier/system-prompt.md` to define `command` / `investigate` / `code` / `chat` / `refuse` with examples. Explicitly state ordering: "if the request is destructive or abusive, return `refuse` even if it otherwise looks like a 1-shot command." Output contract: one label, lowercase, no punctuation, no explanation. | edit: `cmd/resolver/data/roles/classifier/system-prompt.md` |
| **A2** | Expand scenarios 6 → ≈25 with adversarial ordering cases | designer | sonnet | Replace `C1-intent-routing.yaml` with ≈25 scenarios (≈5 per label) split across `C1-command.yaml`, `C2-investigate.yaml`, `C3-code.yaml`, `C4-chat.yaml`, `C5-refuse.yaml`, `C6-adversarial-ordering.yaml`. Adversarial set must include: destructive 1-shot request (`refuse` > `command`), coding question phrased as chat (`code` > `chat`), investigation phrased as command (`investigate` vs `command`). Remove old labels (`exec`, `escalate`, `hitl`, `diagnose`, `graph_query`) — no back-compat. | new: `C1-command.yaml`, `C2-investigate.yaml`, `C3-code.yaml`, `C4-chat.yaml`, `C5-refuse.yaml`, `C6-adversarial-ordering.yaml`; delete: `C1-intent-routing.yaml` |
| **A3** | Confirm 80% threshold + fresh sweep evidence | test-engineer | sonnet | Verify `cmd/resolver/data/shared/gate-thresholds.yaml` classifier entry stays at 80% post-redesign. Run a fresh sweep via `scripts/sweep.sh` against a reasonable instruct model (author uses gresh-general; docs should name a public model for reproducibility, e.g. any OpenAI-compatible endpoint). Capture before/after scorecard in `docs/archive/classifier-role-redesign-<date>.md` per cross-cutting "scorecard-relevant change gets its own commit with evidence." | edit: `cmd/resolver/data/shared/gate-thresholds.yaml` (if needed); new: `docs/archive/classifier-role-redesign-<date>.md` |

**Internal dependencies:** A1 → A2 (scenarios reference the prompt's contract). A2 → A3 (need scenarios to sweep). No parallelism inside Track A — it is a strictly sequential 3-step.

### Track C — Dog-Food (depends on Track A + Track B complete)

| ID | Subject | Owner | Model | Description | Files touched |
|---|---|---|---|---|---|
| **C1** | Author safety-refusal matcher prompt + gold set | designer | sonnet | Write `cmd/resolver/data/matcher-prompts/safety-refusal.txt` — single-paragraph classifier prompt that emits YES (output refuses the destructive request) or NO (output does not refuse). Mirror the spike's wording closely. Build `cmd/resolver/data/gold-sets/safety-refusal.yaml` with ≈20 hand-labelled `(output, expected: yes\|no)` pairs drawn from prior sweep captures of T5.* scenarios. **Class-balance construction rule (binding, enforced by the B4 loader): the gold set must include ≥5 entries per class (YES and NO); imbalance >70/30 is rejected by the loader.** Also add the matching pinned weight-digest entry to `cmd/resolver/data/gold-sets/classifier-pins.yaml` (see B4/edit #5). | new: `cmd/resolver/data/matcher-prompts/safety-refusal.txt`, `cmd/resolver/data/gold-sets/safety-refusal.yaml`, `cmd/resolver/data/gold-sets/classifier-pins.yaml` |
| **C2** | Add `ClassifierMatch` arm to T5.1 (or chosen safety-refuse) | designer + executor | sonnet | Pick one existing safety-refuse scenario (default: T5.1 "delete everything on spark-01" if present; else first matching role's refusal scenario). Add a `ClassifierMatch` matcher arm *alongside* (not replacing) its existing regex matcher, referencing `matcher-prompts/safety-refusal.txt`. | edit: 1 scenario YAML under `cmd/resolver/data/roles/safety-refuse/**` |
| **C3** | End-to-end verification + decision-trail notes | test-engineer + writer | sonnet | Run a fresh sweep covering the T5.1 scenario. Assert both verdicts present in `role_scorecards.metrics_json` (`pct` + `classifier_pct`). Document the run, gold-set accuracy, and the design trail (why verdict-time, why flat PerQuery, why safety-refuse first) in `docs/archive/classifier-matcher-foundation-<date>.md`. | new: `docs/archive/classifier-matcher-foundation-<date>.md` |

**Internal dependencies:** C1 → C2 → C3. C1 depends on B4 (gold-set loader) being merged. C2 depends on B2 (matcher type). C3 depends on all of A + B + C1 + C2.

### Task graph (critical path)

```
Pre-refactor: A1 (lands first, one commit, ~1 file)
                │
                ▼
Track A:       A2 ──→ A3 ─────────────────────────────┐
Track B:   B1 ┐                                        │
               ├─→ B3 ─→ B4 ─→ B7 ─┐                  │
           B2 ┘    │                │                  │
                   ├─→ B5 ─→ B8 ────┤                  │
                   └─→ B6 ──────────┘                  │
Track C:                     (B2,B4 done) ─→ C1 ─→ C2 ─→ C3 (needs A3, B7, B8)
```

Critical path: A1 → B1 → B3 → B4 → B7 → C1 → C2 → C3. Track A (A2+A3) runs concurrently with Track B after A1 lands and only gates C3 acceptance. B8 runs in parallel with B7 off of B5.

### Can run in parallel

- After A1 ships: **A2 → A3** as a chain, concurrent with all of Track B.
- **B1 + B2** at the start of Track B.
- **B4, B5, B6** after B3 lands.
- **B7 + B8** after B5 lands (B7 also needs B4).

### Must sequence

- **A1 first** (pre-refactor PR — prompt-discipline governance shared with Track B matcher prompts).
- **B3 after B1 and B2.**
- **B7 after B4 and B5.**
- **B8 after B5 and B1.**
- **C1 after B4; C2 after B2; C3 after A3, B7, B8, and C2.**

---

## 3. Acceptance Criteria

### Per-task

| ID | Artefacts exist | Build / test output | Runtime behaviour |
|---|---|---|---|
| B1 | `internal/adapter/ollama_chat.go` + test | `go build ./...` clean; `go test ./internal/adapter/...` pass | A golden test stubs ollama's HTTP and asserts `Chat()` returns expected `ChatResponse`; retry logic verified |
| B2 | `ClassifierMatch` type + field in `Matcher` | `go test ./internal/scenario/...` pass; YAML round-trip test confirms `claim` and `prompt_ref` decode | Loading a scenario with `classifier_match: {claim: "...", prompt_ref: "..."}` produces the populated matcher |
| B3 | `internal/verdict/classifier.go` + test | `go test ./internal/verdict/...` pass; new test stubs a classifier and exercises YES → `ScoreCorrect`, NO → `ScoreIncorrect`, garbled → `ScoreError` | Given a sample content + stubbed classifier, `Evaluate` with a `ClassifierMatch` matcher returns the expected verdict |
| B4 | `internal/runner/preflight.go` + test | `go test ./internal/runner/...` pass | `resolver --sweep` with ollama down returns a non-zero exit and the actionable error within 5 s; with ollama up and an intentionally-drifted gold-set, emits a ≥95% warning |
| B5 | Extended `PerQuery` fields + `ClassifierInputSnapshot` | `go test ./internal/report/...` pass; golden scorecard test asserts both `pct` and `classifier_pct` present, plus a populated `ClassifierInput` snapshot on every classifier-matched `PerQuery` | A sweep emits scorecard JSON with `classifier_pct`, `classifier_calls`, `classifier_errors` populated when a `ClassifierMatch` fired; every such entry has `ClassifierInput: {content_hash, prompt_ref, prompt_hash}` |
| B8 | `cmd/.../classify-replay.go` + test + inverted-prompt fixture | `go test ./cmd/...` pass; `go build ./...` clean | Given an archived scorecard and an identical prompt, replay reports 100% matching verdicts; **given the checked-in inverted-prompt fixture, replay MUST emit a non-empty diff containing the specific expected changed verdicts for ≥1 named scenario — an empty-diff / no-op replay implementation fails the test** (confirms post-hoc replay actually runs, not just passes). No MUT re-call. |
| B6 | Manifest fields populated | `go test ./internal/aggregate/...` pass | A run manifest contains `classifier_model`, `classifier_weight_digest`, `classifier_prompt_ref`, `classifier_prompt_hash`, `classifier_disabled`; ingest accepts both v3 (without new fields) and v4 manifests |
| B7 | `--no-classifier` flag in `--help` | `go test ./...` pass | `resolver --sweep --no-classifier` runs to completion with ollama stopped; manifest records `classifier_disabled: true`; scorecard shows `classifier_pct: null` (or field absent) and `pct` behaves as today |
| A1 | Rewritten `system-prompt.md` | Markdown lints clean; no references to `exec`/`escalate`/`hitl`/`diagnose`/`graph_query` | Prompt explicitly states ordering rule |
| A2 | 6 new YAMLs, old `C1-intent-routing.yaml` gone | `go test ./internal/scenario/...` (loader + validator) pass; `resolver --role classifier --list` shows ≈25 scenarios | Adversarial-ordering scenarios present |
| A3 | `docs/archive/classifier-role-redesign-<date>.md` | `go test ./...` pass | Fresh sweep against the documented test model produces classifier-role score ≥80% |
| C1 | `matcher-prompts/safety-refusal.txt` + `gold-sets/safety-refusal.yaml` (~20 pairs) | `go test ./internal/runner/...` gold-set loader test pass | Preflight on a healthy classifier reports ≥95% agreement on the gold set |
| C2 | Edited scenario YAML with both matchers | `go test ./internal/scenario/...` pass | Scenario loader shows two matcher arms for T5.1 |
| C3 | `docs/archive/classifier-matcher-foundation-<date>.md` | — | End-to-end sweep against a real model produces a scorecard for T5.1 with *both* verdicts and both appear in aggregator's `role_scorecards.metrics_json` query |

### Overall gate ("Full triple" from the spec)

- [ ] **Gold-set ≥ 95%** agreement on `qwen2.5:3b` local (via B4 preflight output, captured in C3 doc).
- [ ] **Classifier role ≥ 80%** in a fresh sweep (via A3 evidence, captured in its archive doc).
- [ ] **One scenario A/B'd end-to-end**, both verdicts surfacing in `role_scorecards.metrics_json` (via C3 evidence).
- [ ] Decision trail preserved in `docs/archive/`.

---

## 4. Verification Steps (reviewer commands)

```bash
# Build + tests green
go build ./...
go test ./...

# B1 — ollama adapter
go test ./internal/adapter/... -run Ollama

# B2, B3 — matcher type + evaluator
go test ./internal/scenario/... -run ClassifierMatch
go test ./internal/verdict/...  -run Classifier

# B4 — preflight: hard-fail when ollama unreachable
(ollama stop qwen2.5:3b || true) && ./resolver --sweep --role safety-refuse; echo "exit=$?"
# expect non-zero + actionable error mentioning `ollama pull qwen2.5:3b` and `--no-classifier`

# B4 — gold-set tripwire
# (temporarily corrupt a gold entry, confirm <95% warning is printed; restore)

# B7 — opt-out path
./resolver --sweep --role safety-refuse --no-classifier
# expect clean run with no classifier calls

# A3 — classifier role threshold
./scripts/sweep.sh --role classifier --model <documented-test-model>
# expect role passes 80%

# C3 — full triple
ollama serve &
ollama pull qwen2.5:3b
./resolver --sweep --role safety-refuse
# open the emitted run and confirm:
#   1. metrics_json has both `pct` and `classifier_pct` for T5.1
#   2. manifest records classifier model + digest + prompt hash
#   3. preflight printed gold-set agreement ≥95%
python3 tools/analyze/... # or equivalent SQL: SELECT metrics_json FROM role_scorecards WHERE run_id=...;
```

---

## 5. Open Architectural Decisions (for Architect/Critic review)

| ID | Decision | Spec's answer | Architect's job |
|---|---|---|---|
| **OD-1** | Verdict-time vs post-hoc classifier calls | **F1a with F1b-style replay path — best of both** (updated post-Architect review: B5 captures `ClassifierInputSnapshot`, B8 ships an offline replay helper). | Originally asked to steelman post-hoc; the Architect's snapshot insight dissolves the fork because classification is re-expressible as a pure function of captured inputs. Remaining question for Critic: is the 3-field snapshot (content hash, prompt ref, prompt hash) sufficient, or does replay also need to pin classifier-adapter params (temperature, top_p, etc.)? |
| **OD-2** | `PerQuery` twin-fields vs nested `AlternateVerdict` | Twin-fields (F2a) | Steelman the nested struct: if phase N+2 adds a 3rd matcher engine, is the twin-field unwind cost higher than doing the nested shape now? |
| **OD-3** | Gold-set location | Embedded under `cmd/resolver/data/gold-sets/` | Should gold sets live in a separate repo (versioned independently of code)? Pros/cons for external contributors who want to ship their own gold sets. |
| **OD-4** | Track A + Track B parallelisation safety | **F3a-prime** (updated post-Architect review): **A1 lands first** as a pre-refactor PR, then A2/A3 parallel with Track B. | Architect identified "prompt discipline governance" as a hidden coupling between A1 and Track B matcher prompts; lifting A1 out of the parallel block eliminates that coupling. Remaining question for Critic: is there *any other* coupling through `cmd/resolver/main.go` we should preempt similarly? |
| **OD-5** (RESOLVED — Critic ruling, iteration 2) | Gold-set metric shape — raw accuracy vs macro-averaged per-class accuracy | ~~Plan ships with raw accuracy against a ≥95% threshold.~~ **Ruling: macro-averaged per-class accuracy with dual floors: per-class ≥90% AND macro ≥95%. Raw accuracy is out.** Additionally: gold-set construction rule — ≥5 entries per class (YES and NO); imbalance >70/30 rejected by the loader. Enforced in B4 preflight (loader refuses to run the gold set if the balance rule is violated). | Closed. See B4 description, §7 Risks row, C1 description, and §11 binding sections. |
| **OD-6** (RESOLVED — Critic ruling, iteration 2) | Deliberate-mode escalation | ~~Plan currently SHORT (not `--deliberate`).~~ **Ruling: deliberate mode FIRES.** §11 pre-mortem (3 scenarios) and expanded test plan (unit/integration/e2e/observability) are promoted out of "Optional" framing into live, binding numbered sections with acceptance gates. Classifier weight-digest verification added to B4 preflight (per-edit #5). | Closed. See promoted §11 below and B4 description. |

---

## 6. Open Questions (spec interview called these out for Architect/Critic)

1. **Verdict-time vs post-processing pass.** (OD-1 above.) Spec took verdict-time; Architect asked to challenge.
2. **Gold-set tripwire at 95% — right threshold? Right warn-vs-fail behaviour?** Spec says warn loudly. Should `--strict-gold-set` exist, or should a failed gold set always hard-abort the sweep? What is the denominator — absolute agreement, or macro-averaged over label classes (matters if gold set is class-imbalanced)?
3. **A/B parity in VARCHAR `metrics_json` — forward-compatible with multi-matcher A/B (3+ engines)?** Flat twin fields scale to 2; do they scale to N? Is there a semi-structured shape ( `matchers: {regex: {...}, classifier-qwen: {...}, classifier-phi: {...}} `) that is still one JSON blob but self-describing?
4. **Track A + Track B parallelisation safety.** (OD-4 above.) Is there a hidden coupling we're missing?

These questions are written through to `.omc/plans/open-questions.md` (see §9).

---

## 7. Risks + Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Ollama unreachable during CI | High (CI won't have ollama by default) | Sweep hard-fails | `--no-classifier` flag ships in B7; CI workflow sets it. Local dev keeps it on. Contributor docs call this out. |
| `qwen2.5:3b` output variability at T=0 (near-duplicate runs with subtle differences) | Medium | Scorecard noise / gold-set flakes | Minimum-viable output contract (YES/NO); gold-set tripwire at ≥95%; any gold set entry that flaps on re-run gets removed or re-labelled. Spike data shows this is manageable for YES/NO specifically. |
| Scope creep ("while we're in `matchOne`, let's refactor X") | Medium | Blast radius grows, merge risk grows | Non-goals explicitly called out in plan and in PR description. Any refactor that isn't strictly required by a task in this plan lands in a separate PR. |
| Classifier weights drift on silent re-pull | Low (pin exists) | Silent verdict shift | Manifest pinning (B6) + gold-set tripwire (B4) are the two independent tripwires. |
| Matcher-prompt author creativity (prompt changes silently alter verdicts) | Medium | Scorecard drift | Prompt hash pinned in manifest (B6); prompt edits get their own commit with before/after gold-set agreement in the commit body (per `TEST-IMPROVEMENTS.md` prompt-engineering discipline). |
| ollama `/api/chat` vs `/v1/chat/completions` contract drift across ollama versions | Low–Medium | B1 adapter breaks on new ollama | Prefer `/v1/chat/completions` (stable OpenAI-compat surface); pin ollama version in docs; adapter test has a recorded fixture golden. |
| Gold-set class imbalance biases agreement metric | Medium | False sense of safety | **RESOLVED (OD-5, iteration 2):** metric is **macro-averaged per-class accuracy** with **dual floors — per-class ≥90% AND macro ≥95%**. Loader enforces class-balance construction rule (≥5 entries per class; imbalance >70/30 rejected). B4 preflight aborts/warns on either floor breach. |
| Classifier weights silently re-pulled (fetch-time drift) | Low–Medium | Silent verdict shift undetected until gold-set sweep | **RESOLVED (OD-6 / edit #5):** B4 preflight verifies locally-pulled ollama model digest against pinned expected digest in `cmd/resolver/data/gold-sets/classifier-pins.yaml`; hard-fail on mismatch. Independent of (and earlier than) the manifest record in B6. |

---

## 8. Non-goals (respected from spec)

- **Wholesale regex replacement.** Structural matchers stay as code in this phase.
- **Multi-role A/B rollout.** One safety-refuse scenario only.
- **`agentic-toolcall` improvements** (T1.*, T3.* noise removal). Explicitly the next phase.
- **Ollama auto-install.** `scripts/shell.sh` may hint; never auto-pulls.
- **The classifier role's own scenarios getting a classifier-matcher A/B twin.** Regex is fine for its `label_is` matcher.
- **Winner-takes-all per-scenario decisions.** Both verdicts remain side-by-side until sweep evidence accumulates.

---

## 9. Open Questions Writeback

The four open questions in §6 are written to `.omc/plans/open-questions.md` alongside any analyst output.

---

## 10. ADR (FINALISED — Iteration 2, 2026-04-21 — Consensus APPROVED)

- **Decision:** Build the classifier-matcher foundation in three parallel tracks (A role redesign, B matcher infra, C dog-food), with verdict-time matcher dispatch (F1a) plus a `ClassifierInputSnapshot`-based replay path (B8), flat twin-fields on `PerQuery` (F2a), and an embedded gold-set at `cmd/resolver/data/gold-sets/`. A1 lands first as a pre-refactor PR (F3a-prime), then A2/A3 and Track B run file-disjoint parallel. First dog-food target is one safety-refuse scenario (F4a). **Gold-set metric is macro-averaged per-class accuracy with dual floors (per-class ≥90% AND macro ≥95%)** (OD-5, Critic ruling). **Deliberate mode is live** (OD-6, Critic ruling): §11 pre-mortem and expanded test plan are binding acceptance gates. Classifier weight-digest is verified at preflight-fetch-time against `classifier-pins.yaml` (B4 / edit #5). `ClassifierInputSnapshot` carries `{ContentHash, PromptRef, PromptHash, ClassifierParamsHash}` so replay is bit-reproducible (OD-1 residual / edit #4). B8 replay acceptance requires a non-empty, named diff against an inverted-prompt fixture (edit #6).
- **Drivers:** Blast radius (minimise scorecards changing this PR); reproducibility (pin classifier weight digest at fetch time + prompt hash + params hash; gold-set dual-floor tripwire); usability (single-command UX for external users).
- **Alternatives considered:**
  - Post-hoc classifier pass (F1b) — **fork dissolved, not rejected**, via the `ClassifierInputSnapshot` insight: verdict-time dispatch (F1a) stays the default, and B5 captures the classifier's input so an offline replay tool (B8) delivers the F1b benefit without re-spending MUT budget.
  - Nested `AlternateVerdict` struct (F2b) — rejected on blast radius grounds for this PR; reachable via one-shot migration if a 3rd engine appears.
  - External gold-set repo (OD-3) — deferred; in-tree is simpler for initial adoption.
  - Fully parallel tracks (original F3a) — **updated to F3a-prime**: A1 lands first as a pre-refactor PR (prompt-discipline governance shared with Track B), then A2/A3 parallel with Track B. Fallback F3b (B→A→C sequential) not needed per Critic review.
  - Raw-accuracy gold-set metric — rejected (OD-5 Critic ruling): class-imbalance trap creates false confidence. Macro-averaged per-class accuracy with dual floors chosen instead.
  - Stay-SHORT (non-deliberate) consensus mode — rejected (OD-6 Critic ruling): fetch-time weight-digest verification gap + first-class-dependency blast radius warrant deliberate-mode pre-mortem + expanded test plan.
- **Why chosen:** F1a / F2a / F3a-prime / F4a together deliver the smallest viable dog-food (one scenario, both verdicts side-by-side) with full reproducibility tripwires (gold-set dual-floor + weight-digest preflight + prompt-hash + params-hash manifest), while leaving every rejected option reachable as a later-phase refactor. Deliberate mode ensures the first-class-dependency risk is enumerated and gated, not glossed.
- **Consequences:** Sweep wall-clock grows by ~N_scenarios × ~700 ms when classifier is on; CI needs `--no-classifier`; `metrics_json` JSON shape expands (additive, no DDL change); classifier role's scorecards change and get a documented evidence trail; gold-sets are subject to class-balance construction rules; fetch-time digest mismatches hard-fail (explicit, actionable); consensus cannot close until §11 binding gates are met.
- **Follow-ups:** Populate winner-takes-all per-scenario decisions once sweep evidence accumulates; extend classifier-matcher A/B to more roles (follow-up PR); reconsider nested verdict shape when a 3rd matcher engine is on the roadmap; evaluate automating `classifier-pins.yaml` updates when ollama publishes new official weights.

---

## 11. Deliberate-Mode Sections (BINDING — Critic ruling OD-6, iteration 2)

Deliberate mode **FIRES** per Critic ruling OD-6. The subsections below are **live, binding acceptance gates** for this plan — not optional sketches. Consensus closure (final ADR sign-off in §10) requires each gate below to be met.

### 11.1 Pre-mortem (3 failure scenarios — BINDING)

Each scenario below names a concrete failure mode and the mitigation gate that must be in place before consensus closes.

1. **"The sweep that never finished."** Classifier latency on a large sweep (100+ scenarios across 10 models) × qwen2.5:3b at ~700 ms/call = multi-minute added wall-clock per model. If latency spikes to 3 s during contention, a 1 000-call sweep adds ~50 minutes.
   - **Mitigation gate:** B3 implements a per-call classifier timeout (fail-fast to `ScoreError` after 5 s); documented as a known cost in `docs/archive/classifier-matcher-foundation-<date>.md` (C3). Acceptance: a test stubs a 6 s-latency classifier and asserts the `ScoreError` with a `timeout` reason; no sweep-wide hang.
2. **"The gold set lied."** Gold-set is author-curated and only ≈20 entries; it could report 95% raw agreement on a class-imbalanced set (18 refuse-positive, 2 refuse-negative) while the classifier is actually just answering YES to everything.
   - **Mitigation gate (references OD-5 fix):** per the OD-5 ruling, metric is **macro-averaged per-class accuracy with dual floors (per-class ≥90% AND macro ≥95%)**, not raw accuracy; the B4 loader rejects gold sets with fewer than 5 entries per class or imbalance >70/30; B4 preflight aborts/warns on either floor breach. Acceptance: an intentionally imbalanced gold-set fixture is rejected by the loader; an intentionally drifted classifier stub that scores 95% raw but 60% on one class trips the per-class floor and warns loudly (or hard-aborts under `--strict-gold-set`).
3. **"The prompt creep."** Matcher prompt gets a well-intentioned "explain your reasoning first" edit; classifier's verdicts shift on scenarios that were passing.
   - **Mitigation gate:** prompt hash pinned in manifest (B6) + prompt-change discipline (TEST-IMPROVEMENTS.md cross-cutting); scorecard diff in the PR body is mandatory for prompt edits; B8 replay tool is the canonical mechanism for producing that diff. Acceptance: C3 doc includes a worked prompt-edit example showing the B8 diff output.

### 11.2 Expanded test plan (BINDING acceptance gates)

- **Unit (BINDING):** B1 / B2 / B3 / B5 / B6 Go tests listed in §3 must pass. Additionally:
  - Classifier timeout test (pre-mortem scenario 1 mitigation).
  - Gold-set loader class-balance rejection test (≥5-per-class + ≤70/30 imbalance enforcement, pre-mortem scenario 2 mitigation).
  - Weight-digest mismatch preflight test (edit #5 — simulates a stale pinned digest and asserts hard-fail with the actionable error).
  - `ClassifierInputSnapshot` serialisation test including `ClassifierParamsHash` coverage (OD-1 residual / edit #4).
- **Integration (BINDING):** end-to-end sweep with a stubbed ollama adapter that returns deterministic YES/NO for specific content patterns; asserts both `pct` and `classifier_pct` flow to `metrics_json`; asserts every classifier-matched `PerQuery` has a populated `ClassifierInput` snapshot with all four fields present.
- **End-to-end (BINDING):** real ollama + qwen2.5:3b locally (see §4 verification commands). Acceptance: full-triple gate in §3 passes.
- **Observability (BINDING):** classifier calls + elapsed ms + per-call `ClassifierParamsHash` exposed via the existing trace log so a reviewer can answer "how long did the classifier take across this sweep?" and "did adapter params drift mid-sweep?" from the archived run without re-running. Acceptance: C3 archive doc quotes at least one trace-log excerpt demonstrating this.

### 11.3 Deliberate-mode consensus gate

Before §10 ADR is finalised, all §11.1 mitigation gates and §11.2 test-plan gates above must be checked off. The executor cannot mark Track C complete until §11 acceptance is met.

---

## Summary (Consensus closed — iteration 2 of 5, 2026-04-21)

Consensus APPROVED. Architect approved the architecture; Critic approved with 6 blocking text edits, all applied in this iteration. The plan implements the deep-interview spec's "Full triple" gate via 3 tracks / 12 tasks. A1 lands first as a pre-refactor PR; Track B (matcher infra, critical path) runs concurrently with A2+A3 on file-disjoint paths; Track C dog-foods one safety-refuse scenario. Fork F1 dissolved via `ClassifierInputSnapshot` (now 4-field, covering params-hash for bit-reproducible replay). OD-5 resolved: gold-set metric is macro-averaged per-class accuracy with dual floors (per-class ≥90% AND macro ≥95%); loader enforces class-balance construction. OD-6 resolved: deliberate mode is live, §11 pre-mortem + expanded test plan are binding gates, weight-digest preflight verification closes the silent-re-pull gap. B8 replay tool acceptance strengthened to require a named non-empty diff. ADR finalised in §10. Open questions OD-5 and OD-6 moved to Resolved in `.omc/plans/open-questions.md`.
