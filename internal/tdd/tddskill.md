---
name: tdd
description: TDD enforcement — /tdd status|off|on|allow-main|reset|style terse|plain toggles the session gate; the body is the RED→GREEN procedure the gate assumes
argument-hint: "[status|off|on|allow-main|reset|style terse|plain]"
---

<!-- Written by `aphrollo install`. Hand edits are overwritten by the next init;
edit the template in aphrollo (`internal/tdd/tddskill.md`) instead. -->

# TDD

`/tdd <arg>` is intercepted by the UserPromptSubmit hook (`aphrollo gate userpromptsubmit`).
Reading this after typing `/tdd status` means the hook is
not installed: say so and stop. Never write gate state by hand.

The gate proves RED→GREEN mechanically. Whether the test was worth writing is
this page.

## Loop

1. **RED.** One test, one behavior, named for the break it catches. The edit
   hook classifies it: `red-missing-impl` (clean RED) · `red` (assertion — is it
   the failure you meant?) · `red-bogus` (broken setup, not a RED) · `green` on a
   new test (behavior already exists → mutation proof, below).
2. **GREEN.** Minimum that passes. Nothing extra.
3. **REFACTOR.** Only while green. No new behavior.

Code written before its test goes back through RED.

## Hooks run the tests

One `gate:` line for the edit after every Edit/Write. After it, one `gate: deferred`
line per earlier deferred job of this session, in any tree, that finished since:
each names its own tree and command, and judges that tree, not this edit.
Read them; never re-run what they ran.
`TIMEOUT` / `SKIPPED` / `QUEUED-SKIPPED` = untested, never a pass.
`BUILDING (deferred)` = result at the next hook — and the next hook fires on
your next Edit/Write, so ending the turn to wait for a notification deadlocks.
Wait in the FOREGROUND instead: `aphrollo gate status --wait <tree>`, with the
tree the BUILDING line names (the shell cwd may be a different checkout). `aphrollo gate stats`
= what the verdict WAS; `aphrollo gate output` = what that run actually PRINTED,
assertion lines unfiltered. Neither re-runs a suite.

Iterate with a compile-only command (`cargo check -p <crate> --tests`,
`go vet ./...`). Manual runs only: a mutation proof, a deliberate soak, ONE
targeted run after the hook said TIMEOUT/SKIPPED.

One mixed test+impl commit per task; the commit gate re-proves RED and runs the
touched suites.

## A test worth keeping

- Names the production change that fails it, and that change is a bug. No
  answer → no test.
- Expectation derived independently (literal `want`), never by the code under
  test.
- No change detector (constant value, exact message); assert the behavior.
- Tightest bar the class admits: exact bits for determined output; a tolerance
  only for approximate output, sized so a 1 % error still fails, justified inline.
- Real code over mocks; never assert on a mock.
- DAMP over DRY: readable top to bottom.
- Never weakened — not the assertion, the tolerance, or a ratchet baseline.
  BLOCKED with numbers beats green; pin a real failure as ignored, naming it.

## Existing code

No natural RED → **mutation proof**: name the ONE test expected to fail
first, introduce one specific error in production code (flip a sign, drop a
term), confirm THAT test — not merely "something" — is what moved, restore
byte-identically, report mutation + failing line. A generic red is not
evidence; the predicted line moving is. Before trusting any of that, confirm
the edit actually landed (`git diff --numstat` on the file, non-empty) — a
pattern that silently matched nothing (stale CRLF bytes, a typo, the wrong
worktree) leaves the file untouched and an unrelated green then reads as a
survivor that never existed. `aphrollo gate mutants prove` does both checks
mechanically, and restores for itself. By hand, the restore is from a HOLD,
never the index: `aphrollo gate mutants hold <file>` BEFORE the mutation,
then `MUTATION=1 git checkout -- <file>` puts back the held working state.
A bare `git checkout --` is refused over unstaged work, and the refusal exits
non-zero, so a `&&` chain stops there; the environment markers that force it
restore the index and destroy that work. Unmutated tests over existing code
certify nothing.

## Done means evidence

Name the command, read its output, then claim — quoting the green line and its
count, or the failing test's name. "Should work" is a guess. TIMEOUT/SKIPPED
stays reported as unproven.
