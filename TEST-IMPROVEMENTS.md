# Test improvements — working notes

Running findings from walking through the resolver scenario corpus one
role at a time. Purpose: capture observations and potential direction
for each role. Conclusions and plans come later.

**Guiding principle.** The goal of these edits isn't to improve the
pass rate. It's to push harness noise out of the scoring so the
failures that remain are real model-capability failures. A model that
fails on a fair probe should fail; a model that fails because the
harness mis-framed the task, counted a transient error, or rewarded
the wrong shape of response is telling us something about the harness,
not about the model.

---

## Cross-cutting requirements

### Semantic-classification matchers (A/B regex vs small-classifier)

**Requirement, not parked.** Regex is a weak proxy for the semantic
question the harness actually wants to answer ("is this a refusal?",
"does this command restart the right container?"). A small classifier
LLM at `temperature=0` — running locally, ollama-sized — is better
suited to fuzzy text questions. Structural checks (tool call exists,
`node=spark-01`, JSON field present) stay as code; only the fuzzy
slices get classified.

**Rollout shape — run both in parallel.**

- Every scenario whose matcher currently relies on regex against
  free-text content gets a twin classifier matcher. Scorecards carry
  both verdicts side-by-side: each scenario contributes a `T.1r`
  regex-matcher verdict and a `T.1c` classifier-matcher verdict.
- Same aggregation logic, different matcher engines — roles emit
  `role(r)` and `role(c)` scores. No change to how percentages /
  `parse_validity` / thresholds are computed.
- When enough sweep evidence shows one matcher is reliably better
  than the other for a specific scenario, drop the loser for that
  scenario. Per-scenario decision, not wholesale replacement.

**Non-negotiables when the classifier fires.**

- **Pin the classifier.** Model name + weight hash (or ollama digest)
  captured in the run manifest. A silent re-pull tomorrow shouldn't
  shift verdicts invisibly.
- **Classifier ≠ model-under-test instance.** Same family is OK, same
  instance isn't — mirrors the existing reporter-vs-model-under-test
  guard in `analyze report`.
- **Calibration gold set.** A small labeled corpus of known outputs
  (à la `test-refusal-time.sh`) rerun every sweep to confirm the
  classifier still agrees with ground truth. Warn loudly if accuracy
  drops below a threshold — gives us a tripwire when a new classifier
  pull regresses.
- **Persist classifier call + prompt + answer in the scorecard.**
  Regex misses are self-explanatory; classifier misses need the
  payload recorded so disagreements are auditable.

### Prompt-engineering discipline

Small changes in prompt wording produce measurably different output
behaviour at `temperature=0`. The `test-refusal-time.sh` spike
surfaced this: asking for JSON output instead of single-word YES/NO
introduced noticeable variability in what the small classifier
returned, even with the same model + same temperature.

The classifier role is a worked example of why this matters — but
the implication is general, and it binds both directions of the A/B:

- **Role-level system prompts are load-bearing.** An edit to a role's
  `system-prompt.md` is a scorecard-relevant change and should land
  in its own commit with before/after sweep evidence, not bundled
  with unrelated work.
- **Classifier-matcher prompts inherit the same property.** Their
  exact wording *is* part of the matcher's behaviour. Pin them, version
  them, check agreement against the calibration gold set whenever the
  prompt changes.
- **Prefer the minimum viable output contract.** One word, one label,
  no punctuation. Avoid asking for JSON, explanations, reasoning, or
  "think step by step" unless a specific matcher genuinely needs the
  chain-of-thought. Fewer degrees of freedom = fewer silent
  regressions.

---

## Still to walk through

- safety-refuse
- safety-escalate
- tool-count-survival
- health-check
- hitl
- multiturn
- long-context
- reducer-json
- reducer-sexp

**Pausing the walk-through** after `classifier` to iterate on the
classifier role *and* the classifier-matcher work together. They share
the same prompt-discipline requirements, so building them in parallel
closes the feedback loop. Resume the remaining walk-through once that
iteration has landed.

---

## Reviewed

### agentic-toolcall

Walked through failing rows from today's sweep. The regex matchers are
mostly fine; the signal is mixed in with noise from three separate
issues that we should handle across the whole harness, not just this
role.

1. **Rate-limit errors must be retried, not aggregated.** Kimi's HTTP
   429s today showed up as `error` rows that still counted against the
   role's denominator. Production agent stacks already do this:
   back-off + retry (say, 3 attempts with exponential pause); only
   hard-fail if the problem persists. Errors that are genuinely
   transient aren't capability signal and shouldn't influence the
   scorecard.

2. **Mark invalid runs explicitly so they drop out of aggregation.**
   Once we distinguish "endpoint gave up" from "model answered
   wrong", the aggregator needs to treat the former as invalid
   (excluded from the threshold calculation) rather than mixing them
   into the correct/partial/incorrect/error split. Applies to any
   role.

3. **This role is a strict 1-shot tool-calling probe — make that
   explicit in the role-level system prompt.** Today's failures are
   dominated by models doing "let me recon first" (list containers,
   query topology, check health) before they'd emit the action. That
   behavior is rational in a multi-turn loop but wrong for what this
   role is measuring. The prompt should say so: you get exactly one
   assistant turn, produce the tool calls that achieve the task in
   full.

4. **≥2 tool calls is fine — scoring should look at the combination,
   not the count.** T3.* currently awards partial credit when a model
   emits only one diagnostic call, and full credit only when it emits
   ≥2 in the same response. That penalizes the rational single-call
   pattern AND mis-rewards any 2-call chain even if neither call was
   useful. The rule should be: does the combination of tool calls
   cover the task? For a diagnostic question that means "do the calls
   collectively gather enough signal to answer" — not a count of
   calls.

Multi-turn tool chaining is a separate capability and should live in
the `multiturn` role, where it's already the explicit concern.

#### Expected effect of these changes

Not a rescue — just noise removal. Today's failing rows map to the
fixes roughly as:

| Failure pattern | Seed-runs | Noise-or-signal | Fix that catches it |
|---|--:|---|---|
| T3.* one-call partials | 26 | Noise (the rule mis-rewarded the wrong shape) | Combination-outcome scoring |
| T1.4 recon-then-stop | 6 | Mostly noise (1-shot framing unclear) | Explicit 1-shot prompt |
| T1.3 wrong-tool-choice (Qwen `graph_query`, MiniMax `health_check`) | 4 | Mix — Qwen is probably noise, MiniMax is real | 1-shot prompt partially; MiniMax stays a genuine miss |
| T1.1 recon-only | 3 | Noise | Explicit 1-shot prompt |
| T9.2 escalate-instead-of-exec | 3 | Already handled (scored partial by design) | None |
| 429 rate-limit errors | ~3 | Noise (transient, not capability) | Retry + invalidate |

Removing the noise should lift Qwen/Gemma scores by ~15 pp and flip
some FAIL verdicts to PASS. MiniMax choosing `health_check` for a GPU
utilization question isn't noise — that's a real wrong-tool miss, and
it should stay in the scorecard as a fail.

#### Related observation: `multiturn` is under-scaled

Pushing chained-call behavior out of `agentic-toolcall` means
`multiturn` has to carry that signal. Today it has one scenario. Flag
to revisit when we reach that role — one scenario can't carry a
capability probe on its own.

### node-resolution

The underlying capability is *choosing the correct target/subject and
handling ambiguity when more than one candidate fits*. The current
framing — "node selection" — is too specific to the sysadm reference
corpus; it leaks domain language into what should be a general
capability probe.

Note for later: the declared topology has a latent ambiguity
(`marvin` the node vs `openclaw-marvin` the service running on
`claw`) that none of the current T8 scenarios exploit.

### dep-reasoning

Should be about *knowledge freshness* — prefer looking things up over
recalling from memory or the context, verify facts before committing
to decisions, don't make things up. Current scenarios all ask about
dependency graphs, which is just one instance of the broader
capability.

### classifier

**Purpose refined.** This role is about **fast, intelligent routing**
at the entry of a pipeline — deciding which model family the request
should go to. It is not probing "which tool would get called" (that
belongs in `agentic-toolcall`); it's probing "what *kind of work* is
this?"

**The current label set mixes work-classes with downstream actions.**
`exec`, `escalate`, and `hitl` aren't classes of work — they're what
some downstream component decides to do once the work is understood.
A classifier at the front of a pipeline has no structural reason to
emit them. The work-class taxonomy we actually want:

| Label | Routes to | Example |
|---|---|---|
| `command` | instruct LLM (1-shot, deterministic) | "show disk usage on spark-01" |
| `investigate` | reasoner LLM (diagnosis / problem-resolution) | "why is grafana showing no data since last night?" |
| `code` | coder LLM | "write a systemd unit for the ingest worker" |
| `chat` | chat / creative LLM | "brainstorm names for the monitoring service" |
| `refuse` | filter LLM (profane, dangerous, abusive) | "rm -rf / on claw" |

**Ordering is a requirement, not a tiebreaker.** `refuse` has to be
checked *before* `command`, or a destructive-but-actionable request
slips through to the instruct LLM. The role's system prompt must
state this explicitly — "if the request is destructive or abusive,
return `refuse` even if it otherwise looks like a 1-shot command."

**This is the segue to classifier-matchers.** The rigor the
classifier role needs on its own prompt is exactly the rigor the
classifier-based matchers need. Same discipline, same tripwires
(calibration gold set, pinned weights, output contract minimised).
Doing these two together is the cleanest way to build that
discipline in.

**Scenario expansion required.** Today: 6 scenarios, one per old
label. Target: ~5 per new label (≈25 total) plus targeted adversarial
cases — a destructive request phrased as a 1-shot command (tests
`refuse` beating `command`), a coding question phrased as chat
(tests `code` beating `chat`), an investigation question that reads
like a command ("what's eating memory on spark-01?" — `investigate`
or `command`?).

---

## Parked ideas (not from role walk-through)

### Multi-turn tool calling as its own testing category

Worth promoting beyond a single role. The mocking-tool-responses
approach is right — deterministic, reproducible, no real side-effects
— but the category would need its own thinking:

- **What does multi-turn probe that single-turn doesn't?** Candidates:
  - chain-of-tool-calls where later calls depend on earlier results
  - proper context accumulation across turns
  - recovery from a tool that returns an error or empty result
  - knowing when to stop (not infinitely looping through tools)
  - managing context growth as tool outputs pile up
- **Mock design.** Scenarios would declare each tool's canned
  responses keyed on call-arguments, so the mock behaves
  deterministically but responds to the specific call the model
  makes. Already the shape used by the existing `multiturn.progressive`
  scenario — just needs extending.
- **Scoring.** Not a single regex match at the end. More like:
  *"did the sequence eventually produce the right artefact?"*
  Probably a terminal-state check on the last assistant turn, plus
  maybe invariants over the intermediate turns (no forbidden tool,
  didn't hallucinate, etc.).

Parked until we've finished the role walk-through — then plan
properly.
