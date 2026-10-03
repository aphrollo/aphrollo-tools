level: patch

A shell call run in the background is no longer refused as a long foreground wait, and a heavy child no longer outlives the aphrollo process that started it on unix.

### What you will notice

- A Bash call the harness runs with `run_in_background: true` is not refused for a long `sleep` or a `watch`/`follow` command. A background call blocks nothing. The other guardrail rules still apply to it, so a background python reading the null stdin is still refused.
- On Linux, a heavy child (a build, an install, lint, a test run) is ended by the kernel when the aphrollo process dies, even by a SIGKILL. The kernel signal reaches the child itself, not the processes it started.
- On every unix, a SIGINT or SIGTERM sent to aphrollo is forwarded to the process group of each live heavy child before aphrollo ends as it did before. Ctrl-C at the terminal no longer leaves such a child running. Windows is unchanged.
