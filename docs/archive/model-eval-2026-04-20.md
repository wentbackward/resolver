# SSHWarm model evaluation note — 2026-04-20

## Purpose

This note captures the live reducer-eval results gathered so far for the SSHWarm JSON and S-expression harnesses.

The suite is testing whether a model can act like a viable local reducer under the current contract, not whether it can merely emit structured text.

## Scenarios in the suite

### 1. `continue-basic`
A straightforward ready task (`uname -a`) that should continue cleanly.

What it tests:
- basic structured output obedience
- simple tool request generation
- low-drift forward progress

### 2. `blocked-basic`
A task that targets `/missing/file` and should block rather than blindly continue.

What it tests:
- status judgment
- whether the reducer can recognize a blocked branch
- whether it over-eagerly calls tools instead of preserving control semantics

### 3. `quote-materialization`
A goal with quoted/speculative structure.

What it tests:
- locality rules
- branch-structure awareness
- whether the reducer handles speculative structure without illegal cursor jumps

### 4. `succeeded-basic`
A goal with an observation saying `All checks complete`.

What it tests:
- success semantics
- distinction between node status and run/reducer status
- termination correctness

## JSON results before/after prompt changes

Prompt change:
- require `<think>` reasoning before the final output
- require the response to end immediately after the tool call / final structured output

| Model | Run | Parse | Schema | Envelope | Locality | Status |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| gresh-general | before | 0.75 | 0.75 | 1.00 | 0.50 | 0.25 |
| gresh-general | after | 0.50 | 0.50 | 0.75 | 0.25 | 0.00 |
| gresh-coder | before | 0.75 | 0.75 | 1.00 | 0.50 | 0.25 |
| gresh-coder | after | 0.75 | 0.75 | 1.00 | 0.75 | 0.50 |
| gresh-nothink | before | 0.75 | 0.75 | 1.00 | 0.75 | 0.50 |
| gresh-nothink | after | 0.75 | 0.75 | 1.00 | 0.75 | 0.50 |
| gresh-creative | after | 0.75 | 0.75 | 1.00 | 0.75 | 0.50 |
| gresh-reasoner | after | 0.25 | 0.25 | 1.00 | 0.25 | 0.25 |

## JSON interpretation

### gresh-general
- can emit valid JSON when token budget is high enough
- remains weak on blocked/succeeded semantics
- regressed after the latest prompt changes (including one fenced JSON response)

### gresh-coder
- materially improved after the prompt changes
- now tied with `gresh-nothink` on the JSON reducer suite
- still weak on blocked/succeeded semantics

### gresh-nothink
- best stable baseline so far
- prompt changes did not materially improve or worsen it
- still weak on blocked/succeeded semantics

### gresh-creative
- roughly matches `gresh-nothink` and post-prompt `gresh-coder`
- still shows the same semantic failure class on blocked/succeeded

### gresh-reasoner
- worst JSON performer of the tested set
- tends to over-elaborate and invent state changes or invalid node kinds/statuses
- currently not a good reducer candidate

## Common JSON failure pattern

Across all tested JSON-capable models, the main remaining weakness is not output shape but contract semantics:
- treating a blocked case as `continue`
- using invalid success/node status values like `succeeded` or `completed` instead of node-local statuses like `done`
- occasionally violating locality on quoted/speculative branches
- `gresh-reasoner` additionally invents workflowy state updates instead of staying within the reducer contract

## S-expression results gathered so far

| Model | Parse | Schema | Envelope | Locality | Status |
| --- | ---: | ---: | ---: | ---: | ---: |
| gresh-nothink | 0.00 | 0.00 | 0.75 | 0.00 | 0.00 |
| gresh-general | 0.00 | 0.00 | 1.00 | 0.00 | 0.00 |
| gresh-coder | 0.00 | 0.00 | 1.00 | 0.00 | 0.00 |
| gresh-reasoner | 0.00 | 0.00 | 1.00 | 0.00 | 0.00 |

Observed behavior:
- models often stayed near a single top-level S-expression response
- but they still failed the contract consistently
- common failure shapes:
  - malformed or empty `:edits` instead of a valid `(edit-script ...)`
  - emitting tool forms like `(exec ...)` where edit forms were required
  - incorrect status choice even when the envelope looked superficially plausible
  - occasional full drift out of S-expression mode

Interpretation:
- even after prompt changes, S-expression still does not currently look like a viable primary path
- JSON remains the much stronger baseline

## Current ranking for reducer-like JSON behavior

1. **gresh-nothink** — tied best
2. **gresh-coder** — tied best after prompt changes
3. **gresh-creative** — tied close behind / comparable to the best pair
4. **gresh-general** — weaker than the above
5. **gresh-reasoner** — clearly worst for reducer-style contract obedience

## Current takeaway

1. JSON is still the best available transport baseline.
2. S-expression has not shown evidence of being worth pursuing as the primary path.
3. Model choice is clearly role-sensitive:
   - reducer-style structured control
   - reasoning/diagnosis
   - code generation
   - verification
   should not be assumed to want the same model or parameter profile.
4. The biggest unsolved reducer problem is semantic contract judgment, not formatting.
5. `gresh-reasoner` reinforces the model-routing lesson: a model that may be strong for reasoning is not automatically good at strict reducer control.

## Artifacts

Completed artifacts referenced here:
- `reports/live-json-suite-gresh-general-rerun.json`
- `reports/live-json-suite-gresh-general-postprompt.json`
- `reports/live-json-suite-gresh-coder-rerun.json`
- `reports/live-json-suite-gresh-coder-postprompt.json`
- `reports/live-json-suite-gresh-nothink-rerun.json`
- `reports/live-json-suite-gresh-nothink-postprompt.json`
- `reports/live-json-suite-gresh-creative-rerun.json`
- `reports/live-json-suite-gresh-reasoner-postprompt.json`
- `reports/live-sexp-suite-gresh-nothink-postprompt.json`
- `reports/live-sexp-suite-gresh-general-postprompt.json`
- `reports/live-sexp-suite-gresh-coder-postprompt.json`
- `reports/live-sexp-suite-gresh-reasoner-postprompt.json`
