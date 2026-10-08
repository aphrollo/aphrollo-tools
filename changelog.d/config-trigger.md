level: patch

An edit to the repo's `aphrollo.toml` at the edit stage now runs only the one test that reads that file instead of the whole `internal/tdd/mutation` package; the commit gate and the merge still run the package whole, and a missing reader keeps the whole package.
