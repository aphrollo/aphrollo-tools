level: minor

A Go merge now runs `-race` only over the packages the change touched, runs the packages that import them as a second run without it, and lets several `-race` runs share a box that has room for them.

### What you will notice

- A merge of a change to a package many others import (a regenerated API or sqlc package) no longer builds and races every importer. The importers still run, with the same `-count=1 -shuffle=on`, just without `-race`. The merge is green only when both runs pass, and a refusal names both runs and how each ended. A repo that wants `-race` over everything sets `race-scope = "all"` in `aphrollo.toml`.
- Merges no longer take turns on one `-race` lock. The number of `-race` runs at once is the smaller of free memory divided by 8 GB and cores divided by 8, never more than `APHROLLO_BUILD_SLOTS` (which cargo builds share), and one when free memory cannot be read, so a box with little room behaves as before.
- A merge waiting for a `-race` slot says `gate: merge queue position N of M (est. ~K min; holder: <lane> pid <n>)` on one line, at most once a minute, in place of the raw `queued behind` line. The estimate comes from recent `go test -race` runs in gate.log and is left out when there are none.
