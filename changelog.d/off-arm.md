level: minor

A repo can now run a share of its lanes with aphrollo off, to measure whether aphrollo helps: `ab-off-share` (a percent, default 0) in the `[aphrollo]` table of `aphrollo.toml`.

### What you will notice

- With the default 0 nothing changes. With a share above 0, a lane is off when a hash of the repo and the lane's name falls below it, independently of the enforce/warn assignment; a lane pinned by `tdd` is never off.
- In an off lane no hook answers and no gate judges: the session hooks, the guardrail walls, the git hooks (pre-commit, pre-merge, the gate note, the lane sweep), the git shim's walls and the mutation measurement before a PR all stay silent. The undercover checks (commit message and push) still apply. Only the lane's arm, its commits (time and sha) and its merge are recorded.
- `workspace merge` of an off lane lands the PR like any other, with every local gate skipped and no CI condition of aphrollo's; GitHub's own refusals still stand.
- `aphrollo stats --ab` and the weekly report gain an on vs off section when the log holds an off lane: escapes per lane (primary), lead time, agent cost per lane and friction (an off lane counts 0 by definition), judged like the enforce/warn A/B. An escape is blamed on its lane from `gate escape record`, from issues titled `escape:` naming a PR or merge sha, and from the first red run on main after a merge. The A/B-ready issue covers the on/off verdict too.
