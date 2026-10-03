level: patch

A mutation run, the lint an edit starts, a lane's own build and every git read the gate makes now end their whole process tree when they are given up on, so a test binary is no longer left holding memory after a measurement is cancelled.

### What you will notice

- A mutation measurement that is cancelled or runs past its patience ends the mutation tool and everything it started. On Windows a `*.test.exe` could be left stuck in kernel exit for hours; the tool now runs in a job that kills on close.
- The gate's git calls, `go list`, the `golangci-lint` version probe, `git config --global` in install, and the `git ls-remote` of the behind-main check run with a time limit and with git and gh prompts off, so a call that would ask a terminal a question fails at once instead of waiting.
- A guard that cannot be set up on a box never turns a run red: the child runs unguarded and one line on stderr says so.
- Verdicts, gate lines and timeouts are unchanged. A deferred phase, a lint run and a map build that must outlive the hook that starts them are still started detached.
