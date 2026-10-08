level: patch

The Speed section of `aphrollo report` is readable and complete.

- A gate's rows are its stages (fail-first, go test, lint, mutants, ratchet check), not one row per command line.
- A duration longer than 30 days is left out and counted. A deferred run abandoned before it started used to log the largest duration there is, which read as a 2562047-hour edit suite, and it now logs no time.
- The merge queue is timed for a PR the verb waited on: the merge record carries `enqueued_at`.
- The CI pipeline is timed from the settled run, from its first check's start to its last check's end, recorded as `secs` on the `ci` event.
- An empty row says what would fill it.
