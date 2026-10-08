level: minor

`aphrollo report` now shows whether the gate is getting faster: a Speed block in the text, the `--json` model (`speed`), the weekly issue and the `report web` page.

### What you will notice

- Each stage the log times (the edit-time suite, edit to verdict, each commit-gate and merge-gate stage and their totals, mutation at commit, the merge queue, CI) shows its run count, p50, p90 and max in seconds, green runs and refusals alike, and the p50 change against the week before; a slower p50 reads as worse.
- PR lead time is the span from `pr_opened` to the merge of the same PR; a PR opened and never merged, or merged without an open record, is not counted.
- The commit and merge gate result events now carry the elapsed `secs` of the whole run, and the first-run `ci` event carries a `secs` detail (the first run's creation to the last run on that head to finish, only when every run finished, none was a rerun and both times are known), so the report reads the gate totals and the CI pipeline. A result's seconds are never added to the time lost: its stages already carry them. Mutation at commit is still not derivable, because the mutants stage line is written with 0 seconds; the report names it as such instead of guessing. Other `ci` events carry no `secs`, so the CI row fills from the first-run path only.
- Runs written by a binary that names no version stay in the stage row but are left out of the version split, and a first version with fewer than four runs stays on its own.
- A speed change against the week before, or between the binary versions of the window, shows only when both sides have four runs and an exact two-sided Mann-Whitney U test gives p under 0.05 (a normal approximation with tie correction above 50 runs or with ties); otherwise the cell reads `~`, as `benchstat` prints it. A version is confounded with the work done that week.
- The text report and the page now lead with a Changed block: the clear speed changes (slower first), rules new this week and rules gone, and not-tested runs and wrong blocks that went up; with nothing to show it says `no clear change against the previous 7d`.
