---
name: sdd
description: /sdd <slug> — spec-driven development: brainstorm to a spec, plan it into lanes, execute one lane per builder under the tdd skill, then close the tree out into its durable homes
---

<!-- Written by `aphrollo gate init` -- edit the template in aphrollo, not this file. -->

# Spec-driven development

`/sdd <slug>` runs a feature end to end in four phases. The spec tree is
SCAFFOLDING: it exists to carry the decision from the question to the code, and
it is deleted in the merge that lands the work.

`<sdd-dir>` is the workspace's `[workspace.metadata.aphrollo] sdd-dir`, default
`docs/sdd`; read it first. Every path below is `<sdd-dir>/<YYYY-MM-DD>-<slug>/`.

## 1. Brainstorm → `spec.md`

Ask questions ONE AT A TIME until the design is settled. Prefer multiple
choice. Never start building because the answer seems obvious — an unexamined
assumption is what the questions are for.

Read the repo first: the existing flow, the recent commits, the design docs.
A request covering several independent subsystems is decomposed into one spec
each before any of them is written.

Write `spec.md`:

- **Problem** — what is wrong now, in the terms of whoever feels it.
- **Decisions** — one per line, each with the rejected alternative and why, in
  one line: `chose X over Y because Z`.
- **Boundaries** — what is explicitly NOT in this feature.
- **Acceptance criteria** — testable statements. "Rejects a NaN velocity and
  keeps the previous value", not "handles bad input".

Then read it once with fresh eyes: no TBDs, no section contradicting another,
no requirement readable two ways. Fix inline, then run `aphrollo ratchet check --no-tighten` and fix every hit.

## 2. Plan → `plan.md`

Split the spec into LANES. A lane is the smallest unit that carries its own
test cycle and is worth a fresh reviewer's judgement — small enough for one
builder to finish and be reviewed on its own.

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
`tdd` skill: RED first for new code, a mutation proof for code that already
exists, and the gate's own line is the evidence — never a claim without it.

Review each lane cold, by someone who did not write it. At most two fix rounds,
then park with a ruling. Merge a lane only with the gate green and every
finding fixed or accepted in the merge body. At lane end the spec tree is committed or deleted, never left untracked, or a hand-edit left uncommitted is invisible to the next `gate init` and gets overwritten.

## 4. Close

Before the final merge, move what outlives the spec into its durable home:

- design → the repo's design docs
- decisions the code cannot state itself → `crates/<x>/docs/decisions.md` or
  that language's equivalent
- open points → the followups index

Then DELETE `<sdd-dir>/<YYYY-MM-DD>-<slug>/` in the merge. Nothing durable
cites the spec tree and it cites nothing durable: at zero references it deletes
without breaking anything, which is the test that the move actually happened.
