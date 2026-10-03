level: patch

The ratchet engine, the sqlc guard, `ci run`, the language-server spawn and the other helpers that read git or run a tool now start their children through the guarded runner, so a child that outlives its time limit or its caller takes its whole process tree with it.

### What you will notice

- A `ci run` step that reaches `--ci-timeout`, or is cancelled, now ends every process it started. On Windows an MSYS shell chain used to survive the step that was reported stopped. A step keeps its below-normal priority and its own environment.
- A language server started for `outline`, `show` or a rename is ended with the whole tree it started (a `cargo check` under rust-analyzer), whether the verb returns early or its context ends.
- The git reads behind a diff-scoped law, the sqlc drift guard, the commit identity check, `docs check`, the `gh` probe and `cargo metadata` / `go list` for the dependency-graph laws now carry a ceiling of ten minutes, where they had none. A child that reaches it is ended and the verb says so.
- A heavy step that cannot be placed under its guard still runs, and a line says it ran unguarded.

### What migrates by itself

- Nothing: output, exit codes and the environment each child sees are the same. `aphrollo dev` still runs `sudo` on the terminal as before.
