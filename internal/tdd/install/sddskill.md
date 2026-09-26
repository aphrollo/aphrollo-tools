---
name: sdd
description: /sdd <slug> — spec-driven development: brainstorm to a spec, plan it into lanes, execute one lane per builder under the tdd skill, then close the tree out into its durable homes
---

<!-- Written by `aphrollo install` -- edit the template in aphrollo, not this file. -->

# Spec-driven development

`/sdd <slug>` runs a feature end to end in four phases. The spec tree is
SCAFFOLDING: it exists to carry the decision from the question to the code, and
it is deleted in the merge that lands the work.

`<sdd-dir>` is the workspace's `[workspace.metadata.aphrollo] sdd-dir`, default
`docs/sdd`; read it first. Every path below is `<sdd-dir>/<YYYY-MM-DD>-<slug>/`.

## 1. Brainstorm → `spec.md`

Ask questions ONE AT A TIME until the design is settled, multiple choice where
possible. An answer that seems obvious is an unexamined assumption: ask it.

Read the repo first: the existing flow, the recent commits, the design docs.
Several independent subsystems get one spec each, before any is written.

Write `spec.md`:

- **Problem** — what is wrong now, in the terms of whoever feels it.
- **Decisions** — one per line, each with the rejected alternative and why, in
  one line: `chose X over Y because Z`.
- **Boundaries** — what is explicitly NOT in this feature.
- **Acceptance criteria** — testable statements. "Rejects a NaN velocity and
  keeps the previous value", not "handles bad input".

Re-read it fresh: no TBDs, no contradictions, no requirement readable two ways. Fix inline, then run `aphrollo ratchet check --no-tighten` and fix every hit.

## 2. Plan → `plan.md`

Split the spec into LANES: the smallest unit with its own test cycle, small
enough for one builder to finish and a fresh reviewer to judge on its own.

Each lane states, with no placeholders:

- **Files** — created, modified (with line ranges), and the test files.
- **Tests** — each named for the break it catches
  (`rejects_expired_token_at_the_boundary`, not `token_test`), with the
  closed-form expectation written out: the literal value, the reference
  output, or the identity two peers must agree on. A test whose expectation is
  computed by the code under test is not a test.
- **Interfaces** — exact signatures a later lane depends on. A builder sees
  only its own lane; this block is how it learns the neighbouring names.

Check the plan against the spec: every acceptance criterion maps to a lane, and
the names and types used in a later lane match what an earlier one defines. Then run `aphrollo ratchet check --no-tighten` and fix every hit.

## 3. Execute

One lane per builder, handed the lane's plan text VERBATIM. Build to the
`tdd` skill: RED first for new code, a mutation proof for code that already exists,
and the gate's own line is the evidence. Only builders edit: the coordinator never edits.
A brief or resume carries only what the agent lacks.

Review each lane cold, by a reviewer that did not write it. Follow-ups (fix
round, base merge, re-measure, red CI) resume that lane's builder with
only the delta, and its reviewer re-reviews its own findings. A fresh builder is for a new issue
(or a related one in files a builder already holds). At most two fix rounds, then
park with a ruling. Merge only with the gate green and every finding fixed or
accepted in the merge body. Commit or delete the spec tree at lane end: an
untracked hand-edit is overwritten by the next `aphrollo install`.

## 4. Close

Before the final merge, move what outlives the spec into its durable home:

- design → the repo's design docs
- decisions the code cannot state itself → `crates/<x>/docs/decisions.md` or its equivalent
- open points → the followups index

Then DELETE `<sdd-dir>/<YYYY-MM-DD>-<slug>/` in the merge. Nothing durable
cites the spec tree and it cites nothing durable: at zero references it deletes
without breaking anything, which is the test that the move actually happened.
