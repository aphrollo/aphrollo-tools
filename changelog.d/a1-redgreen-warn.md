level: minor

The red-to-green rule now answers at the edit: a lane that edits code with no test run since gets one line of guidance, or under `enforce` a deny that names its override, and a suite you run from a shell counts as a run.

### What you will notice

- `tdd` now defaults to `warn`. A lane no file pins is assigned to the `enforce` or `warn` arm by a hash of its repo and name, the same on every box; a pinned `tdd` is outside the experiment. `aphrollo config show` says which arm the lane is in and why.
- A test run from a shell (`go test`, `pytest`, `vitest`, `npm test`, `cargo test`) counts as a run: green covers the edits it ran, red opens red. The `PostToolUseFailure` hook is wired for the red ones; `aphrollo gate init` adds it.
- Under `enforce` the deny can be waived for the session with `aphrollo gate allow red-green`, or left for good with `aphrollo config set tdd warn`. Under `off` nothing changes.
- `aphrollo stats --ab` prints per arm and per language the lanes, denies and warnings, overrides, escapes, friction and whether each arm has the 30 lanes the comparison needs.
