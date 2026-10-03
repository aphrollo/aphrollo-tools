level: patch

A short-lived child on Windows (a `git`, `gh`, `tasklist`, `go list` or `cargo metadata` call) is now held in a job object, so a child that reaches its time limit, or whose caller gives up, takes its whole process tree with it.

### What you will notice

- A timed-out child no longer lingers: the tree was ended by walking it from the child's pid, which missed processes under load and the grandchildren of an MSYS shell, and those could keep running after the verb reported the child stopped.
- A child that exits on its own still leaves what it started running, as before.
- A box that refuses the job still runs the child, ended at its time limit the old way, with no notice line.

### What migrates by itself

- Nothing to do: output, exit codes and the environment each child sees are the same, and Unix is unchanged.
