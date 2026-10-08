level: minor

The merge gate no longer re-runs a declared precommit command whose inputs the merge did not change.

### What you will notice

- A command in `[aphrollo.precommit]` can be written `{ argv = ["npx", "eslint", "src"], inputs = ["src/**", "package.json"] }`. The commit gate records its verdict with a hash of the files those globs select. When the merge gate finds the same hash on the merged tree under a green entry, it prints `[reuse] <command>: inputs unchanged since the lane's green (<hash>)` and skips the command, and the gate log records the seconds saved.
- A command with no `inputs`, a red or missing entry, an unreadable store or a changed tool runs as before. A command with `baseline = "lines"` never reuses.
