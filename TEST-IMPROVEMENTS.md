# Test improvements — working notes

Running findings from walking through the resolver scenario corpus one
role at a time. Purpose: capture observations and potential direction
for each role. Conclusions and plans come later.

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

The label space (`hitl`, `exec`, `diagnose`, …) is a distinct axis
from the scenario-role structure, so no conflict there — but the
category itself is weak. Six scenarios isn't enough padding to draw
reliable conclusions from. Needs substantial expansion before it
carries real signal.
