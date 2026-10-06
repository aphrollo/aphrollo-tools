level: minor

The post-edit hooks stop producing noise and the text they inject into a session shrinks; `/aphrollo off` is a real per-session switch.

### What you will notice

- A session start is one line, and the reply-style reminder goes out once per session (with the first prompt after a session start) instead of on every prompt.
- A red `gate:` line names the first failure, shows what fits in 400 tokens and points at `aphrollo gate output`, which serves the whole run. The commit gate's NOT RUN line and fail-first line are a count and one named example once the list is longer than three; the full list is on the gate-log event (`aphrollo why`) and in the run's retained output.
- `/aphrollo off|on|status` switches every session hook off for the session: edit-time guidance and denies, test runs, gate lines, turn-end checks, injected context and the statusline (shows `off`). The git-side gates and the secrets wall stay on. `/tdd off|on|status` is the same switch, `TRELLIS_OFF=1` switches a whole process, and each flip is an event counted as a wrong-block signal. `aphrollo install` writes the `aphrollo` skill the command needs and `aphrollo gate doctor` checks it.
- An edit a newer run superseded (a replaced queue entry, an edit that moved the tree under its own running job) now gets the verdict of the run that replaced it in the edit ledger.
- Another lane rebasing while a proof ran no longer reads as the proof changing git state.
- The `commit_gate` event of a `check-error-rejected` refusal carries the error text in its `detail` field.
