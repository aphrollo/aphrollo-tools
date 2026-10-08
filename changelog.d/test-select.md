level: minor

A Go repo that keeps a coverage store can let an edit run only the tests that cover it, with `test-select = "edit"` under `[aphrollo]`. It is off by default.

### What you will notice

- Nothing changes until a repo sets the key. With it off, the post-edit `go test` argv is the one it always was, byte for byte.
- With it on, an edit to a function of a Go package runs `go test <pkg> -run=^(T1|T2)$`: the tests the coverage store (the one commit-time mutation keeps under the git dir) says cover that function, every test in a test file the edit changed or added, and every test the store has no measurement of. An edit to a test file runs that file's tests.
- Anything doubtful runs the package whole: no store, a store measured under other build inputs, an entry that no longer holds, a function no entry names, an edit to a var, const, type, import, `init`, `TestMain` or a test helper, or an edit that touched several files. The edit stage never builds coverage itself.
- The line says what ran: `green (12 passed, selected 3 of 40 tests: covering Parse, 0.4s)`, or `green (40 passed, full suite: no coverage store for internal/p yet, 2.1s)`. A selected green is never shown as a full one, and the gate event carries `selected` and `total`.
- The precommit fail-first, the merge, CI and mutation always run every test.
