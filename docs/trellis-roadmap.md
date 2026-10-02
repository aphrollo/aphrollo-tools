# aphrollo-tools roadmap

**Proposed 2026-10-02, pending the owner's approval.** Until it is approved in review, the revision on `main` is the plan in force. This file is the source of the roadmap. The tool is named trellis from the move to its own repo onward (Decided, below); until then the code is aphrollo-tools.

## Goal

The owner, 2026-10-02:

> trellis helps the agent write better code faster, like a trellis for a plant: it supports and guides, it does not cage. You let the agent work on your repo (fanvue, etc.) and what it merges does not break, without the gate slowing it down or blocking it wrongly.

Three measures say whether it does. Each is recorded per consuming repo, and the targets are proposed until a month of data confirms them.

| Measure | What is counted | Where the data comes from | Target |
| --- | --- | --- | --- |
| Speed | Task start to merged PR, p50 | `events.jsonl`: lane opened, merge recorded | falling month on month, per repo |
| First-try quality | PRs green in CI on the first run; escaped defects per 100 merged PRs in consuming repos (a revert, a fix commit on the same lines within 7 days, a regression issue) | GitHub's CI history, git history of the consuming repo, its issues | first-run green at or above 85%; escaped defects falling every week |
| Friction | Gate wall time per task; agent tokens spent on gate round trips per task; wrong blocks | gate stage timings in `events.jsonl`; a local per-task token count from the session; wrong blocks detected automatically, not only through `gate feedback` | commit gate p95 under 60 s and merge gate under 5 min on Windows; wrong blocks near 0 |

A wrong block is detected when a deny is followed by an override or by `tdd` set off, when a test is deleted before the merge, or when a test is vacuous (it passes with the code under test removed). `gate feedback` stays as one more source.

The rule: every phase and every feature is judged by whether it moves these numbers. Anything that does not is cut.

## Principle: guide, not cage

The default is fast feedback and a named next step: what to do, why, and the override. A hard block is only for real damage (a write to the primary checkout or to main, a test touching the real repo or the global git config, a secret) or where measured data shows the block saves more time than it costs. Every deny carries a rule id, its cause and the override command. A stage that blocks and does not pay for itself is demoted to a warning (F.b).

The git-shim walls stay until per-repo git hooks replace them: refusing `--no-verify` and `-c core.hooksPath` on the primary checkout, and a move off main. They are real-damage walls that nothing else holds.

## Where it stands

The fixes keep landing fast, but each round adds load and the gate got worse. The 2026-10-02 audit:

| Measure | Value | Source |
| --- | --- | --- |
| PRs green in CI on the first run | 91% on 09-25 to 09-27, 56% on 09-29 to 09-30, 54% on 10-02 | GitHub CI history, all boxes |
| Edit-hook runs that never tested the code | 40%, 10-02 | gate log, Windows box, one day |
| Merge-gate runs that blocked / timed out | 66% / 6%, 10-02 | gate log, Windows box, one day |
| Gate time per change before CI | about 28 min, 10-02 | gate log, Windows box, one day |
| Commits fixing something merged 7 days earlier or less | 15% overall; 39% on 09-26 to 09-30 | git history, this repo |
| Lines added in 14 days | +158,000, 57% of the Go code | git history, this repo |
| Issues opened against closed, 14 days | 199 against 190; 27% caused by this repo's own changes | GitHub issues |
| Escapes in 7 days, by cause | 12 local-against-CI mutation disagreements, 11 the gate's own canary, 3 real CI test reds | gate log, both boxes |

Only the CI, git and issue rows cover several days and both boxes. The hook-level rows (edit runs, merge gate, gate time) are one Windows day; earlier days ran on the Linux box. The Linux baseline is still to be run, and budgets are fixed only after it.

What the numbers say: 23 of the 26 escapes are the gate disagreeing with itself, and only 3 are product defects a test should have caught, so a target on all escapes rewards the wrong work. The gate is slow and often untested. Growth outran structure: a fix lands in one call site and misses its sibling. State and processes are scattered across packages, which is where the Windows fork bomb (#997), the 26 GB mutant (#1005) and the writes into the real repository (#1043) came from.

## Compatibility policy

A `trellis update` never turns a green consuming repo red on its own. Every change that could is versioned, migrated automatically and announced.

1. **Versions.** Done in #1106 (1.0.0). Each release has a semantic version next to the build stamp; a change to any verdict a consumer sees (laws, masks, gates, mutation) is at least a minor bump. Every persisted format carries its own format number, and append-only logs one per record; readers skip record versions they do not know. A repo declares the oldest trellis it accepts (`requires`); an older binary refuses and names `trellis update`.
2. **Consumer changelog.** Done in #1106 (`CHANGELOG.md`, checked on every bump): what a consumer will notice and what migrates by itself.
3. **Migration.** Derived data (caches, the managed CLAUDE.md block, shims) is recomputed by whichever binary runs. Committed data (baselines, `trellis.toml`) migrates on first run, lands as its own commit, and a format change ships in two releases: first read-new/write-old, then write-new. Local state migrates in one release after a `.bak.<version>` copy; an older binary meeting a newer format refuses by name. Never a required manual step.
4. **Release replay.** Moved into F (below): each release is replayed against this repo and a synthetic tree of fanvue's size, on Linux and Windows, and ships only with zero new hits on unchanged trees. It also runs the previous binary on the data the new one migrated. Pinned public repos per language come later, in 1a. Nothing leaves the box.
5. **Safety invariants.** A test never touches a real repo or the global config (#1043). No child runs without a memory cap (#1005). No hook writes a shim pointing at a temporary binary (#1033). Local CI never installs globally and refuses a host marked production (#1102). From F these hold because every exec goes through `internal/run`.
6. **Rollback.** `trellis update --to <version>` reinstalls an earlier release and pins it; the last 3 binaries stay on the box; local state restores from the `.bak.<version>` copy. Committed data stays readable one release back by item 3.

## Roadmap

Phases run in order; each starts when the previous gate is met, and nothing new enters a running phase. All are built in aphrollo/aphrollo-tools; the move to a new repo is last. No dates, except F's proposed timebox.

| Phase | What | Moves | Gate |
| --- | --- | --- | --- |
| 0 | Unblock fanvue: finish #1102 and #1103 | Friction, safety | fanvue merges through local CI |
| F | Foundation: spikes, one mutation authority, measurement, fast tests, lane state machine, the spine | all three | below |
| 1a | Core on the spine: red to green, guardrails, escapes, edit results | quality, friction | a release replays with 0 new hits |
| 1b | Plugin: launcher, config, session and repo start, lanes, explainability | friction, speed | setup on a new box is one step |
| 3 | Integration: `trellis ci` under the merge verb, merges without GitHub | speed | a merge completes without GitHub |
| Move | To harryberg1n/trellis, last, after the plugin work is proven | none | one migration |

### Phase 0

Landed: the event log (#1091, #1095), local CI from the repo's own workflow (#1092), lane-owner commit records (#1094), the Windows fixes (#1097, #1099, #1110, #1112), the Windows smoke job (#987), install PATH (#1100), the related-runner guard at commit (#1105), merges made outside `workspace merge` recorded (#1107), the Stop and TaskCompleted checks (#1111), go test lists cut to the budget (#1113), versions and the changelog (#1106), and smaller fixes (#1089, #1090, #1093, #1096). Open: #1102 (local CI ran `pip install` into a production host's global Python) and #1103 (parallel jobs at normal priority hit the deadline). The gate is narrowed to those two: fanvue merges through local CI that is serial, low priority, never a global install, and refused on a production host.

### Phase F: Foundation

F comes before the rest of 1a because every 1a workstream adds state, processes and hook logic, and each is built a different way in each package. F builds the one way, measures what the gate costs, and moves the existing code onto it. It adds no new feature; any change to a verdict a consumer sees is listed in the changelog and versioned. **Timebox: about 3 weeks (proposed, the owner confirms).** If the exit gate is unmet at the end, ship what landed and re-plan. Consumer verdicts are frozen during F except F.e, which ships as a versioned minor change. Parts run in this order.

**a. Spikes (about 1 day).** Record real hook payloads for every event trellis uses, on Linux and Windows, for the main session, a subagent, an isolated subagent and after EnterWorktree; commit them as fixtures, never hand-written. Time a no-op hook through the launcher on Windows (p50, p95). Measure how often the agent enters a lane after a deny that names its path. Check whether `CLAUDE_ENV_FILE` reaches the PowerShell tool.

Recorded 2026-10-02 from a real `claude -p` session on Windows: SubagentStop's cwd is the isolated subagent's own worktree and it carries `agent_transcript_path` and `last_assistant_message`; `stop_hook_active` exists on Stop and SubagentStop and is false on the first stop; PreToolUse in a subagent carries `agent_id`, `agent_type` and the subagent's worktree as cwd; after EnterWorktree the cwd follows into the worktree and moves back on ExitWorktree; PostToolBatch fires (8 times for 10 tool calls). So #1111 stands as built; its tests swap their hand-written payloads for these recordings. Still to do: TaskCompleted in an interactive session (TaskCreate is not available in `-p`), launcher latency, follow rate after a deny, `CLAUDE_ENV_FILE` for PowerShell, the Linux recordings.

**e. One mutation authority.** CI's mutation verdict is the one that blocks; commit-time mutation prints its survivors and never refuses. This ends the local-against-CI disagreements (12 of 26 escapes in a week) and closes #1078. The mutation rules leave the agent brief. Shipped as a versioned minor change.

**b. Measurement.** The three goal measures, recorded per repo from `events.jsonl`, git and CI. Escapes split into product escapes (a test red in CI or a defect after the merge, on a head the local gates passed) and gate disagreements (local against CI mutation, the gate's canary, a timeout read as a verdict). Per-stage cost and catch counts, where a catch is a block whose lane later changed code before passing and was not marked wrong. Wrong-deny recording (above) lands here. A demotion policy: a stage with no catch in 4 weeks, or more disagreements than catches, moves to warn or to CI only, as an event and a changelog line. A length cap on the agent brief, enforced as a CI check. Budgets are set from the Windows and Linux baselines.

**f. Fast test tiers.** Pure unit tests with `-race` on pure functions; integration tests on a prebuilt template repo each test copies; real process and memory-cap tests behind a build tag, nightly.

**d. The lane state machine.** Explicit states (no lane, lane open for tests, red seen, code open, green, closed by an escape, merged) and pure transitions with table tests for every state and event, written first. The edit, commit, Stop and merge hooks become thin adapters from payload to event to hook response. Workstreams 1, 2, 3 and 5 of 1a build on it.

**c. The spine, as an ordered strangler.** Run from `internal/proc` into `internal/run`, then git, then the store, then config. Each is a new package beside the old; call sites move in separate PRs; the old code is deleted once a law shows 0 uses. Partial spines already exist and are absorbed, not duplicated: `internal/proc`, `internal/argvbatch`, `internal/gitenv`, `internal/gitiso`, `internal/rootseam`, `internal/tdd/core/stateschema.go` and `internal/tdd/escape/demote.go`.

- `internal/run`: every exec, with a memory cap, kill-on-close job object (Windows) or rlimits (Linux), tree kill, timeout and the environment rules.
- `internal/git`: one git client; writes and reads through `internal/run`.
- `internal/store`: state keyed by repo and lane, a format number on every file and record, every read-modify-write under one lock, retention per collection; `events.jsonl` becomes the only log and gate.log retires. Compatibility: read-new/write-old for one release, because two binaries can run on one box at once.
- `internal/config`: one schema, three layers (built-in, user, repo), `trellis config show` and `set`.
- `tools/tddsplit` and its forwarders are deleted; the git shim shrinks to the queue lock once per-repo hooks hold the walls.

**Release replay (compatibility item 4)** is built before F.c starts, narrowed to this repo and a fanvue-sized tree, on Linux and Windows, so the spine moves are replayed before they ship.

**Exit gate for F**, each item checkable:

- The three goal measures recorded for a week on both boxes.
- First-run CI green at or above 85% over that week, reported with mutation reds counted separately.
- Inconclusive edit runs under 10%.
- 0 exec call sites outside `internal/run`, enforced by a law.
- `tools/tddsplit` deleted.
- Every hook adapter runs on recorded payload fixtures, committed.

### How work is done

These numbers are proposed; the owner confirms them.

- A lane changes at most about 600 non-generated lines in one domain; a pure move is exempt.
- At most 4 lanes are open, at most 2 of them on the spine.
- A green PR merges within 2 hours.
- The line stops when the 7-day fix-on-fix rate is above 20%: no new work until fixes are the only commits.
- A fix names the sibling paths it checked.
- A dead builder's lane is resumed before new work starts.
- No builder claim counts without an artifact: a CI run id, an event line or a committed fixture.

### Later phases

**1a, core.** Workstreams on the store and the state machine: red to green as transitions (tdd = off, warn or enforce, a four-way run result where "not tested" keeps the last real verdict); guardrails (secrets, attribution, destructive calls) with long waits and noisy output as guidance; escapes closing code edits until a test reproduces them; edit results with tests run once per batch. Plus compatibility items 3 and 6 and the per-language replay. Moves first-try quality and friction.

**1b, setup and worktrees.** The plugin and its launcher, which fetches, verifies and keeps the last 3 binaries; config verbs; session start (Check the box) and repo start with defaults and no questions; a write on main gets a worktree and the agent is told to enter it. Also: explainability (`trellis why` for a deny), an off switch (an environment kill switch every hook honours) and eject (restore the earlier hooks and config), a new repo starting in guide/warn mode, and consumer docs. The session flow `docs/trellis-flow/session_flow.py` is left unchanged now and is redrawn here. Moves friction and speed.

**3, integration.** `trellis ci` under the merge verb, hermetic and serial, the verdict stored per tree hash, local merges, `ci = auto | local | github`. Moves speed where GitHub CI is down.

**Move.** To harryberg1n/trellis, last: history pushed, protection and hosted-runner CI re-created, issues moved, a last aphrollo-tools release that switches boxes over and keeps an `aphrollo` alias for one release.

**Cut or deferred.** Each can return as a proposal with a measured question behind it.

- OpenTelemetry and the transcript join, except a local per-task token count, which the Friction measure needs.
- The lead dashboard, the cloud notes ref, per-owner stores, 90-day raw retention.
- The general merge queue, a full local CI, the background verdict comparison on each box.
- The setup conversation, the fourth config layer and `--session`.
- The in-process `.git` reader, and the package-by-domain split: only if F.b data justifies them.
- Linux cgroups: rlimits instead.
- Turning the YAML test pins into laws.
- Stays in aphrollo-tools: `refactor` and the LSP client, `sqlc`, `dev`. trellis is the gate alone.

The design detail for 1b and 3 (requirements R1 to R14, setup, configuration, repo start, the command surface, the migration steps) is the previous revision of this file, commit d3c296c0; each part is rewritten when its phase starts.

### Build against adopt

Claude Code's native LSP tool, EnterWorktree, Monitor and `/code-review` overlap parts of trellis. At each phase gate each overlap is compared on correctness, speed, token cost and upkeep, and trellis adopts the native feature and deletes its own where it covers the need. A Claude Code change to a hook payload is handled first, since it can break us; the F.a fixtures are re-recorded on it.

## Decided

- Name **trellis**, a new repo under harryberg1n, moved last. The language stays Go: a hook process starts on every edit and the Go binary starts in 8 to 9 ms, against 28 ms for Node and 17 ms for Python.
- Distribution: a Claude Code plugin pinning one binary version, built on GitHub-hosted runners; no deploy runner.
- Telemetry: none leaves the box; secrets are redacted before anything is stored.
- Escapes split into product escapes and gate disagreements; targets count product escapes.
- Lanes are Claude Code's native worktrees; a lane's state is keyed by its branch.

Risks: F is a rewrite under load, so it moves one domain per PR behind the existing tests. F swells, so it takes no new feature. The gate taxes the agent loop, so the friction measure and the demotion policy bound it.

## Open questions

Each says what it blocks.

- [ ] **F timebox: 3 weeks?** Blocks F's start date and the re-plan trigger. Proposed: 3 weeks.
- [ ] **Lane caps** (600 lines, 4 open, 2 on the spine, 2 hours to merge). Blocks the operating rules. Proposed: as written.
- [ ] **Enforce against guide.** Blocks the default of `tdd` in 1a. Proposed: alternate lanes between the two for 2 weeks, at least 30 per arm; guide stays the default unless enforce wins on escaped defects at no more than +10% friction.
- [ ] **One mutation authority** (CI blocks, commit-time mutation only reports). Blocks F.e. Proposed: yes.
- [ ] **How a host is marked production.** Blocks #1102's fix. Proposed: a user-layer key `host.production = true`.
- [ ] **When the repo moves.** The release workflow, plugin manifest and pin URLs all name the repo, so moving at the start of 1b avoids building them twice. Blocks 1b's release workflow. The owner chooses.
- [ ] **#999 (first-run setup).** Blocks 1b. Proposed: rescope it to defaults plus `trellis config set`.

## The whole flow

The session flow, with each trellis step drawn at the Claude Code hook that runs it, is generated from `docs/trellis-flow/session_flow.py`, its source. It still draws the previous revision and is redrawn in 1b.

<img alt="A session with trellis on Claude Code's hook lifecycle" src="trellis-flow/session-dark.svg">
