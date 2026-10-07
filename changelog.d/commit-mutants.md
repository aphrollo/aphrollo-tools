level: minor

Commit-time mutation now measures coverage itself, at the commit that needs it, and nothing is built in the background any more.

### What you will notice

- In a repo that declares `mutants-at-commit`, the commit gate compiles each touched Go package's tests once with coverage, runs each test alone, and tests each mutant against only the tests that execute its line. A mutant on a line no test executes is named NOT MEASURED as `not-covered` and is not run. A mutant its covering tests miss is a survivor at once, with no second run of the whole package. Nothing runs and nothing is prepared in a repo that does not declare the key.
- The result is kept in the repository's git directory, keyed by the package's import path, the content of its test binary's inputs, the Go version and the build tags, so the next commit to the same package reuses it and skips the coverage run. `aphrollo gate gc` removes a map nothing has read for 30 days. A package whose coverage does not fit the commit budget is named NOT MEASURED.
- A new key, `mutants-test-tags` (a string array, empty by default), names the build tags a repo's tests need, such as `["integration"]`. The coverage build and every mutant run carry them, so code only a tagged suite executes is measured against that suite. A tagged suite that cannot run leaves its mutants NOT MEASURED, never survivors.
- The post-merge and post-commit hooks no longer start a detached test-map build, and `aphrollo gate mutants testmap` and `aphrollo gate mutants edit` are gone. The edit hook already started no mutation run; the code behind it is deleted.
- The lane warm-up build planned in the architecture doc is struck; dependency install stays.
