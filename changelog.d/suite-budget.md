level: patch

A deferred test run is now given the time its own record says it needs, and a run that ends without a verdict says why.

### What you will notice

- The ceiling a deferred run is abandoned at, and the `-timeout` a deferred `go test` is handed, were one flat 600s (plus 5 minutes for go). A package whose suite takes 607s alone, or 1775s on a loaded box, could never end with a verdict. Each is now sized from the command's own recorded runs: the p90 of the last 20 green or abandoned runs in the event log, times a 1.5 headroom, times the box's current load (1 + load, so a fully busy box doubles it). The floor is the old 600s, the cap is 60 minutes, and a command with no record keeps exactly what it had.
- An abandoned run now names its budget: `budget 900s, from measured p90 600s × 1.5 headroom × 1.00 load (10 runs)`.
- A run that ran no test while the tree changed under it (a rebase, a merge) reports `tree moved during the run, not tested` instead of `writing-test`, and is logged as `tree-moved`, a not-tested verdict.
