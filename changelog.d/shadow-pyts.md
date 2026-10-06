level: minor

The shadow record now scores Python and TypeScript projects the way it scores Go packages, keeps its budget on a clock and counts every record it drops, and `aphrollo stats --shadow` reads agreement per language.

### What you will notice

- A pytest or vitest run folds into the lane record as the run of its project, a green run after the newest edit covers the project, and a red run is its red fact. A red-green would-be block of a Python or TypeScript project is joined to the commit proof run in that project instead of staying open.
- `aphrollo stats --shadow` prints a `budget drops` line (a count, and a percent from ten records) and one `language` row each for go, python and ts, with the holdout arm's fires counted in the row.
- A record that outruns its budget is always written as unjudged with its cause; a queued run and facts that did not finish used to be lost silently. No live gate decision changes.
