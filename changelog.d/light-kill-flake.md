level: patch

The light-child timeout test in the workspace package no longer fails at random on Windows CI.

### What you will notice

- Nothing in the binary changes: the test asked the OS about a shell's own MSYS pid, which names an unrelated Windows process on some runs. It now records the Windows pids of runtest's shell chain, as the gc timeout test does.
