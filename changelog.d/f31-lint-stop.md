level: minor

The edit hook's Go run now lints the packages the edit touched, so a finding a commit would refuse is named when the edit is made instead of at the commit.

### What you will notice

- A Go run's line ends with `lint guidance, not a test verdict: N lint findings a commit would refuse: ...` when golangci-lint found something. It is guidance only: the run's outcome, and what counts as not tested, are the tests' alone.
- The lint is the commit stage's own command over the touched packages, run by the run's own wrapper after its verdict is written and its slot released, under the lint lock; it replaces the edit-time fast lint, so a finding is reported once, and the edit hook starts no extra job. A linter that is missing, busy, or past its three-minute budget, a loaded box, and a lint still going when the run is harvested leave the line as it was.

A run's verdict is now also written to the lane's store, keyed by the worktree, and the Stop and SubagentStop checks read it.

### What you will notice

- Stop and SubagentStop block once on a red recorded for the lane's current tree that the session has not been told of. A red of an older tree, a run that was not tested, and a red with a green beside it on the same tree all allow. When no store verdict exists, or its write failed, the checks read the session's job records as before.

A lint that finishes after its run was reported is delivered at the next hook as `gate: lint <unit> in <tree> → N findings a commit would refuse: …`, once, unless a newer run of the same unit has started. A clean lint says nothing.
