# Property tests and fuzzing

A property test pays for some shapes of code, and is never a blanket rule:

- a round-trip pair (`Parse`/`Format`, `Marshal`/`Unmarshal`, `Encode`/`Decode`): `parse(format(x)) == x`;
- idempotence: `f(f(x)) == f(x)` for a normaliser or canonicaliser;
- a reference implementation: a fast or optimised function agrees with a simple one;
- model-based, for stateful code: random command sequences checked against an in-memory model;
- untrusted bytes in: a Go `Fuzz` target, its seeds under `testdata/fuzz/`.

Elsewhere an example test is the right tool. Property tests here use `pgregory.net/rapid`.

## A red from rapid

A rapid failure prints its seed. A gate red from a property test is a real bug, not a flake:
replay it with `go test -run <Test> -rapid.seed=<n>` on the same package, then fix the code or
add the shrunk input as an example test.

## Fuzzing

No gate fuzzes. The nightly CI job runs every `Fuzz` target for 60s each, keeps the corpus between
nights, and on a crash uploads only the new reproducers and opens an issue. A test fails when a
`Fuzz` target is missing from the nightly job's list. A reproducer is committed under
`testdata/fuzz/` and is a deterministic regression test from then on.
