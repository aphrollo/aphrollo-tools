level: minor

The merge gate runs a Python project's tests under the project's own virtualenv, and a suite that could not import a third-party module is refused as not tested, not as a failing test.

### What you will notice

- A pytest root's tests run under the first interpreter that imports pytest, looked up in the project root's `.venv`, `venv` or `env`, then the same three at the top of its worktree, then (at a merge) in the lane being merged, then in the primary checkout (project root, then top), and only then on PATH. The choice is remembered per project root until a venv changes, so a repeat edit starts no python.
- A pytest run that fails at collection with `ModuleNotFoundError`, with no test executed and every error a missing third-party module, now reads `NOT TESTED` and names the module, the interpreter and the venv to build in the primary checkout. It is logged as `env-missing` at the edit hook and `env-missing-rejected` at the merge, both counted as not tested in `gate stats`. A module the repo itself ships, found from the Python files git lists, or any other kind of collection error, keeps the run a failing test.
- When every GitHub check on the PR head was skipped, `workspace merge` prints `ci: github (...) — no check ran on <sha> (all N skipped) — the local suite is the proof`, and `workspace merge --help` says so under `--ci`.
