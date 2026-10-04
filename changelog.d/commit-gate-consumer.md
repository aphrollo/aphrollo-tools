level: patch

The commit gate now measures mutants in a Go module that sits below the repo root, and its passing lines name the command they ran.

### What you will notice

- `mutants-at-commit` in a multi-root repo whose go.mod is in a subdirectory (backend-go/go.mod beside a frontend) no longer prints "skipped (this repo is not a Go module)". The stage resolves the staged files' Go modules the way the rest of the commit gate resolves roots, runs `go test` from the module's directory, and names survivors by their repo paths, which is what `mutation-accept` entries match. `gate mutants edit` and `gate mutants testmap` resolve the module the same way.
- A passing check line reads `declared in <root> → clean (<command>, <time>)` instead of naming no command, and a command still running after fifteen seconds prints `→ running <command> …` once, so a long suite can be told from a hung gate. `gate premerge` prints the same lines.
