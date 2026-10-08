level: minor

A mutant a measurement settles by running it now runs only the tests that execute its line, so a package's unit and integration suites are no longer re-run whole for every mutant.

### What you will notice

- The run builds per-test coverage on the spot, once per tag set (the unit tests, and the `mutants-test-tags` set when the repo declares one), with `-coverpkg` set to the packages under mutation, so a test in another package that reaches the mutated line is credited. Only packages whose test binary links the mutated one are compiled; the tagged set runs one test at a time. The coverage is kept by content (sources, tests, testdata, tags, Go version, `mutants-env`, module files) and an entry that still holds is never rebuilt.
- A mutant runs the unit tests that execute its position first, then the tagged tests that do, and only if it survived. A position no test executes is `not-covered` and is not run. A killed mutant is run once more; a pass there marks it `flaky`, neither caught nor missed.
- Any doubt runs the package's full suite as before: no kept coverage, a file whose lines the coverage no longer speaks for, a position Go coverage cannot count (a case clause's condition), a build or listing failure, a test that failed alone or wrote no profile. A test that starts the test binary again joins every selection of its package.
- Coverage that names a doubt is used for the run and not kept: the next run measures it again, on whatever the box is then.
- The run's log ends with one line: how many mutants ran selected tests (and how many tests each ran on average), how many ran the full suite and why, how many were not-covered, how many packages the coverage covers and how long building it took.
- A mutant that survives its own package's tests can now be caught by a test in a package that imports it, where before only the mutated package's tests were run.
