level: minor

A mutant a measurement settles by running it now runs only the tests that execute its position, where that saves time, so a package's integration suite is no longer re-run whole for every mutant.

### What you will notice

- A package is selected among only when its tests take longer to run than one rebuild of its test binary, which is what each mutant costs whatever tests it runs. The run times both once per package and tag set (kept by content) and logs the decision per package. A faster suite runs whole, exactly as before, and no per-test coverage is built for it.
- Per-test coverage is built on the spot, only for packages with mutants to settle in the run, once per tag set (the unit tests, and the `mutants-test-tags` set when declared): one compile of the test binary with `-coverpkg`, then each test run alone. The tagged set runs one test at a time. The coverage is kept by content and an entry that still holds is never rebuilt. The log says what the compile and the test runs of each package cost.
- Tests of other packages are run for a mutant only for packages listed in `mutants-integration-packages`; with none listed no importer is compiled or run.
- A mutant runs the unit tests that execute its position first, then the tagged tests that do, and only if it survived. A position no test executes is `not-covered` and is not run. A killed mutant is confirmed without the mutant and run once more; a pass there marks it `flaky`.
- Any doubt runs the package's full suite as before: no kept coverage, a changed file, a position Go coverage cannot count (a case clause's condition), a build or listing failure, a test that failed alone or wrote no profile. A test that starts the test binary again joins every selection of its package. Coverage that names a doubt is not kept.
- The run's log ends with one line: how many mutants ran selected tests, the full suite and why, and not-covered, and what the coverage cost to build.
- A mutant in a package with no tests of its own that is not listed in `mutants-integration-packages`, a mutant with no known position, and a listed package whose unit suite is cheap all run the full suite.
- The coverage's key covers the module files, the package's files, its `testdata` and any subdirectory without Go files (what `//go:embed` may name). A tree that changes while the coverage is measured is not kept.

### Known limits

- A test that reads a fixture outside its package directory, or whose coverage varies from run to run, can leave the kept coverage out of step with what the test executes; the key cannot see either. Clearing the `aphrollo-mutcover` directory under the git directory rebuilds it.

### Intended difference

- Tests of the packages in `mutants-integration-packages` are credited through `-coverpkg`, so a test of an importer can now kill a mutant the old path left surviving. Outcomes can differ in that direction only.
