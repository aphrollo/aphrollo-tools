level: minor

The edit hook's Go run now lints the packages the edit touched, so a finding a commit would refuse is named when the edit is made instead of at the commit.

### What you will notice

- A Go run's line ends with `lint guidance, not a test verdict: N lint findings a commit would refuse: ...` when golangci-lint found something. It is guidance only: the run's outcome, and what counts as not tested, are the tests' alone.
- The lint is the commit stage's own command over the touched packages, run inside the run's existing build slot after its tests; the edit hook starts no extra job. A linter that is missing, busy, or past its three-minute budget leaves the line as it was.
