level: minor

The merge wait no longer reads a required check as missing while the head's pipeline run is still queued, and the commit gate scans the staged diff for secrets.

### What you will notice

- `aphrollo workspace merge --wait` keeps waiting, within `--timeout`, while any workflow run of the PR head has not concluded, and prints how many are still queued or running. Only once every run has concluded without making the required check does the gate call it missing.
- The commit gate runs `gitleaks` over the staged diff before the suites, using the repo's `.gitleaks.toml` when present. A finding refuses the commit with file:line, rule id and the `// gitleaks:allow` escape. With no `gitleaks` on PATH, or one that fails, it prints one `NOT RUN` line and lets the commit through.
- A regression test now holds a merge commit of 400 long-path packages to the command-line budget (the `command line is too long` failure on Windows).
