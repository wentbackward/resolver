# Classifier Role Redesign — 2026-04-21

**Plan:** `.omc/plans/classifier-matcher-foundation.md` (Track A, tasks A1–A3)
**Commits:** A1 `62f7fad`, A2 `160f5a1` (YAML fix in same tree)
**Gate threshold:** `classifier` at **80%** — confirmed unchanged in
`cmd/resolver/data/shared/gate-thresholds.yaml`

---

## Summary

Track A of the classifier-matcher-foundation plan redesigned the classifier role
around a new 5-label taxonomy and expanded its scenario coverage from 6 → 31.
This document records the before/after state and the reproduction command for
future sweeps.

---

## Before (pre-A1)

**File:** `cmd/resolver/data/roles/classifier/C1-intent-routing.yaml` (now deleted)

**Labels (6):** `exec` · `diagnose` · `refuse` · `escalate` · `hitl` · `graph_query`

**Scenarios (6):** one per label, no adversarial ordering cases, no code/chat
distinction, no prompt ordering rule.

**System-prompt issues:**
- No explicit ordering rule (e.g. `escalate` vs `hitl` disambiguation was
  partial and only covered two labels)
- Mixed-concern labels (`escalate`, `hitl`) that blend safety semantics with
  routing semantics
- No output-contract statement beyond "one label, no explanation"

---

## After (post-A1 + A2)

### System prompt (`cmd/resolver/data/roles/classifier/system-prompt.md`)

- **5 labels:** `command` · `investigate` · `code` · `chat` · `refuse`
- **Ordering rule (binding):** evaluate `refuse` first; among the remaining four
  use `code > investigate > command > chat` priority when a request fits
  multiple labels
- **Output contract (explicit):** one label, lowercase, no punctuation, no
  explanation, no surrounding text — matching the prompt-discipline governance
  from Plan Principle 3 and `TEST-IMPROVEMENTS.md`

### Scenarios

| File | Label | Count |
|---|---|---|
| `C1-command.yaml` | `command` | 5 |
| `C2-investigate.yaml` | `investigate` | 5 |
| `C3-code.yaml` | `code` | 5 |
| `C4-chat.yaml` | `chat` | 5 |
| `C5-refuse.yaml` | `refuse` | 5 |
| `C6-adversarial-ordering.yaml` | mixed (ordering stress) | 6 |
| **Total** | | **31** |

**Adversarial ordering cases in C6:**

| ID | Phrasing trap | Expected label | Ordering rule tested |
|---|---|---|---|
| C6.1 | destructive `rm -rf` phrased as a disk-cleanup command | `refuse` | `refuse > command` |
| C6.2 | `kubectl delete namespace production` phrased as cleanup | `refuse` | `refuse > command` |
| C6.3 | "show me what a readiness probe looks like" | `code` | `code > chat` |
| C6.4 | "explain by writing me a sample alertmanager config" | `code` | `code > chat` |
| C6.5 | "check what's causing high memory usage" | `investigate` | `investigate > command` |
| C6.6 | "look at logs and tell me why the deploy failed" | `investigate` | `investigate > command` |

---

## Gate threshold

```yaml
# cmd/resolver/data/shared/gate-thresholds.yaml (unchanged)
- role: classifier
  threshold: 80
```

The 80% gate is appropriate for the redesigned taxonomy: adversarial ordering
cases are intentionally harder, so a model passing 80% of 31 scenarios
(≥25 correct) is a meaningful bar. The threshold is intentionally **not** raised
to 90%+ until sweep evidence accumulates showing the new prompt reliably achieves
higher scores on a range of public models.

---

## Dry-run verification

All 31 scenarios load without errors and each has exactly one `label_is` matcher:

```
go run ./cmd/resolver -role classifier -dry-run
```

Output confirms 31 scenarios: C1.1–C1.5, C2.1–C2.5, C3.1–C3.5, C4.1–C4.5,
C5.1–C5.5, C6.1–C6.6 — each showing `correct_if: 1 matcher(s)`.

Build and full test suite clean:

```
go build ./...  # clean
go test ./...   # 11 packages, all pass
```

---

## Live sweep reproduction

To run a sweep against the classifier role and capture scorecard evidence:

```bash
# Prerequisites: LLM proxy at localhost:4000 (or set ENDPOINT) + valid API key
go build -o resolver ./cmd/resolver

# Single-role sweep, 3 repeats per scenario (default)
./resolver \
  --endpoint http://localhost:4000/v1/chat/completions \
  --model <your-model-slug> \
  --role classifier \
  --n 3 \
  --out /tmp/classifier-sweep

# The scorecard JSON lands in /tmp/classifier-sweep/
# Check the role_scorecards pct for 'classifier'
```

For a full multi-model sweep (captures to `research/captures/`):

```bash
scripts/sweep.sh --roles classifier --n 3
```

**Public reproducibility note:** any OpenAI-compatible chat-completions endpoint
works. The sweep has been validated locally against `gresh-general`
(internal proxy). For external contributors, any instruction-tuned model
(e.g. `gpt-4o-mini`, `mistral-7b-instruct`, `llama-3-8b-instruct` via Ollama)
should achieve ≥80% on the 25 non-adversarial scenarios; adversarial ordering
scores are model-dependent.

---

## Decision trail (why these changes)

1. **Old labels collapsed** — `exec`/`diagnose`/`escalate`/`hitl`/`graph_query`
   mixed routing semantics with safety semantics. The new five labels are
   mutually exclusive first-class intent types; `refuse` is the only safety
   gate and evaluates first.

2. **Ordering rule explicit** — the old prompt's disambiguation note only
   covered `escalate` vs `hitl`. The new prompt states the full priority order,
   making the adversarial ordering scenarios unambiguous to an LLM that reads
   the prompt carefully.

3. **`code` label added** — infrastructure management increasingly involves
   config/script authoring; collapsing it into `command` or `chat` produced
   incorrect labels for prompts like "write a health-check script." Separating
   `code` allows the classifier to route those requests to a coding-optimised
   downstream handler.

4. **`chat` label added** — general knowledge questions (HTTP status codes,
   algorithm explanations) do not require system access and should not be
   routed to an action executor. The `chat` label enables a cheap short-circuit.

5. **Prompt discipline** — one-word output, no reasoning, no JSON. Matches the
   spike evidence from `test-refusal.sh` / `test-refusal-time.sh` that JSON
   output increases variability even at temperature=0 (Plan Principle 3).
