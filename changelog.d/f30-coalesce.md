level: minor

An edit made while a deferred run is going now gets a gate line of its own, and a write that only regenerates code no longer widens to every importer.

### What you will notice

- An edit behind a running run prints `gate: <command> in <root> → QUEUED (...)` naming the run it will start when the slot frees, its place in the queue, and that it has not run yet, instead of the running job's `BUILDING` line with no command. A newer edit of the same run replaces the waiting one. When six runs already wait, the edit prints `QUEUED-SKIPPED` and says the code was not tested. Queued runs start when the running one is harvested, on the tree as it stands, and report at a later hook like any deferred run.
- A run known to be measuring an older tree state says so and offers no `gate status --wait`, which could only return a verdict about code no longer on disk.
- A file that declares itself generated (`Code generated ... DO NOT EDIT.`) whose package has no tests of its own is no longer widened to the packages that import it. The line says so and leaves the generated file to the commit gate's regen check; the files that call it and changed in the same write have their own runs.
