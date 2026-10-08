level: minor

The merge gate no longer re-runs a declared precommit command whose inputs the merge did not change.

### What you will notice

- A command in `[aphrollo.precommit]` can be written `{ argv = ["npx", "eslint", "src"], inputs = ["src/**", "tsconfig.json"] }`. The commit gate records its verdict with a hash of the files those globs select, plus the root's lockfiles and manifests. When the merge gate finds the same hash on the merged tree under a green entry, it prints `[reuse] <command>: inputs unchanged since the lane's green (<hash>)` and skips the command.
- Only what `inputs` lists is covered, besides the lockfiles and manifests: environment variables and any other file are not. Put tool configs (`tsconfig.json`, the eslint config) in `inputs`.
- The command runs as before when it has no `inputs`, a glob matches no file, a glob selects a git-ignored file (other than under `node_modules`, which the lockfiles stand for), a glob starts with `/` or `..`, the entry is red or missing, the tool changed, or it has `baseline = "lines"`.
- `aphrollo report` counts each reuse and adds up the seconds saved in a "declared reuse saved" row.
