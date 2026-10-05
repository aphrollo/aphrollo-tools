level: minor

`aphrollo gate gc` now keeps the Go build cache bounded, and the sweep after a merge removes lanes that landed as squash merges.

### What you will notice

- `gate gc` trims the Go build cache (`go env GOCACHE`) down to `gocache-cap` in `aphrollo.toml` (20GB by default), removing the files unused longest first and only those unused for `gocache-age` (12h by default, never under 2h, and a shorter value is refused). It removes files only, never a directory, never the cache's README or trim.txt, so a build running at the same time is unaffected. It acts only on files named as go names its entries, in a directory holding go's README or trim.txt, and refuses a GOCACHE that is off, relative, or the home, temp or root directory, saying so. `--dry` prints what it would remove. The session-start sweep trims at most once every six hours.
- After a merge, a lane whose PR was squash-merged is removed with its worktree, node_modules, venv and branch: previously only a lane whose tip was an ancestor of trunk counted, so the sweep printed `examined 87 lane(s); pruned none`. A lane counts as landed when trunk already holds everything it changed, or when the merge verb recorded its merge and the lane has no newer commit. A lane with uncommitted work, a locked worktree, a dev-tier claim, or a session that worked in it in the last 30 minutes is kept, and the line says why.
- A release replay that was killed no longer leaves its clones in the temp directory: the next `gate gc` removes them, and a replay given `-work` removes what it made at the end (`-keep` leaves it).
- `gate doctor` warns under 30 GB free on the drives holding the Go build cache, the lane worktrees, the temp directory and the target directory, and names the three sizes, biggest first. A mutation measurement refused for lack of space now says "not enough disk".

- The gate's go and golangci-lint runs now build with `-trimpath` (`go-trimpath = "false"` in `[aphrollo]` opts out), so a repo's lane worktrees share one set of Go build-cache entries: building two packages from a second checkout added 198 cache files without it and 66 with it. A test that finds its repository through `runtime.Caller` sees a module path under it and should use its working directory instead.
- `gate gc` removes the output vite and vitest leave in the temp directory: a 21-character directory holding only `client/` and `ssr/` folders of hash-named files, idle for a day and held by no live process.
- `gate gc` removes the legacy `jobs` subdirectories of the Claude config directory once nothing in one has been written for a week, unless a deferred-job record started in the last day points into it.

### What migrates by itself

- Nothing to do: the defaults apply to every repo. Set `gocache-cap = "50GB"` or `gocache-age = "24h"` in `[aphrollo]` to change them.
