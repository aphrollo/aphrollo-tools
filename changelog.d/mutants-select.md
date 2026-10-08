level: minor

A mutant a measurement settles by running it now runs only the tests that execute its position, where that saves time, so a package's integration suite is no longer re-run whole for every mutant.

### What you will notice

- A package is selected among only when its tests take longer to run than one rebuild of its test binary, which is what each mutant costs whatever tests it runs. The run times both once per package and tag set (kept by content) and logs the decision per package. A faster suite runs whole, exactly as before, and no per-test coverage is built for it.
- Per-test coverage is built on the spot, only for packages with mutants to settle in the run, once per tag set (the unit tests, and the `mutants-test-tags` set when declared): one compile of the test binary with `-coverpkg`, then each test run alone. The tagged set runs one test at a time. The coverage is kept by content and an entry that still holds is never rebuilt. The log says what the compile and the test runs of each package cost.
- Tests of other packages are run for a mutant only for packages listed in `mutants-integration-packages`; with none listed no importer is compiled or run.
- A mutant runs the unit tests that execute its position first, then the tagged tests that do, and only if it survived. A position no test executes is `not-covered` and is not run. A killed mutant is confirmed without the mutant and run once more; a pass there marks it `flaky`.
- Any doubt runs the package's full suite as before: no kept coverage, a changed file, a position Go coverage cannot count (a case clause's condition), a build or listing failure, a test that failed alone or wrote no profile. A test that starts the test binary again joins every selection of its package. Coverage that names a doubt is not kept.
- The run's log ends with one line: how many mutants ran selected tests, the full suite and why, and not-covered, and what the coverage cost to build.
