level: minor

`aphrollo gate gc --only <kind>[,<kind>...]` limits a sweep to the kinds you name, so you can trim the Go build cache without the sweep touching anything else.

### What you will notice

- The kinds are `incremental`, `gate-dir`, `orphan-worktree`, `temp-litter`, `mutants`, `mutants-target`, `mutants-temp`, `deps-member`, `deps-third-party`, `stray-target`, `gotmp`, `gate-prmerge`, `other` and `gocache` (the Go build cache trim). An unknown kind is refused with the list.
- With `--only` the state retention and the probe discard backup, mutants and legacy-job listings are skipped; `--dry` shows the plan for just those kinds.
- Without `--only` a sweep is unchanged.
