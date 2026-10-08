---
name: tdd
description: TDD gate — /tdd status|off|on|reset|style terse|plain; the body is the RED→GREEN procedure
argument-hint: "[status|off|on|reset|style terse|plain]"
---

<!-- Written by `aphrollo install`; edit the template in aphrollo. -->

`/tdd <arg>` is handled by the hook `aphrollo gate userpromptsubmit`. Reading this after typing
it means the hook is missing: say so and stop.

1. **RED.** One test, one behavior, named for the break it catches. `red-missing-impl` is a clean
   RED; `red`: is it the failure you meant? `red-bogus` is broken setup; `green` on a new test means
   the behavior exists: mutation proof.
2. **GREEN.** The minimum that passes. **REFACTOR** only while green.

Code before its test goes back through RED.

## The gate runs the tests

Read the `gate:` lines; never re-run what they ran. `TIMEOUT`/`SKIPPED`/`QUEUED-SKIPPED` are
untested. On `BUILDING (deferred)` wait in the foreground (`aphrollo gate status --wait <tree>`),
never by ending the turn. A Bash script is a fine multi-file edit: it gets the Edit gate.

## A test worth keeping

- It names the bug that fails it; else delete it.
- A literal `want`, never computed by the code under test; no change detector.
- Exact for determined output.
- Never weaken a test, tolerance or baseline: report BLOCKED.

## Existing code

No natural RED: a **mutation proof**. Name the one test that should fail, introduce one error
(`aphrollo gate mutants prove` restores), confirm that test moved. By hand,
`aphrollo gate mutants hold <file>` first, then `MUTATION=1 git checkout -- <file>` restores.

## Property tests

A property test pays for some code, never a blanket rule: a round-trip pair (Parse/Format,
Marshal/Unmarshal), idempotence (f(f(x)) == f(x)), a reference implementation to compare with,
a model-based test for stateful code, a fuzz target for untrusted bytes. Else an example test.
