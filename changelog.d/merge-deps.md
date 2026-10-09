level: patch

The merge gate no longer refuses a merge as "tests failing" because its checkout resolved modules from two lanes.

### What you will notice

- The merge checkout links a lane's `node_modules` only when every top-level package of it resolves inside that lane. A lane whose package points into another lane (a donor install) is not linked; the gate says which package and where it resolved, and installs for itself.
- That install is kept by the hash of the lockfile and manifest, beside the merge checkouts in `.depcache`, and the next merge with the same lockfile links it instead of installing again. An install unused for 14 days is swept.
- A suite run that died of a duplicated module ("more than one copy of React", "Invalid hook call") is refused as NOT TESTED, logged `infra-failed`, with the `node_modules` paths that resolved outside the checkout, not reported as failing tests.
- `workspace prune` no longer keeps a merged lane only because a generated test report (`.output`, `test-results`, `playwright-report`, `.last-run.json`) is newer than its last commit.
