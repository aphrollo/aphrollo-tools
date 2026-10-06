level: minor

`workspace merge --wait` now rides out the failures of a bad hour: it asks GitHub to run again a job no hosted runner picked up, and it keeps following a PR across a dropped connection.

### What you will notice

- A job GitHub cancelled with no step run and the note "not acquired by Runner of type hosted" is asked for again, twice at most, with one line saying so, before the wait calls CI unavailable. A billing lock or any other cause is never asked again.
- A cancelled job of a CI run that a later run of the same workflow on the same commit replaced no longer counts (only one a concurrency group cancelled, with a later run of the same event that ran a job) as a failed check, so `merge --wait` stops refusing over the run its own concurrency group cancelled.
- A job nothing replaced after its re-requests ends the wait as CI unavailable, so the `ci = auto` fallback takes over.
- A status read that fails on the network, or is rate limited (429, a secondary limit; its Retry-After is honoured up to two minutes), is retried with a backoff inside the wait's timeout. After five failures in a row the wait says the PR's state is unknown and names `gh pr view`, instead of saying the PR did not merge.
- `workspace pr` without `--title` titles a multi-commit PR from its first commit, not its branch name. `workspace merge` refuses to enqueue a PR into a merge queue when its title would fail the commit-msg subject rules (a branch name, under four words, a list of files), and prints the `gh pr edit` that fixes it. A direct merge is not judged on its title.
- `aphrollo version` of a `go install ...@tag` build prints the release it is, such as `aphrollo 1.20.0 (module v1.20.0)`, where it printed `0.0.0-dev (unstamped)`.
- `ci run` makes its scratch under the root of the system drive on Windows, so test code a job runs stays under the path limit, and hands workflows the PR it judges. On Windows the scratch base is `C:phrollo-<user>`, readable and writable by that user and SYSTEM only (`github.event.pull_request.number`, `html_url`, `title`).
- The commit gate's mutation run measures a function it has not seen before against the tests the commit added or touched first, instead of the whole package.
- This repo's `test` job allows 1500 s to its race run, where a loaded box needed more than 600 s for `internal/cli`.
