level: minor

The merge gate and local CI now reuse one checkout per repo instead of building a new one for every merge, so the build, lint and type-check caches that are keyed by directory stay warm; the lines `workspace merge --wait` prints now begin with the time.

### What you will notice

- A merge builds in `<repo's lanes dir>/gate-prmerge-warm` (local CI in `gate-prmerge-localci`), reset to the merge tree before each use: a file the last merge had and this one lacks, an untracked file and build output are gone. Only tsc's `*.tsbuildinfo` is kept. When another merge holds that checkout, this one builds in a fresh directory as before: correct, only cold. A checkout that is not a worktree of the repo is removed and made again.
- `aphrollo gate gc` removes a warm checkout only when it has sat idle past the gc age (3 days); `workspace prune` leaves it alone.
- Every line of `workspace merge --wait` output after the plan (`[wait]`, `[queue]`, the gate's stage lines, `ci:`, `merged`) starts with a UTC `HH:MM:SS `, so the gaps between stages can be read from the log. The final `aphrollo: <error>` line is unchanged.
