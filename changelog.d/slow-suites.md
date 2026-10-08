level: patch

A write that changes several Go files of one package now runs the tests that cover all of them, instead of the whole package.

### What you will notice

- With `test-select = "edit"`, an edit that touches `a.go` and `a_test.go` together runs the covering tests of the changed functions plus every test of the changed test file, and the gate line says `selected M of K tests`. A write that includes a file outside the package, a file that is not Go, or a change that cannot be mapped to functions still runs the whole package and says why.
- The workspace and CLI test suites build their git fixtures once per run and copy them, and run git itself instead of the queue shim, so a local run takes fewer process spawns.
