level: minor

The merge wait no longer reads a required check as missing while the head's pipeline run is still queued, and the commit gate scans the staged diff for secrets.

### What you will notice

- `aphrollo workspace merge --wait` keeps waiting, within `--timeout`, while the required check is absent and a run of the workflow that makes it (`pipeline.yml`, or `ci-reuse-workflow`) is queued or running on the PR head; a run of any other workflow is not waited for. Once those runs have concluded without making the check, the gate calls it missing.
- The commit gate runs `gitleaks` over the staged diff before the suites, using the repo's `.gitleaks.toml` when present. A finding refuses the commit with file:line, rule id, fingerprint and the three ways to clear a false positive (`gitleaks:allow` in a comment, `.gitleaksignore`, an `[allowlist]`). With no `gitleaks` on PATH, or one that fails, it prints one `NOT RUN` line and lets the commit through.
- A regression test now holds a merge commit of 400 long-path packages to the command-line budget (the `command line is too long` failure on Windows).
