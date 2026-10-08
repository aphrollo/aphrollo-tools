level: minor

`aphrollo report` now shows whether the gate is getting faster: a Speed block in the text, the `--json` model (`speed`), the weekly issue and the `report web` page.

### What you will notice

- Each stage the log times (the edit-time suite, edit to verdict, each commit-gate and merge-gate stage and their totals, mutation at commit, the merge queue, CI) shows its run count, p50, p90 and max in seconds, green runs and refusals alike, and the p50 change against the week before; a slower p50 reads as worse.
- PR lead time is the span from `pr_opened` to the merge of the same PR; a PR opened and never merged, or merged without an open record, is not counted.
- A duration the log does not carry yet is named as not derivable, with the smallest event change that would add it, instead of being guessed. Today that is the commit and merge gate totals, mutation at commit and the CI pipeline.
- A speed change against the week before, or between the binary versions of the window, shows only when both sides have four runs and an exact two-sided Mann-Whitney U test gives p under 0.05 (a normal approximation with tie correction above 50 runs or with ties); otherwise the cell reads `~`, as `benchstat` prints it. A version is confounded with the work done that week.
- The text report and the page now lead with a Changed block: the clear speed changes (slower first), rules new this week and rules gone, and not-tested runs and wrong blocks that went up; with nothing to show it says `no clear change against the previous 7d`.
