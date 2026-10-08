level: minor

A new test that is already green on the old code is now accepted with a recorded mutation proof, and a few small CLI notices stop being wrong.

### What you will notice

- A commit that adds a pin or regression test (green before the change) is no longer forced into `split-commit` when `aphrollo gate mutants prove --want-fail <Test>` has killed that test on the code as staged. The proof is keyed to the content that was broken: edit the file afterwards and it no longer counts. The refusal now names this route beside `split-commit`.
- When the refusal is a `package.json` or lockfile bundled with tests, the message says so and prints the exact `git commit -m ... -- <files>` line that commits it alone.
- `aphrollo ratchet check` with no `--base` defaults to the merge base with `origin/<default branch>`, so `test_removed` runs, and prints the base it used. With no origin the skip message is unchanged.
- The "aphrollo binary is behind origin/main" notice now appears only in the aphrollo-tools repo itself.
- `aphrollo doctor` judges the PATH the current process has for the shim directory, and reports the system registry order as an info line.
