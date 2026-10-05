level: minor

Every setting a repo declares is now read through one TOML reader, and the user-facing `APHROLLO_*` environment variables became keys of the user's config.

### What you will notice

- `undercover = true # why` reads as true. The old line readers took the comment as part of the value and read it as false; every reader of `aphrollo.toml` and `Cargo.toml`'s `[workspace.metadata.aphrollo]` now goes through `internal/config/decl`, which drops a trailing comment, joins arrays that span lines, and names a line it cannot read without losing the keys beside it. A key written with a value it cannot read is still reported as written, so the readers that refuse a bad number keep refusing it.
- `aphrollo config show` also reads `Cargo.toml`'s metadata table as an alias: `undercover` is on when either file says so, `requires` is the stricter floor, and the mutation level reads `Cargo.toml` before `aphrollo.toml`, as the mutation reader always did.
- Ten variables became keys of the user's `config.toml`, each with the default and floor it had: `budgets.edit_s` (`APHROLLO_POSTEDIT_BUDGET_SECS`), `budgets.lock_wait_s` (`APHROLLO_LOCK_WAIT_SECS`), `budgets.cargo_wait_s` (`APHROLLO_CARGO_WAIT_SECS`), `budgets.git_wait_s` (`APHROLLO_GIT_WAIT_SECS`), `budgets.lint_wait_s` (`APHROLLO_LINT_WAIT_SECS`), `budgets.deferred_max_s` (`APHROLLO_DEFERRED_MAX_SECS`), `budgets.mech_total_s` (`APHROLLO_MECH_TOTAL_SECS`), `box.mech_parallel` (`APHROLLO_MECH_PARALLEL`), `box.build_slots` (`APHROLLO_BUILD_SLOTS`) and `reply_style` (`APHROLLO_REPLY_STYLE`).
- The old variable still works for one more release, over the file, and prints one line per process: `aphrollo: APHROLLO_LOCK_WAIT_SECS is deprecated and read for one more release; set budgets.lock_wait_s instead`. `aphrollo config show` names it as the source of the key. Move the value into `config.toml` (`aphrollo config set budgets.lock_wait_s 600 --user`) and unset the variable.
- `TRELLIS_OFF=1` turns the gate's session checks off for a process, where `/tdd off` does it for a session: the edit hook, the Stop and SubagentStop checks and the status line. The commit gate and git hooks are not touched, as with `/tdd off`.
- The remaining `APHROLLO_*` variables are test seams and markers the gate sets for its own children, and stay as they were. `APHROLLO_DEV_BIN`, which nothing read, is gone.
- Not moved yet: the `[aphrollo.precommit]` tables, whose inline tables and nested arrays the shared reader does not take.
