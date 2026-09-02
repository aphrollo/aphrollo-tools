---
name: tdd
description: TDD enforcement — /tdd status|off|on|allow-main|reset toggles the session gate; the body is the RED→GREEN procedure the gate assumes
argument-hint: "[status|off|on|allow-main|reset]"
---

<!-- Written by `aphrollo gate init`. Hand edits are overwritten by the next init;
edit the template in aphrollo (`internal/tdd/tddskill.md`) instead. -->

# TDD

`/tdd <arg>` is intercepted by the UserPromptSubmit hook (`aphrollo gate
userpromptsubmit`), which applies the toggle and prints the result. If you are
reading this body right after typing `/tdd status`, the hook did NOT intercept:
say that `aphrollo gate userpromptsubmit` is missing from the hooks in
`settings.json`, and stop. Never write gate state by hand.

The rest is the procedure the gate assumes. The gate proves the RED→GREEN
OUTCOME mechanically; it cannot tell whether the test was worth writing. That
part is here.

## The loop

**RED.** One test first, for one behavior, named for what should happen. Watch
it fail — the edit hook classifies the run for you:

- `red-missing-impl` — the clean RED: the symbol under test does not exist yet.
- `red` — it fails on an assertion. Read the message: is that the failure you
  intended? A test failing for the wrong reason proves nothing.
- `red-bogus` — broken setup (syntax, imports, collection). Not a RED. Fix it
  and get a real one before writing implementation.
- `green` on a brand-new test — you are testing behavior that already exists.
  Either the test is wrong, or this is existing code: use a mutation proof
  (below).

**GREEN.** The minimum that passes. No options nobody asked for, no adjacent
refactor, no "while I'm here".

**REFACTOR.** Only while green. Remove duplication, improve names, split a file
that got long. No new behavior.

Then the next failing test. Code written before its test is code no test has
been proven to catch: delete it and come back through RED.

## The hooks run the tests, not you

After every Edit/Write the PostToolUse hook prints exactly one `gate:` line.
Read it. Never re-run a suite it just ran.

- `green (N passed)` — proven.
- `red-missing-impl` / `red` / `red-bogus` — as above.
- `TIMEOUT` / `SKIPPED` / `QUEUED-SKIPPED` — **inconclusive: the code was NOT
  tested.** Not a pass, and never reportable as one.
- `BUILDING (deferred)` — still running; its result arrives at the next hook.

While shaping code, iterate with a command that compiles but runs nothing
(`cargo check -p <crate> --tests`, `go vet ./...`, the equivalent in your
language). The only sanctioned manual runs: a mutation proof, a deliberate
soak, or ONE targeted run after the hook itself said TIMEOUT or SKIPPED.

Commit one mixed test+implementation commit per task. The commit gate re-proves
RED in a clean worktree and runs the touched packages' suites.

## What makes a test worth keeping

- **Name the break it catches.** Before writing the body, answer: what
  production change would make this fail, and is that change a bug? No answer →
  do not write it. A test that cannot fail reads as coverage and is worse than
  none.
- **Derive the expectation independently.** Literals and hand-checked values;
  table-driven cases with literal `want`. An expected value computed by the code
  under test (or its helpers) passes no matter what that code does.
- **No change detectors.** Do not assert a constant's value or an exact message;
  assert the behavior that depends on it. A change detector fires on every
  intentional edit and sleeps through real bugs.
- **The bar is the tightest the class admits.** Determined output (codecs,
  golden vectors, pure kernels) → assert exact bits. Genuinely approximate
  output → a tolerance, sized so a 1% error in the quantity under test still
  fails, and justified where it is written.
- **Real code over mocks.** Mock only what is slow or external, and only after
  learning the real thing's side effects. Never assert on a mock's behavior.
- **DAMP over DRY.** A little duplication beats a helper that hides what is
  being asserted. A test reads top to bottom.
- **Test your boundary, not the framework**, and not the text of a file: run the
  artifact and assert its effects.
- **Never weaken a test, a tolerance, or a baseline to get green.** A hand-edited
  ratchet baseline is a rejected commit, not a fix. BLOCKED with numbers is a
  valued outcome; pin a real failure as an ignored test naming the defect.

## Code that already exists

Verification, characterization and kernel tests have no natural RED. Use a
**mutation proof** instead: introduce one specific error in the production code
(flip a sign, drop a term, scale a constant), confirm THAT test fails, restore
the code byte-identically, and report the mutation and the failure line. A test
written against existing code and never mutated certifies nothing.

Before finishing a test file, mutate mentally: wrong constant, wrong branch,
missing side effect, empty return, missing validation of zero/empty/malformed
input. A mutation nothing catches marks the behavior unprotected.

## Before claiming done

Evidence, then the claim — never the other way round, and never "should work".

1. Name the command that proves it.
2. Read its full output: exit code, counts, the first failing name.
3. State the claim WITH that evidence: the gate's green line and its count, or
   the failing test's name.

"Passes", "fixed", "complete" without a line of output behind it is a guess.
Anything the hook reported as TIMEOUT, SKIPPED or QUEUED-SKIPPED is unproven,
and the honest report says so.
