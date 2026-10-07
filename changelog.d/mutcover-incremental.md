level: minor

Commit-time mutation on a big package now fits the commit budget on every commit, not only on a byte-identical retry.

### What you will notice

- The coverage the commit gate measures is kept per test and per function instead of one blob per package keyed by the content of everything the package imports. A commit that edits one function measures again only the tests that ran it; a function that moves, or a file that is renamed, keeps its entries. A warm commit with nothing to measure compiles and runs nothing.
- With no kept coverage, the gate no longer runs every test of the package (about 8 minutes for the largest package of this repo). It measures first the tests that can reach the functions your commit changes, found statically by name through helpers, package variables and method values, then keeps measuring the rest of the package while half of that package's share of the budget lasts, so the store fills over a few commits.
- A map in which some test has no measurement is partial. On a partial map a mutant the measured tests kill is still caught, but one they miss, and a line none of them ran, is reported NOT MEASURED as `partial`, never as a survivor and never as not covered. Only a map with every test measured calls a mutant a survivor or a line uncovered.
- A deadline no longer throws away finished work: every test's coverage is saved as it completes, and the store is saved when the budget cuts the run. The coverage build and the mutant runs share one disposable copy of the lane.
- The coverage phase spends at most half of the time the commit has left. The coverage kept in the git directory is replaced by a new format; the old files are never read and are removed by `aphrollo gate gc` with the rest. The content of imported packages and of `testdata/` is no longer part of the key, which is documented in `docs/mutation-runner.md`.
