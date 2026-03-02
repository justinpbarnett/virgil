# Pipeline Specification

This document defines what a pipeline is, what it must provide, and how to build one. It is the reference standard for anyone creating a new pipeline for Virgil.

For the philosophy behind pipelines, see `virgil.md`. For architectural decisions, see `ARCHITECTURE.md`. For the atomic units that pipelines compose, see `pipe.md`.

---

## What a Pipeline Is

A pipeline is a graph of pipes. It defines an execution order with semantics that go beyond simple chaining: sequential steps, parallel branches, retry loops, and cycles. Pipelines can contain other pipelines.

From the outside, a pipeline is indistinguishable from a pipe. It has a name, takes an envelope in, produces an envelope out. The router routes to a pipeline the same way it routes to a pipe. The user doesn't know or care whether their request triggered an atomic pipe or a hundred-step pipeline. The runtime unfolds it.

This is the same relationship as Unix programs and shell scripts. `ls` is a program. A deploy script that calls `git`, `docker`, `ssh`, and `curl` is also a program — from the outside. It takes input and produces output. It can be called from other scripts. The fact that it has internals doesn't change its interface.

---

## The Pipeline Contract

Every pipeline must provide a **definition**. Unlike pipes, pipelines have no handler — their behavior is defined entirely by the graph of pipes (and other pipelines) they compose.

---

## Definition

The pipeline definition is declared in configuration. It tells the runtime how to arrange pipes into an execution graph.

### Required Fields

```yaml
name: dev-feature
description: Full feature development cycle with build, verify, and review.
category: dev
```

**name** — The pipeline's unique identifier. Same rules as pipes: lowercase, no spaces, no special characters. This name appears in plan templates, logs, and can be referenced by other pipelines.

**description** — A one-sentence explanation of what the pipeline does. Used by the AI fallback for classification, and by the user when listing available capabilities.

**category** — Which category the pipeline belongs to. Same categories as pipes. A pipeline and a pipe can share a category — the router doesn't distinguish between them during classification.

### Triggers

```yaml
triggers:
  exact:
    - "build a feature"
    - "add a feature"
  keywords:
    - feature
    - implement
    - build
  patterns:
    - "add {feature} to {target}"
    - "implement {feature} in {target}"
    - "build {feature} for {target}"
```

Identical to pipe triggers. They feed the same router layers — exact match, keyword index, category narrowing. The router doesn't care whether a trigger resolves to a pipe or a pipeline.

### Steps

Steps are the core of a pipeline definition. Each step is one of: a pipe invocation, a pipeline reference, a parallel group, a loop, or a cycle definition.

```yaml
steps:
  - name: spec
    pipe: spec.generate
    args:
      topic: "{{feature}}"
      target: "{{target}}"

  - name: prepare
    parallel:
      - pipe: worktree.create
        args:
          branch: "feat/{{feature | slugify}}"
      - pipe: codebase.study
        args:
          role: builder
      - pipe: codebase.study
        args:
          role: reviewer

  - name: build-verify
    pipeline: verify-fix-loop

  - name: publish
    pipe: git.commit
    then: git.push
    then: pr.create

  - name: review
    pipe: review
```

Each step has:

**name** — A label for this step within the pipeline. Used in logging, the detail panel, cycle references, and error reporting. Unique within the pipeline.

**pipe** — Which pipe to invoke. Mutually exclusive with `parallel` and `pipeline`.

**pipeline** — Which pipeline to invoke as a nested step. The referenced pipeline is expanded at runtime. Mutually exclusive with `pipe` and `parallel`.

**parallel** — A list of pipe or pipeline invocations to run concurrently. Mutually exclusive with `pipe` and `pipeline`.

**args** — Flags to pass to the pipe. Can use template variables from the signal's parsed components (`{{feature}}`, `{{target}}`) or from upstream envelopes.

**then** — Shorthand for sequential sub-steps within a single named step. Syntactic sugar for cases where a step is really 2-3 quick operations that don't warrant their own named step.

### Loops

A loop repeats a sequence of steps until a condition is met or a maximum iteration count is reached.

```yaml
loops:
  - name: verify-fix
    steps:
      - pipe: build
      - pipe: verify
      - pipe: fix
        condition: verify.error != null
    until: verify.error == null
    max: 5
    carry: error
```

**steps** — The pipes to run each iteration. Steps within a loop are sequential.

**until** — The exit condition. Evaluated after each full iteration. When true, the loop breaks and execution continues to the next step in the parent pipeline.

**max** — Maximum iterations. A safety bound. If the loop hasn't satisfied `until` after `max` iterations, it exits with an error envelope. This prevents infinite loops.

**carry** — What context carries forward between iterations. At minimum, the previous iteration's error envelope is available to the next iteration so that retries have context about what failed. Additional fields can be specified.

**condition** — Per-step conditions within a loop. The `fix` step only runs if `verify` produced an error. Steps without conditions always run.

### Cycles

A cycle routes a later step back to an earlier step with accumulated context. It differs from a loop: a loop repeats a contained sequence, a cycle spans the full pipeline.

```yaml
cycles:
  - name: review-rework
    from: review
    to: build-verify
    condition: review.outcome == "fail"
    carry: findings
    max: 3
```

**from** — The step whose output triggers the cycle. Evaluated after this step completes.

**to** — The step to return to. Execution resumes from this step with additional context.

**condition** — When to cycle. If false, execution continues past the `from` step normally.

**carry** — What the `from` step passes back to the `to` step. Typically structured findings — specific feedback that the earlier step can act on. This is appended to the `to` step's input, not replacing it.

**max** — Maximum full cycles. After `max` cycles, the pipeline exits regardless of the condition. The final output is whatever the last `from` step produced.

The critical difference between a loop and a cycle: a loop retries a contained sequence (build → verify → fix, try again). A cycle spans the pipeline — review at the end feeds findings all the way back to the build step, which runs through verify and publish again before reaching review again. Loops are inner retries. Cycles are outer rework.

### Envelope Flow

Envelopes flow between steps according to the execution pattern.

**Sequential steps** — Each step receives the previous step's output envelope as its input.

```
spec.generate → [envelope A] → worktree.create → [envelope B] → build → ...
```

**Parallel steps** — Each branch receives the same input envelope (the output of the step before the parallel group). All branches run concurrently. Their output envelopes are collected into an **envelope set** — an ordered list of envelopes available to the next step.

```
                    ┌→ worktree.create → [envelope B1]
[envelope A] ──────┼→ codebase.study(builder) → [envelope B2]    → [envelope set: B1, B2, B3]
                    └→ codebase.study(reviewer) → [envelope B3]
```

The step after a parallel group receives the full envelope set. How it uses them depends on the pipe:

- A non-deterministic pipe typically receives all envelopes as combined context.
- A deterministic pipe may read specific envelopes by the producing pipe's name.
- The runtime can be configured to merge envelopes into a single envelope with combined content, or to pass them as a set.

**Loop iterations** — The first iteration receives the input from before the loop. Subsequent iterations receive the previous iteration's output plus any `carry` context.

```
iteration 1: [input envelope] → build → verify → fail → fix
iteration 2: [fix output + error context] → build → verify → pass → exit loop
```

**Cycle passes** — The `from` step's output is combined with the `carry` fields and passed to the `to` step. The `to` step receives its original input plus the accumulated findings.

```
cycle 1: build → verify → publish → review(fail) → extract findings
cycle 2: build(original input + findings) → verify → publish → review(pass) → done
```

### Pipeline Output

The pipeline's output envelope is the output of its final step. If the final step is a parallel group, the output is the envelope set. If a cycle is active, the final output is the last `from` step's output.

The output envelope's `pipe` field is set to the pipeline's name, not the final step's pipe name. From the outside, the pipeline produced this output.

```
pipe: dev-feature
action: complete
args: { feature: "OAuth login", target: "Keep" }
content: { pr_url: "https://github.com/...", summary: "..." }
content_type: structured
```

---

## Error Handling

Errors in pipelines are handled at two levels: the pipe level and the pipeline level.

**Pipe-level errors** are reported through the envelope (see `pipe.md`). The runtime reads the error and decides what to do based on the pipeline's configuration and the execution context.

**Pipeline-level error behavior:**

**In a sequential step** — a fatal error halts the pipeline. A retryable error is retried according to the pipe's configuration. A warning is logged and execution continues.

**In a parallel group** — each branch handles its own errors independently. If one branch fatals, the pipeline can be configured to either halt all branches or continue with the remaining branches. The default is to continue — a partial result is better than no result.

```yaml
steps:
  - name: prepare
    parallel:
      - pipe: worktree.create
      - pipe: codebase.study
        args: { role: builder }
      - pipe: codebase.study
        args: { role: reviewer }
    on_branch_failure: continue # or: halt
```

**In a loop** — a retryable error triggers the next iteration (this is the purpose of the loop). A fatal error exits the loop and propagates to the pipeline. If the loop exhausts `max` iterations without satisfying `until`, it exits with an error envelope describing what failed and how many attempts were made.

**In a cycle** — similar to a loop but at the pipeline level. If the cycle exhausts `max` passes, the pipeline completes with the last output and a warning indicating the cycle didn't converge.

**Pipeline-level fatal** — when no recovery is possible, the pipeline produces an error envelope with `severity: fatal`. The runtime reports this to the stream and the detail panel. Any parallel pipelines running in the background are unaffected.

---

## Nesting

A pipeline can reference another pipeline as a step. The referenced pipeline is expanded and executed as if its steps were inlined at that position — but with its own scope for loops, cycles, and error handling.

```yaml
# A reusable loop
name: verify-fix-loop
description: Build, verify, and fix until tests pass.
steps:
  - pipe: build
  - pipe: verify
  - pipe: fix
    condition: verify.error != null
loops:
  - name: verify-fix
    steps: [build, verify, fix]
    until: verify.error == null
    max: 5

---
# A pipeline that uses the loop
name: dev-feature
description: Full feature development cycle with build, verify, and review.
steps:
  - name: spec
    pipe: spec.generate
  - name: prepare
    parallel:
      - pipe: worktree.create
      - pipe: codebase.study
        args: { role: builder }
      - pipe: codebase.study
        args: { role: reviewer }
  - name: build-verify
    pipeline: verify-fix-loop
  - name: publish
    pipe: pr.create
  - name: review
    pipe: review
cycles:
  - name: review-rework
    from: review
    to: build-verify
    condition: review.outcome == "fail"
    carry: findings
    max: 3
```

Nesting rules:

- **Depth is unlimited** but should be kept shallow for readability. Two levels (a pipeline referencing a pipeline) covers most cases. Three levels is a code smell.
- **Scope is isolated.** A nested pipeline's loops and cycles are internal. The parent pipeline can't reference steps inside a nested pipeline in its own cycle definitions.
- **Input flows in.** The nested pipeline receives the envelope from the previous step in the parent.
- **Output flows out.** The nested pipeline's output envelope becomes the input to the next step in the parent.
- **Errors propagate up.** A fatal error in a nested pipeline becomes a fatal error in the parent at that step.

---

## Logging and Observability

Pipelines get the same automatic observability as pipes — the runtime logs every envelope transition. But pipelines add structure that logging reflects.

At **info** level, the runtime logs each step's start and completion with timing:

```
[info]  pipeline:dev-feature started
[info]    step:spec → spec.generate → 1.2s
[info]    step:prepare → parallel(3) → 3.4s
[info]    step:build-verify → verify-fix-loop → 14.9s (2 iterations)
[info]    step:publish → pr.create → 0.8s
[info]    step:review → review → 4.2s → cycle triggered
[info]    step:build-verify → verify-fix-loop → 8.3s (1 iteration)
[info]    step:publish → pr.create → 0.6s
[info]    step:review → review → 3.8s → pass
[info]  pipeline:dev-feature complete → 37.2s (2 cycles)
```

At **debug** level, the runtime additionally logs the envelope content between steps, the parallel branch outputs, the loop iteration details, and the cycle carry content.

Pipeline logs are structured hierarchically. The detail panel renders this hierarchy: pipeline → step → pipe → (nested pipeline → step → pipe). Expanding a step shows its internals. Collapsing shows the one-line summary.

---

## Examples

### Simple: Research and Save

A two-step inline pipeline. Could be built by the planner from a template, but defining it as a named pipeline means it gets its own triggers and can be referenced by other pipelines.

```yaml
name: research-and-save
description: Research a topic and save key findings to memory.
category: research

triggers:
  patterns:
    - "research {topic} and save"
    - "look into {topic} and remember"

steps:
  - name: research
    pipe: research
    args:
      query: "{{topic}}"
  - name: save
    pipe: memory.store
    args:
      type: long_term
      tags: ["research", "{{topic}}"]
```

### Medium: Draft with Review

Research context, draft content, self-review, revise if needed. Demonstrates a loop without a cycle.

```yaml
name: draft-reviewed
description: Draft content with self-review and revision loop.
category: comms

triggers:
  patterns:
    - "draft a reviewed {type} about {topic}"

steps:
  - name: gather
    parallel:
      - pipe: memory.retrieve
        args:
          topic: "{{topic}}"
      - pipe: memory.retrieve
        args:
          query: "writing style preferences"
  - name: write
    pipe: draft
    args:
      type: "{{type}}"
      topic: "{{topic}}"
  - name: review
    pipe: review
    args:
      criteria: [clarity, accuracy, tone]
  - name: revise
    pipe: draft
    args:
      type: revise
    condition: review.outcome == "needs_revision"

loops:
  - name: review-revise
    steps: [write, review, revise]
    until: review.outcome == "pass"
    max: 3
```

### Complex: Feature Development

The full dev-feature pipeline from the architecture discussions. Demonstrates all four execution patterns: sequential setup, parallel preparation, inner retry loop, and outer review cycle.

```yaml
name: dev-feature
description: Full feature development cycle with build, verify, and review.
category: dev

triggers:
  exact:
    - "build a feature"
  keywords:
    - feature
    - implement
  patterns:
    - "add {feature} to {target}"
    - "implement {feature} in {target}"
    - "build {feature} for {target}"

steps:
  - name: spec
    pipe: spec.generate
    args:
      topic: "{{feature}}"
      target: "{{target}}"

  - name: prepare
    parallel:
      - pipe: worktree.create
        args:
          branch: "feat/{{feature | slugify}}"
      - pipe: codebase.study
        args:
          role: builder
          path: "{{target}}"
      - pipe: codebase.study
        args:
          role: reviewer
          path: "{{target}}"
    on_branch_failure: halt

  - name: build-verify
    pipeline: verify-fix-loop

  - name: publish
    pipe: git.commit
    then: git.push
    then: pr.create

  - name: review
    pipe: review
    args:
      source: pr-diff

cycles:
  - name: review-rework
    from: review
    to: build-verify
    condition: review.outcome == "fail"
    carry: findings
    max: 3

metrics:
  - name: cycle_convergence
    type: ratio
    numerator: completed_in_one_cycle
    denominator: total_runs
    window: 30d
    threshold:
      warn: 0.50
      degrade: 0.30

  - name: total_duration_p95
    type: percentile
    field: duration
    percentile: 95
    window: 30d
    threshold:
      warn: 300000    # 5 minutes
      degrade: 600000  # 10 minutes
```

---

## Testing

Pipelines are tested differently from pipes. A pipe is tested in isolation with a constructed envelope. A pipeline is tested by its graph structure and its end-to-end behavior.

### What to Test

**Graph validation** — does the pipeline definition parse correctly? Are all referenced pipes and pipelines defined? Are step names unique? Do cycle references point to valid steps?

**Sequential flow** — given a starting envelope, does each step receive the correct input from the previous step? Mock every pipe to return a known envelope and verify the chain.

**Parallel flow** — do all branches receive the same input? Are all output envelopes collected into the envelope set? Does the next step receive the full set?

**Loop behavior** — does the loop exit when the `until` condition is met? Does it respect `max` iterations? Does carry context accumulate correctly across iterations?

**Cycle behavior** — does the cycle trigger when the condition is met? Does carry content propagate back to the `to` step? Does it respect `max` cycles? Does it exit cleanly when the condition is not met?

**Error propagation** — does a fatal error in a step halt the pipeline? Does a fatal error in one parallel branch behave according to `on_branch_failure`? Does a loop that exhausts `max` produce the correct error envelope?

**Nesting** — does a nested pipeline receive the correct input? Does its output flow to the next step in the parent? Does a fatal error in a nested pipeline propagate to the parent?

### Test Strategy

Mock every pipe in the pipeline. Each mock returns a predetermined envelope. This isolates the pipeline's graph logic from the pipe's execution logic. You're testing the wiring, not the work.

```
# Pseudocode
mock(spec.generate).returns(envelope(content="spec v1"))
mock(worktree.create).returns(envelope(content="branch created"))
mock(codebase.study).returns(envelope(content="codebase context"))
mock(build).returns(envelope(content="code changes"))
mock(verify).returns(envelope(content="all tests pass", error=null))
mock(pr.create).returns(envelope(content="PR #47"))
mock(review).returns(envelope(content="approved", outcome="pass"))

result = run(dev-feature, input=envelope(feature="OAuth", target="Keep"))
assert result.content.pr_url exists
assert result.error == null
assert cycle_count == 0
```

Then test the failure paths:

```
mock(verify).returns(envelope(error={message: "test failure", severity: error, retryable: true}))
# Assert: loop retries, fix runs, verify runs again

mock(review).returns(envelope(outcome="fail", findings=[...]))
# Assert: cycle triggers, build-verify runs again with findings in context

mock(verify).always_fails()
# Assert: loop exits after max iterations, pipeline reports error
```

---

## Checklist

Before shipping a new pipeline, verify:

```
Definition
  ☐  name is unique, lowercase, no special characters
  ☐  description is one clear sentence
  ☐  category is correct
  ☐  triggers cover the common ways a user would invoke this
  ☐  all referenced pipes exist and are defined
  ☐  all referenced pipelines exist and are defined
  ☐  step names are unique within the pipeline

Graph
  ☐  sequential steps flow logically (each step's output is useful input to the next)
  ☐  parallel branches are truly independent (no branch depends on another's output)
  ☐  loops have a reachable exit condition
  ☐  loops have a max iteration bound
  ☐  cycles reference valid step names for from and to
  ☐  cycles have a max cycle bound
  ☐  carry fields contain what the target step actually needs

Envelope Flow
  ☐  the first step can work with the pipeline's input envelope
  ☐  parallel groups produce an envelope set the next step can consume
  ☐  loop iterations carry forward the context needed for retries
  ☐  cycle carry content is structured feedback, not raw output dumps
  ☐  the final step's output is a useful pipeline output

Error Handling
  ☐  parallel groups have on_branch_failure configured
  ☐  loop exhaustion produces a clear error envelope
  ☐  cycle exhaustion completes with the last output plus a warning
  ☐  fatal errors in any step propagate correctly

Nesting
  ☐  nested pipelines are tested independently
  ☐  nesting depth is ≤ 2 levels (pipeline → pipeline → pipes)
  ☐  parent cycles don't reference steps inside nested pipelines

Testing
  ☐  graph validation passes (all references resolve)
  ☐  happy path with mocked pipes produces expected output
  ☐  loop retry path works (verify fails → fix → verify succeeds)
  ☐  loop exhaustion path works (max iterations → error)
  ☐  cycle path works (review fails → findings carry back → rebuild)
  ☐  cycle exhaustion path works (max cycles → warning + last output)
  ☐  parallel branch failure path works (per on_branch_failure config)
  ☐  nested pipeline failure propagation works
```
