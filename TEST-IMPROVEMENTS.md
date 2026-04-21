# Test improvements — working notes

Running findings from walking through the resolver scenario corpus one
role at a time. Purpose: capture observations and potential direction
for each role. Conclusions and plans come later.

---

## Still to walk through

- agentic-toolcall
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
