level: minor

The merge gate runs a Python project's tests under the project's own virtualenv, and a suite that could not import a third-party module is refused as not tested, not as a failing test.

### What you will notice

- A pytest root's tests run under the first interpreter that imports pytest, looked up in the project root's `.venv`, `venv` or `env`, then the same three at the top of its worktree, then in the primary checkout (project root, then top), and only then on PATH. A lane with no venv of its own takes the primary checkout's.
- A pytest run that fails at collection with `ModuleNotFoundError` for a third-party module, with no test executed, now reads `NOT TESTED` and names the module, the interpreter and the fix ("the suite's python lacks `pyseto` — the repo's requirements are not installed in <interpreter>; build its venv"). At the edit hook it is logged as `env-missing`; at the merge it refuses the merge as untested. A module the repo itself ships that fails to import stays a failing test.
- When every GitHub check on the PR head was skipped, `workspace merge` prints `ci: github (...) — no check ran on <sha> (all N skipped) — the local suite is the proof`, and `workspace merge --help` says so under `--ci`.
