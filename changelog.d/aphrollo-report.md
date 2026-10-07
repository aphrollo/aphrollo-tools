level: minor

`aphrollo report` is a new verb: a weekly continuous-improvement report built from the event log the gates already write.

### What you will notice

- `aphrollo report` prints six sections (friction per rule, wrong-block candidates, escapes by class and the stage that should have caught them, the A/B and shadow per arm and language, the token cost of what the harness injects, and proposals), each number with the event seqs behind it for `aphrollo why <seq>`. It only proposes; it never changes a rule.
- `aphrollo report --issue` opens one `Report <ISO week>` issue in the repo's own remote and closes the previous week's with a comment linking it; a second run in the week changes nothing. The first time both A/B arms hold 30 lanes it opens one `A/B ready: <repo>` issue. `--dry` previews.
- A seventh section reads the agent harness's local transcripts (aggregates only; no prompt, code or tool text is ever copied) for tokens per day, lane (joined from the event log and the worktree calls, with coordination and unattributed rows), coordinator or subagent and model, the top sessions, a notional cost computed at read time, and the share of the input that aphrollo's hook text is. `--compare-at <date|sha>` compares before and after.
- `aphrollo report web` writes the same report as one self-contained HTML page (inline CSS and SVG charts, light and dark, readable on a phone; no script and nothing fetched) to the git common dir's `aphrollo-report/` directory, prints the path and opens it in the default browser. `--out` chooses the path and `--no-open` skips the browser. No server is started.
- The daily gc sweep files the weekly report on its own, once 7 days after the last, in a repo with a GitHub origin. Set `report = false` in `aphrollo.toml` to turn it off.
