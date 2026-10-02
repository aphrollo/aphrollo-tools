# aphrollo-tools roadmap

**Proposed 2026-10-02, pending approval.** This revision follows a three-part architecture audit on 2026-10-02 and puts a foundation phase before the rest of phase 1a. Until the owner approves it in review, the previous revision on `main` is the plan in force. This file is the single source of truth for the roadmap; nothing else is a working copy of it.

aphrollo-tools stops adding features on a gate it cannot trust. The audit showed the gate costing more and catching less each week while the code grew faster than it could be held together. So the work now runs in this order: finish phase 0, build a foundation that makes the gate cheap, measured and correct by construction (phase F), and only then carry on with the core, the plugin and integration on top of it. Four results stay the aim: a gate whose verdicts are trusted and cheap, a stable core with a compatibility promise, a plugin that sets itself up, and an integration path that works without GitHub. Statistics come from the event log, built one question at a time.

## Where it stands

The fixes keep landing fast, but each round adds load and the gate got worse, not better. The 2026-10-02 audit:

| Measure | Value |
| --- | --- |
| PRs green in CI on the first run | 91% on 09-25 to 09-27, 56% on 09-29 to 09-30, 54% on 10-02 |
| Edit-hook runs that never tested the code, 10-02, this repo | 40% |
| Merge-gate runs that blocked, 10-02 | 66% |
| Merge-gate runs that timed out, 10-02 | 6% |
| Gate time per change before CI, 10-02 | about 28 min |
| Commits that fix something merged 7 days earlier or less | 15% overall; 39% on 09-26 to 09-30 |
| Lines added in 14 days | +158,000, 57% of the Go code |
| Issues opened against closed, 14 days | 199 opened, 190 closed |
| Issues caused by this repo's own changes, 14 days | 27% |
| Escapes recorded in 7 days, by cause | 12 local-against-CI mutation disagreements, 11 the gate's own canary, 3 real CI test reds |

What the numbers say:

- **The gate is mostly noise.** Of the 26 escapes in a week, 23 are the gate disagreeing with itself (local mutation against CI mutation) or tripping its own canary. Only 3 are product defects a test should have caught. A target on all escapes rewards the wrong work.
- **The gate is slow and often untested.** Four in ten edit runs prove nothing, two in three merge-gate runs block, and a change spends about half an hour in local gates before CI sees it. First-run CI green still fell from 91% to 54%.
- **Growth outran structure.** 158,000 new lines in two weeks, a quarter of the issues caused by our own changes, and more than a third of the commits in the worst week fixing something merged days before. Each fix lands in one call site and misses its sibling (the 2026-09-05 audit found the same pattern).
- **Shared state and process handling are scattered.** Every package writes its own files under its own lock, spawns its own processes and shells out to git on the hot path. That is where the Windows fork bomb (#997), the 26 GB mutant (#1005) and the writes into the real repository (#1043) came from.

Caveat: this box (Windows) measured the repo for one day, 10-02. The earlier days ran on the Linux box, so the hook-level numbers for 10-02 are one day of one machine; the CI numbers come from GitHub and cover all of them.

## Architecture

trellis sits between Claude Code and an integration backend. Claude Code supplies hooks, native worktrees and transcripts. The integration backend, which runs CI and takes merges, is GitHub or local CI on plain git, and both run the same `trellis ci` command. Inside trellis, the gate is the pipeline: at edit, commit and merge it calls the runners, the ratchet laws and mutation.

Under every part sits one spine (phase F): one state store keyed by repo and lane (`internal/store`), one process layer that every exec goes through (`internal/run`), one git client that reads `.git` in-process on the hot path (`internal/git`), and one config schema (`internal/config`). The lane state machine is built on the store and is trellis's kernel: a lane's states and the transitions between them are pure functions with table tests, and the hooks are thin adapters that read an event, ask the state machine and write its answer. A real red opens a lane's code edits, a green or an escape closes them, and a merge-gate pass hands the lane to integration, which merges it after a CI verdict from GitHub or from `trellis ci` run locally.

Setup installs, updates and rolls back. Config and state live in one versioned place, which the automatic migrations act on. Events from every part, git and CI land in one log, `events.jsonl`; `trellis stats` reads it as text. The learning loop closes inside trellis: a product escape becomes a new law or gate stage, built test-first in a lane of its own, and a stage that costs more than it catches is demoted (Targets).

## Compatibility policy

A `trellis update` never turns a green consuming repo red on its own. Every change that could is versioned, migrated automatically and announced.

1. **Versions.** Done in phase 0 (#1106: 1.0.0). Each release carries a semantic version next to the build stamp. A change to any verdict a consumer sees (laws, masks, gates, mutation) is at least a minor bump. Every persisted format carries its own format number (the scan-view stamp from #1050 is the pattern; phase F's store puts one on every file and record), and append-only logs carry one per record; readers skip record versions they do not know. A repo declares the oldest trellis it accepts in `trellis.toml` (`requires = ">=1.4"`, like go.mod's `go` line): an older binary refuses with the version it needs (trellis too old here) and names trellis update, which fetches it; it never misreads newer state.
2. **Consumer changelog.** Done in phase 0 (#1106: `CHANGELOG.md`, checked against the version on every bump). One file lists, per release, what a consumer will notice and what migrates by itself, in plain words. Commit history stays the developer's record.
3. **Automatic migration.** Derived data (caches, the scan cache, the managed CLAUDE.md block, hook shims) is never migrated: whichever binary runs recomputes it. Committed data (baselines, `trellis.toml`) migrates itself on first run, is checked by recomputation, and lands as its own commit, so `git revert` undoes it; a format change to committed data ships in two releases, the first reading the new format and still writing the old one, the next writing the new one. Local state (the store, the event log) migrates forward in one release: the store copies it to `.bak.<version>` first, and an older binary that meets a newer format number refuses it by name instead of guessing. There is never a required manual step.
4. **Replay before release.** CI replays each release against this repo, pinned public repos for every supported language and a synthetic tree generated to fanvue's size and file mix, on Linux and on Windows; it ships only with zero new hits on unchanged trees, which is the #1023 test made general. The replay also runs the previous release's binary on the committed data the new one migrated, and it must read it cleanly. The per-box background comparison of the old proposal is cut: the release replay is the one check. No client code or telemetry leaves the box it runs on.
5. **Safety invariants, enforced in one place.** A test process never touches a real repo or the global config (#1043, fixed by PR #1044). No child runs without a memory cap (#1005). No hook writes a shim pointing at a temporary binary (#1033). Every runner carries a canary that refuses its own result on a violation. Local CI never installs anything globally and refuses to run on a host marked production (#1102). From phase F these hold because every exec goes through `internal/run`, which sets the cap, the kill-on-close job object or cgroup, the timeout and the environment; the call-site text guard that hunts for a bare exec retires with it.
6. **Rollback.** `trellis update --to <version>` reinstalls any earlier release and records it as a pin override in the user layer, which SessionStart honours until a plain trellis update clears it; the last 3 binaries stay on the box. Committed data stays readable one release back because of its two-release rule (item 3); local state is restored from the `.bak.<version>` copy the migration made. Further back, the repo's declared minimum version makes the older binary refuse with the version it needs; it never misreads newer state.

## Roadmap

The phases run in order; each starts only when the previous gate is met, and nothing new enters a phase that is already running. Every phase is built in aphrollo/aphrollo-tools. The move to the new repo keeps the owner's decision (after the plugin work is proven); whether it should come at the start of the plugin phase instead is an open question below.

1. **0 · Now**: unblock fanvue, a minimal event log, the open safety escapes. Mostly done. Gate: fanvue merges through local CI; events are logged.
2. **F · Foundation**: spikes, measurement, the spine, the lane state machine, one mutation authority, test tiers. Gate: recorded fixtures for every hook; budgets met on Windows; first-CI-green at or above 85% over a week; inconclusive edit runs under 10%.
3. **1a · Core**: red to green, guardrails, escapes and edit results on the store and the state machine; compatibility items 3, 4 and 6. Gate: a release replays with 0 new hits.
4. **1b · Setup and worktrees**: launcher, config, session and repo start, lanes on a write to main. Gate: setup on a new box is one step.
5. **3 · Integration**: `trellis ci` under the merge verb, local merges, Artifact review. Gate: a merge completes without GitHub.
6. **Stats from events** (was phases 2 and 4): no phase of its own. Each metric is built when a real question needs it, from `events.jsonl`, and `trellis stats` prints it as text.
7. **Move**: to trellis, a new repo under harryberg1n, after the plugin work is proven (owner's decision; see Open questions).

No dates yet: each phase is sized when it starts, from what the event log then measures.

### Phase 0 · Now: what landed

| PR | Closes | What it did |
| --- | --- | --- |
| #1087 | | The roadmap moved the trellis migration after phase 4 |
| #1089 | #1085 | Undercover accepts a cloud session's assigned branch; the merge verb writes its own merge subject |
| #1090 | #1072, #1083 | The test-map canary stops tripping on lane work; prune never deletes through a junction; a remote notes ref that is ahead is merged |
| #1091, #1095 | | The event log: versioned JSONL beside gate.log, then countable: distinct gate-result kinds, settled per-commit CI events, real repo paths, lane-aware escapes |
| #1092 | #1064, #1081 | The repo's own GitHub workflow runs as local CI when hosted CI cannot start |
| #1094 | #1074, #1076 | A lane owner's commits are told from a leak by a record the post-commit hook writes |
| #1096 | #1080 | The real-golangci-lint test stops losing to other lints running on the same box |
| #1093 | #996 in part (reopened) | The pytest fail-first line is held to the argv budget; argument lists grown in a loop are guarded |
| #1097, #1099, #1110, #1112 | | Windows: long paths in the isolated test config, the Windows test fixes, the env PATH on Windows, the fail-first checkout kept under git's path limit |
| #987 | #866 | The Windows smoke job on a GitHub-hosted runner (planned for 1a) |
| #1100 | #998 | Install converges the user PATH: the shim dir and the binary dir once each, ahead of Git; doctor audits it |
| #1105 | #1079 | The related-runner guard runs at commit: the call-site stage judges both argvbatch tables |
| #1107 | | A merge made outside `workspace merge` is recorded when local trunk takes it in |
| #1111 | | Stop and SubagentStop block a turn once on an unseen red; TaskCompleted keeps a task open while its tests are red (workstream 4, planned for 1a) |
| #1113 | | The merge gate cuts a go test list into runs that each fit their budget |
| #1106 | | Versions: 1.0.0 beside the build stamp, a `requires` floor, a changelog per version, a CI check on the bump (compatibility items 1 and 2, planned for 1a) |

The gate is still pending: fanvue has not yet merged through local CI, and #1102 and #1103 are in progress. Local CI ran a workflow's `pip install` into a production host's global Python and downgraded a package there (#1102), and it ran all workflow jobs in parallel at normal priority, so a build that normally takes 5 minutes hit the 30-minute deadline (#1103). Both are answered by the shrunk local CI below: serial, low priority, never a global install, refused on a host marked production.

### Phase F · Foundation

F comes before the rest of 1a because every 1a workstream adds state, processes and hook logic, and today each of those is built a different way in each package. F builds the one way first, measures what the gate costs, and moves the existing code onto it. It adds no consumer-visible feature. Its parts run in this order.

**a. Spikes (about 1 day).** Facts first, so nothing below is built on a guess.

- Record the real hook payloads for every event trellis uses, on Linux and on Windows, in four situations: the main session, a subagent, an isolated subagent (`isolation: "worktree"`), and after EnterWorktree. Commit them as test fixtures. They are never hand-written; a fixture that is not a recording is refused in review.
- Time a no-op hook through the launcher on Windows, p50 and p95, which sets what a hook can afford.
- Measure how often Claude enters a lane after a deny that names its path, with and without the worktree line in the managed CLAUDE.md block.
- Check whether `CLAUDE_ENV_FILE` reaches the PowerShell tool, since the queue shims depend on it.

Recorded on 2026-10-02, from a real `claude -p` session on Windows that logged every hook's stdin:

- SubagentStop's cwd is the isolated subagent's own worktree (.claude/worktrees/agent-\<id>), not the parent session's. It also carries `agent_transcript_path` and `last_assistant_message`.
- `stop_hook_active` exists on both Stop and SubagentStop, and is false on the first stop.
- PreToolUse inside a subagent carries `agent_id`, `agent_type`, and a cwd that is the subagent's worktree.
- After EnterWorktree the cwd follows into .claude/worktrees/\<name>, and it moves again on ExitWorktree.
- PostToolBatch fires: 8 times for 10 tool calls in the recorded session.

So #1111 stands as built: its lane scoping by cwd is right for an isolated subagent, and its once-guard reads a real field. Its tests still swap their hand-written payloads for these recordings.

Not yet recorded: TaskCompleted, because TaskCreate is not available in a `-p` session. Whether its decision is ignored when Claude completes a task through TaskUpdate stays unverified until an interactive session checks it. Still to do in F.a: the launcher latency on Windows, the follow rate after a deny, `CLAUDE_ENV_FILE` for the PowerShell tool, and the Linux recordings.

**b. Measure before enforcing more.**

- Per-stage cost and catch counts from `events.jsonl`: for every stage of the edit, commit and merge gates, how long it takes and how often it is the stage that caught a real defect.
- Budgets, on Windows: a commit's gate p95 under 60 s, the merge gate under 5 min.
- Escapes split into **product escapes** (a test red in CI, or a defect found after the merge, on a head the local gates passed) and **gate disagreements** (local mutation against CI mutation, the gate's own canary, a timeout read as a verdict). Targets count product escapes only; each disagreement is a defect of the gate, fixed or answered by demotion.
- A demotion policy: a stage with no catch in 4 weeks, or with more disagreements than catches, moves to warn or to CI only. A demotion is an event and a changelog line, and a stage comes back only with a catch on record.
- A cap on the agent brief's length (the managed CLAUDE.md block, the injected gate rules, the SubagentStart brief), so rules cannot pile up in the prompt faster than they are removed.

**c. The spine.**

- `internal/store`: one state store keyed by repo and lane, with typed collections; a format number on every file and every record; every read-modify-write under one lock; retention per collection. `events.jsonl` becomes the only log: the readers of gate.log move to it and gate.log retires.
- `internal/run`: one process layer. Every child gets a memory cap, a kill-on-close job object on Windows or a cgroup on Linux, a tree kill, a timeout and the environment rules (isolated git config, no global writes). Every exec in trellis goes through it, which retires the call-site text guard that today looks for a bare exec.
- `internal/git`: one git client. On the hot path it reads `.git` in-process (HEAD, refs, the index stamp), so a hook spawns no git process; writes and rare reads still run git through `internal/run`.
- `internal/config`: one schema and the layers of workstream 7.
- Retire the L0 to L8 forwarders in `tools/tddsplit` and cut the packages by domain instead: the hook pipeline, lane state, runners, laws, mutation, integration, setup. Each domain has a narrow API, and the seams tests replace (clock, process runner, git, store) are injected in one struct instead of one package variable each.
- Shrink the git PATH shim to the queue lock alone. The walls (the primary checkout, branch moves, commits on main) live at PreToolUse and in each repo's own git hooks, not in a shim that intercepts every git call.

**d. The lane state machine (R13).** Built on the store: explicit states (no lane, lane open for tests, red seen, code open, green, closed by an escape, merged) and pure transitions with table tests for every state and event. The edit, commit, Stop and merge hooks become thin adapters that turn a payload into an event and the state machine's answer into a hook response. This is trellis's kernel; workstreams 1, 2, 3 and 5 build on it.

**e. One mutation authority.** CI's mutation verdict is the one that blocks. Commit-time mutation becomes advisory: it prints its survivors and never refuses. That ends the local-against-CI disagreements by design (12 of 26 escapes in a week) and closes #1078. The mutation coding rules leave the agent brief: the agent learns of a survivor from the CI verdict, not from a rule in its prompt.

**f. Test tiers.**

- Pure unit tests, run with `-race`, on the state machine, the store and every pure function.
- Integration tests on a prebuilt template repo that each test copies, instead of building a repo with git commands in every test.
- Real process and memory-cap tests behind a build tag, run nightly, not on every edit.
- The repo-policy pins that today live as YAML assertions in tests become ratchet laws, judged like every other law.

**Gate for F:** recorded fixtures for every hook trellis uses; the budgets met on Windows; first-run CI green at or above 85% over a week; inconclusive edit runs under 10%.

### The plan: from aphrollo to the session flow

The target is the session flow in the repo's `docs/trellis-flow` (on `main` since #1082): every trellis step drawn at the Claude Code hook that runs it. Most of it exists in aphrollo today. The table says what each phase builds to get there and what it starts from.

| # | Workstream | What gets built | Starts from today | Phase |
| --- | --- | --- | --- | --- |
| 1 | Red to green, finished | R13's test-edit rule as transitions of the lane state machine; tdd = off, warn (the next step as guidance) or enforce; a four-way run result where "not tested" keeps the last real verdict | the edit ledger, fail-first, deny laws at edit time | 1a, on F.d |
| 2 | Guardrails and guidance | R1's guardrails: secrets, attribution, destructive or outward-facing calls, enforce-mode red to green, deny laws; long waits and noisy output become guidance instead of blocks | guardrail pretooluse, the edit smells | 1a, on F.c |
| 3 | Escapes and feedback | one definition: a red after a local green, split into product escapes and gate disagreements; a product escape closes code edits until a test reproduces it; `trellis feedback` records a wrong deny | gate escape record, auto-escape, gate feedback | 1a, on F.b and F.d |
| 4 | Stop, task and subagent checks | done in #1111, confirmed by the 2026-10-02 recordings (lane from the subagent's cwd, `stop_hook_active` is real); its fixtures become recordings, and TaskCompleted waits on an interactive check | #1111 | 0, fixtures in F.a |
| 5 | Edit results | PostToolUse formats the file and names it; the tests run once per batch at PostToolBatch; unproven packages are marked in the store | the post-edit hooks, BUILDING deferred | 1a, on F.c |
| 6 | Launcher and binary | the plugin pin and a pin override in the user layer; fetch, verify and keep the last 3; `trellis update` repairs a missing binary; `requires` refuses instead of fetching | aphrollo update, the release workflow | 1b |
| 7 | Configuration | three layers with a checked schema (built-in, user, repo); the setup record and a repo's decline in the user layer, keyed by repo; TRELLIS\_CONFIG; `trellis config show` and `set`; /gate retired | aphrollo.toml, gate allow and revoke | F.c builds the schema, 1b the verbs |
| 8 | Session start | Check the box, which names a git identity the commit gate would refuse and an old aphrollo install; the gate rules injected on every start; clear and compact skip the checks; every hook silent where trellis is off (R5) | gate sessionstart, doctor | 1b |
| 9 | Repo start | defaults and `trellis config set` instead of setup questions; an old aphrollo install removed by the one command Check the box names, with a backup; git init per the user layer, `requires`, set up the repo (merge.ff=false, branch protection offered with ci = github), a foreign git hook named, the rules injected, what finished told | aphrollo install, gate init, the features table | 1b |
| 10 | Lanes | a write on main gets a worktree (deny, then EnterWorktree); post-merge removes merged lanes no session is in; no WorktreeCreate hook; the managed CLAUDE.md block dropped once the F.a measurement shows Claude follows the deny | the primary-checkout wall, workspace prune | 1b |
| 11 | CI under the merge verb | ci = local, github or auto; local CI is the hermetic `trellis ci` checks only, serial, low priority, never a global install, refused on a host marked production; hosted-run states (pending up to 90 min, no ready PR, ci unavailable); the verdict recorded per lane head; pre-merge-commit refuses a head without one; a GitHub-side merge recorded like an escape; the open-a-PR verb | #1092, workspace merge and pr, ci why | 0 started, 3 finishes |
| 12 | Stats from events | each metric built when a question needs it, read from `events.jsonl` and printed by `trellis stats`; wrong denies and enforce against warn measured | gate stats, the event log from #1091 and #1095 | as needed, from F.b |
| 13 | The flow stays the spec | each step is built with its test; a change of behaviour edits `docs/trellis-flow/session_flow.py` in the same PR, and the workbench's layout check stays clean | docs/trellis-flow | every phase |

Phase 1a now carries four workstreams (1, 2, 3, 5) plus compatibility items 3, 4 and 6, all on the store and the state machine. If this proposal is approved, the session flow is redrawn in 1b where it changes: the setup conversation, the local config layer and `--session` leave it.

### Cut or deferred, and why

The audit found more planned than the gate can carry. These leave the plan; each can come back as a proposal once a measured question needs it.

- **OpenTelemetry and the transcript join.** The questions the retros ask are answered from gate, git and CI events; joining Claude Code's telemetry and transcripts is a second data pipeline with no question waiting on it.
- **Per-owner stores.** One store keyed by repo already keeps client data apart; a store per owner multiplies paths and migrations for no measured need.
- **90-day raw retention.** Retention is set per collection in the store, by size and age, from what the stats actually read.
- **The cloud notes ref** (refs/notes/trellis-events). It existed to carry the OpenTelemetry-scale event store off a short-lived container; with that cut, a cloud session's events stay in its own log.
- **The Artifact dashboard.** `gate stats` text answers today's questions. A page is built when a question needs more than text.
- **The setup conversation.** Built-in defaults plus `trellis config set` cover what the four questions asked, and a session nobody answers already needed the defaults path. A conversation is a second path to test on every box.
- **The fourth config layer and `--session`.** A machine-only override lives in the user layer, keyed by repo; a session-only override has no measured user.
- **The background verdict comparison on each box.** The release replay in CI is the check; a second comparison on every box doubles every update for the same answer.
- **The two-release write rule for local state.** Local state migrates forward in one release with a `.bak.<version>` copy; the two-release rule stays for committed data, where another checkout may still run the older binary.
- **A general local merge queue.** Merges are serialized by the queue lock and judged by `trellis ci`; a queue with its own scheduling has no measured need.
- **A full local CI.** Local CI shrinks to the hermetic `trellis ci` checks: serial, low priority, never a global install, refused on a host marked production. Running a consumer's whole workflow YAML on the box is what broke a production host (#1102, #1103).
- **refactor and the LSP client, sqlc and dev leave trellis.** They stay in aphrollo-tools: they are not part of the gate, they carry a language server and company infrastructure, and trellis stays the gate alone.
- **Phases 2 and 4 merge into stats from events**, built only as each metric answers a real question (workstream 12).

## Targets, risks and non-goals

The gates say when a phase is done; these targets say whether the work between them is getting better. They are measured from `events.jsonl` and from GitHub's CI history.

| Target | Now (2026-10-02 audit) | Goal |
| --- | --- | --- |
| PRs green in CI on the first run | 54% on 10-02 | at or above 85% over a week (gate of F) |
| Edit runs that never tested the code | 40% | under 10% (gate of F) |
| Gate time per change before CI | about 28 min | commit p95 under 60 s and merge gate under 5 min, on Windows |
| Product escapes per merged PR | 3 real CI test reds in 7 days | falling every week |
| Gate disagreements | 23 in 7 days (12 mutation, 11 canary) | each one fixed or answered by demotion; 0 mutation disagreements after F.e |
| Commits that fix a merge 7 days old or less | 15% (39% on 09-26 to 09-30) | under 10% |
| Issues caused by this repo's own changes | 27% over 14 days | falling every week; none caused by a release (the replay catches it first) |
| Wrong denies (a code edit denied that the gate should have allowed) | not measured | near 0; each one is recorded through `trellis feedback` |
| tdd = enforce against warn, same tasks | not measured | enforce has fewer product escapes without more tokens or time per task; otherwise warn becomes the default |

Risks:

- **F is a rewrite under load.** The spine replaces code that consumers run every day. It moves one domain at a time behind the existing tests, each move its own PR, and the event log shows whether a move made a stage slower or noisier.
- **F swells.** F adds no feature. Anything new goes into a later phase or the queue.
- **Plugin migration doubles hooks.** A box with an old `aphrollo install` and the new plugin runs every hook twice. Only two boxes carry the old install (the Linux box and the Windows box), so Check the box finds the old install, the global core.hooksPath included, and names the one command that removes it, with a backup.
- **Claude Code overlaps us, or breaks us.** When the Claude Code version on a box changes, SessionStart matches the new changelog entries against a list of our features (hooks, worktrees, commit skills, review) and names each hit in one line. Every hit gets a side-by-side comparison: correctness, speed, token cost, worktree awareness, safety and upkeep. The result is one of three: adopt theirs and delete ours, keep ours where it is better, or combine them. A hit that changes a hook payload is handled first, since it can break us, and the recorded fixtures from F.a are re-recorded on it.
- **The gate taxes the agentic loop.** Every deny costs Claude a round trip and every test run costs time. The budgets of F.b, the demotion policy and the cap on the brief bound that cost, and the enforce-against-warn comparison decides whether enforce stays the default.

Non-goals: no hosted service, no IDE extension, no reimplementing a Claude Code feature that exists natively, no general merge queue, no tracking beyond the user's own box: no telemetry leaves the box it runs on (opt-in summaries, never code, may come later).

This doc is reviewed at every phase gate and after any week with more than 5 product escapes.

## Requirements

The tool extends Claude Code instead of fighting it: every requirement below builds on a native feature (hooks, worktrees, skills, plugins) and adds only what Claude Code lacks.

| # | Requirement | Spec | Phase |
| --- | --- | --- | --- |
| R1 | Give Claude wings, don't fight it | Refuse only real guardrails (secrets, attribution, destructive or outward-facing actions, enforce-mode red to green, deny laws). Everything else is guidance in context. Never rewrite a tool's input behind Claude's back, so Claude's model of what it did stays true. | all |
| R2 | Fully automated setup | Distributed as a Claude Code plugin: enabling it installs the hooks, skills and agents. The plugin pins one binary version; SessionStart, Setup or trellis update fetches it from a GitHub Release when it is missing, verifies its sha256 and keeps the last 3 for rollback. No manual `install` or `gate init` step remains. | 1b |
| R3 | First run asks nothing | Proposed change: the setup conversation is cut. A first run applies the built-in defaults; any setting changes later through `trellis config set`, typed or in conversation (Configuration). Everything about the code is detected per repo at Repo start, never asked and never stored. | 1b |
| R4 | Repos and worktrees happen by themselves | Lanes are git worktrees under .claude/worktrees/, and a lane is its branch, whose state the store makes the first time a hook needs it; trellis hooks neither WorktreeCreate nor WorktreeRemove. By default (isolation = true) a write on main (code, a test, a doc or config; a gitignored file passes) makes one without asking: trellis adds the worktree from main, the deny names its path, and Claude passes that path to EnterWorktree and repeats the edit. A subagent with isolation: "worktree" gets its worktree from Claude Code, and SubagentStart briefs it. isolation = false keeps the work on main. A folder with no git gets git init or nothing, as the user layer says. | 1b |
| R5 | Zero cost where not opted in | Plugin hooks fire in every repo. In a repo that is not opted in, unless trellis runs everywhere, every hook answers in under 50 ms and fails open. SessionStart is the one exception: it still gets the binary and checks the box within its 200 ms, since a later CwdChanged or DirectoryAdded may enter an opted-in repo, but it injects nothing. The F.a timing of a no-op hook on Windows confirms or revises these numbers. | 1b |
| R6 | Analytics of the work | Proposed change: one log, `events.jsonl`, in the store: gate events with per-stage cost, git and CI events, resource use. Local only, secrets redacted at write time, retention per collection. OpenTelemetry, transcripts, per-owner stores and 90-day raw retention are cut. | 0 minimal, F full |
| R7 | Architecture for growth | Domains with one owner each and narrow APIs, one state store, one process layer, one git client, one config schema, a written compatibility policy, and a roadmap that is kept current. | F |
| R8 | Not tied to GitHub, built on Claude's tools | Lanes run on Claude's native worktrees. Review and diffs are Artifact pages with aggregated data only. Our own code covers what nothing else does: `trellis ci`, the one CI entry point the GitHub workflow wraps and local CI runs on plain git, which also serves repos off GitHub and repos whose hosted CI is down (#1064). No general merge queue. | 3 |
| R9 | A lead's overview | Proposed change: per project, lane and week, the numbers a question asks for (loops, pushes, refusals, product escapes, gate disagreements, time open to merge), printed by `trellis stats` from the event log. A page comes only when text does not suffice. | as needed |
| R10 | Name and ownership settled once | A neutral name (no vendor mark), the repo owner chosen by ownership intent, and both changed in one migration. | before 1 |
| R11 | Windows is a first-class platform | A smoke job on GitHub-hosted windows-latest runs on every PR (#866, PR #987, done), the budgets are measured on Windows, and the pre-release replay runs on Windows too. Half the serious bugs of 09-29 and 09-30 were Windows-only. | 0, F |
| R12 | Minimal resources, only when needed | Nothing runs persistently that is not in use: no background language servers (all LSP plugins are off; a gopls had held 548 MB for 26 h), heavy work starts on demand through `internal/run`, and the resource governor holds it back when the box lacks headroom. | all |
| R13 | Red to green is mechanical | The lane state machine (F.d) holds it. Code edits open only on a new test's real red (missing implementation or failed assertion) and close on its green; editing the test before green needs a new red. Refactors of covered code pass while their tests stay green; mutants on the changed lines are reported at commit and judged by CI (F.e). A Bash command is checked like an edit: PreToolUse parses its write targets, so a Bash write to code meets the same lane and red-to-green checks. Only a write no parser can see (a script that writes files, a generated file, an edit outside Claude) is found in the tree after the batch and marks its package unproven: Claude gets one line asking for the failing test, and the commit falls back to the re-run at HEAD. The commit gate reads the ledger's red-green pair and re-runs at HEAD only without one. A later product escape re-closes the lane until a test reproduces it. Red-to-green numbers go to the event log. | F, 1a, 3 |
| R14 | The gate earns its cost | Proposed: every stage has a measured cost and catch count in the event log, a budget, and a place in the demotion policy (F.b); targets count product escapes, and a gate disagreement is a gate defect. | F |

## Setup

Enabling the plugin is the whole install: the first start applies the built-in defaults, and every later session, subagent and repo uses them and whatever `trellis config set` has changed since.

Setup uses only hooks Claude Code documents today, and their payloads are pinned by the fixtures F.a records. SessionStart, matched on its `source`, runs the checks on `startup`, `resume` and `fork`, and when /reload-plugins loads trellis mid-session; it skips them on `compact` and `clear`. Its `additionalContext` hands Claude the gate rules on every start, and `CLAUDE_ENV_FILE` puts the queue shim on PATH for every later command (F.a checks that it reaches the PowerShell tool); the shim calls the plugin's launcher, never a binary path, so it stays current whichever binary runs, and from F.c it holds only the queue lock. The `Setup` event (`claude --init-only`, `-p --init`, `--maintenance`) runs the same steps with the defaults or TRELLIS\_CONFIG for CI and cloud boxes. `CwdChanged` and `DirectoryAdded` send a newly entered repo to Repo start. `SessionEnd` shares a 1.5 s budget, so it does nothing that can block. In an interactive session SessionStart runs in the background: you can type at once, but Claude's first response waits for it, so with nothing to do its checks finish in under 200 ms and only a fetch touches the network. A /clear while it still runs discards what it returns. Deliberately not used: PreToolUse `updatedInput` to redirect edits silently (R1).

**Distribution and updates.** There is no deploy runner and no build on the box. A merge to `main` that bumps the version runs a release workflow on GitHub-hosted runners (free on a public repo): it cross-compiles linux, darwin and windows for amd64 and arm64 and publishes a GitHub Release with sha256 sums. The plugin carries the hooks, skills and agents plus the one binary version it pins, so plugin and binary move together. Every hook calls a small launcher script in the plugin (sh, and PowerShell on Windows) that hands over to the pinned binary; only at SessionStart, Setup and trellis update does it fetch and verify one that is missing. A newer pin is used from the next start; the release replay in CI is what vetted it. Claude Code auto-updates plugins only from official marketplaces by default; trellis keeps that default, and turning auto-update on for ours is one documented setting. Claude Code has no plugin rollback, so trellis keeps the last 3 binaries in `CLAUDE_PLUGIN_DATA` and `trellis update --to <version>` switches back (Compatibility policy, item 6).

### Getting the binary

The launcher script in the plugin runs before every hook and always ends on a usable binary, or says plainly that there is none.

A download whose sha256 does not match is deleted and reported as a security error; it is never used. Without the pinned binary, the launcher runs the newest of the last 3 kept and warns once. Only with no binary on the box at all is trellis not ready for the session, which is not the same as "trellis off here", where a repo declined trellis. `systemMessage` tells you and `additionalContext` tells Claude (`trellis not ready: commits read as ungated; the merge gate checks them`). Nothing blocks the work: no commit gets the git note that marks it gated, so each reads as ungated, the same unproven path R13 uses for a Bash write. Nothing retries in the background: the notice names what failed and the fix, Claude fixes it first, and trellis update then runs Getting the binary again; once it succeeds, trellis is back mid-session. A fetch that runs past its time limit counts as a failed download, and a kept binary is written to a temp file and renamed into place under a lock, so two sessions starting together never share a half-written file. A clear or compact while trellis is not ready takes the full path again, so it never re-injects rules that are not running. CI and the merge gate check them anyway before anything reaches `main`.

## Configuration

Settings live in three layers, the later one winning, the same structure as Claude Code's own settings and git's config. One schema in `internal/config` (F.c) defines every key; every hook reads the result.

| Layer | Where | Set by | Example |
| --- | --- | --- | --- |
| Built-in defaults | in the binary | each release | `ci = "auto"` |
| User | `CLAUDE_PLUGIN_DATA/config.toml`, with per-repo sections keyed by repo | `trellis config set` | trellis everywhere, a rollback's pin override, this machine merges fanvue through local CI, this host is production |
| Repo | `trellis.toml`, committed | `trellis config set --repo`, through the commit gate | `undercover`, mutation policy, laws, `requires` |

A flag on one command (`--ci local`) beats every layer for that command only.

- **One way in, for people and for Claude.** `trellis config show` prints each effective value and the layer it came from, like `git config --show-origin`; `trellis config set <key> <value> [--user|--repo]` changes one. A skill wraps both, so "use local CI for fanvue" in conversation becomes that call. Nobody edits the files by hand. The local layer (`trellis.local.toml`) and `--session` of the previous revision are cut: a machine-only override is a user-layer key in that repo's section.
- **A schema, checked.** Every key has a type, its allowed values and the layers it may live in (`undercover` is repo-only: it is a policy for everyone working in the repo). An unknown key or a wrong value is refused with a suggestion, the same rule as an unknown flag.
- **Changes apply at once.** Every hook is a fresh process that reads the layers, so a user change applies on the next hook, with no restart; a repo change is code and applies once committed.
- **Detected values are never stored.** Languages, test runners and the remote are recomputed each time, the derived-data rule of the compatibility policy, so the config holds only overrides and never goes stale.
- **Every change is an event.** Who changed which key, in which layer, from what to what, lands in `events.jsonl`, so `trellis stats` can answer why fanvue merged without GitHub CI.
- **The defaults answer what the setup questions asked**: ask per repo (a repo is in once it has a `trellis.toml` or the user layer turns trellis on everywhere), ask before git init, ci = "auto"; plugin auto-update keeps Claude Code's own default. A session nobody answers (`-p`, a routine, CI) takes the same defaults, or a file handed in through `TRELLIS_CONFIG`.
- **CI is one setting:** `ci = "auto" | "local" | "github"`. `auto` uses GitHub when the remote is on GitHub and its jobs start, and falls back to local CI when they never start (the billing-lock case #1064 detects). Local CI is `trellis ci`, the hermetic checks only: serial, at low priority, with no global install, and refused on a host the user layer marks production. Every merge prints which CI judged it and why. Merges are local (merge.ff=false): CI runs under the merge verb, before the merge, and the merge gate, which refuses a head without a CI verdict, runs in the repo's own pre-merge-commit hook (a conflicted merge reaches it through pre-commit when it is committed), and post-merge records every merge, one made by hand in a terminal included (#1107), so the next hook in the session tells Claude that the lane is closed.
- **Enforcement and isolation are settings, on by default.** `tdd = "enforce" | "warn" | "off"`: enforce denies a code edit before a real red (PreToolUse) and blocks the end of a turn once when a deferred run finished red after Claude's last hook, so Claude never stops on a red it has not seen (Stop and SubagentStop, never twice in a row, as stop_hook_active says; a red Claude has seen may end a turn), and keeps a task open while its tests are red (TaskCompleted, exit 2; whether Claude Code honours it for a task completed through TaskUpdate is still to be checked in an interactive session); warn gives the same next step as guidance instead of a deny, and off skips red to green but keeps guardrails and laws. The tests run once per batch of tool calls (PostToolBatch), not after every edit, and every deny names the next step: the failing test to write, in which file. `isolation = true | false`: true, the default, makes a worktree when a write lands on main and has Claude enter it (EnterWorktree) and repeat the edit, so every code change gets its own lane; false keeps the work on main, with no lane and no deny. Repo start sets up the repo and its git gate either way.
- **Subagents inherit.** They need no setup; SubagentStart only hands them the gate brief, which stays under the length cap of F.b.

### First start

The first start asks nothing. It applies the built-in defaults, runs Check the box, and names anything that needs a decision (an old aphrollo install, a git identity the commit gate would refuse) with the one command that settles it. The setup conversation of the previous revision is cut (Cut or deferred).

## Repo start

trellis works on git repos only: lanes are Claude's worktrees, and the gate, the edit ledger and merges are all git. The repo is the unit; `trellis.toml` lives in it, versioned with the code. Repo start runs once per repo, the first time Claude takes a task there, and applies the user layer: whether trellis runs everywhere or only in repos that opted in, and whether a folder without git gets `git init` or nothing. What the code is gets detected, never asked: languages, test runners and law presets. An empty repo starts with no languages; each is detected once a file in it exists. The CI backend follows the ci setting (Configuration); its default, auto, reads the repo's remote at each merge: GitHub when there is a GitHub remote whose jobs start, the local backend otherwise, so adding a remote later needs no step. A new remote is created only on request. Repo start also installs the commit, push and merge gates as this repo's own git hooks, so no global core.hooksPath reaches a repo that never opted in (R5); it sets merge.ff=false and, with ci = github, offers branch protection. A repo whose requires the binary does not meet stops at trellis too old here until trellis update fetches that version. A repo that is not opted in ends at trellis off here, where every hook stays silent (R5).

## A change, end to end

Repo start ends at Repo ready; from then on every task starts here, in a lane made by its first write on main. Red to green is mechanical: the lane state machine refuses a code edit until the lane holds a test that has gone red for the right reason (a missing implementation or a failed assertion, never a broken setup), and the implementation then runs until that test is green. The commit gate proves the order again, and CI and the merge gate judge every PR; CI's mutation verdict is the one that blocks. A product escape, a test red in CI or at the merge gate on a head the local gates passed, starts over with a failing test that shows it; a defect found after the merge is fixed the same way, test first. A product escape is fixed as a new law or gate stage in a lane of its own, which tightens an earlier gate, so the next change meets it sooner; a gate disagreement is fixed in the gate, or the stage is demoted. Every verdict, escape and merge lands in `events.jsonl`, which `trellis stats` reads. The steps are named here and detailed per phase once the whole picture stands.

### Red to green, mechanically

The lane state machine keeps code edits in a package closed until a new test goes red for a real reason: a missing implementation or a failed assertion, never a broken setup and never another test's red. The edit ledger already records each edit's class and its run's failing tests, so opening needs no new command. Code edits stay open until that test is green, and editing the test or its helpers before then needs a new red, so a green always comes from the code. A refactor of code that tests already cover passes while they stay green; the mutants on the changed lines are reported at commit and judged by CI, so new behaviour hidden in tested code is refused before the merge. A Bash write is parsed at PreToolUse and checked like any edit. Only a write no parser can see, or an edit outside Claude, cannot be refused before it lands; it marks the package unproven, Claude is asked for the failing test, and the commit falls back to the re-run at HEAD. The commit gate reads the red-green pair from the ledger and re-runs the test at HEAD only when there is none. A product escape closes the lane's code edits again until a test reproduces it (R13).

## Command surface and what moves where

Hooks drive everything an event can decide; verbs remain only for deliberate actions, about 6 to 8 of them instead of 20 top-level verbs and 69 subcommands today. trellis takes the gate; everything else stays with aphrollo-tools.

| Job | Who does it | How |
| --- | --- | --- |
| Lanes and isolation | Claude | Native worktrees under .claude/worktrees/: trellis makes one when a write lands on main and Claude passes its path to EnterWorktree; an isolated subagent gets its own from Claude Code and SubagentStart briefs it; a lane's state is made in the store on first use, keyed by its branch |
| Review and diffs | Claude | Artifact pages; client diffs shown while viewed, never stored |
| Issues and tracking | Claude or GitHub | GitHub issues where the repo is on GitHub |
| Stats | trellis | `trellis stats` prints the numbers a question asks for from the event log |
| Local CI | trellis | `trellis ci` on plain git: the hermetic checks, serial and at low priority; the one part nothing else provides |

The verbs fall into three tiers:

1. **Hook entry points** (`gate posttooluse`, `gate precommit`, …): called by the hooks, hidden from help.
2. **Event-driven, no verb needed:** `install` and `gate init` become enabling the plugin; `workspace create/claim/unclaim` become the lane a write on main makes (EnterWorktree); `workspace prune` and `gate gc` become post-merge cleanup and the background sweep at SessionStart; `ratchet check` and `docs check` run in the commit gate; escapes are recorded at every merge. The verbs may stay as a manual fallback that no workflow needs.
3. **Deliberate actions, a small set with a skill each:** open a PR, merge, explain a red CI run, check the tree, update (which also repairs a failed start, and rolls back with --to), change a setting, record a wrong deny, print stats.

| Verb | Where it goes | Why |
| --- | --- | --- |
| `refactor` (rename-symbol, find, outline, show) and the LSP client | stay in aphrollo-tools | Not part of the gate. find, outline and show had 0 runs and Read and Grep cover them; rename-symbol starts gopls only for the rename. trellis carries no language-server code |
| `dev` (systemd control of aphrollo services) | stays in aphrollo-tools | Company infrastructure, not generic |
| `sqlc` | stays in aphrollo-tools | Specific to aphrollo-api |
| `guardrail pretooluse` | folded into trellis's own PreToolUse hook | One hook entry point, not two |
| `trellis update [--to <version>]` | stays in trellis, a deliberate-action verb | One verb for fetching, updating and rolling back: it makes the binary match the pin, after a failed start once the cause is fixed or after the plugin moved the pin, and with --to it writes a pin override first; a binary already in place is a \[skip\]. Nothing retries in the background, so trellis not ready names this verb as the fix |
| `trellis config show, set` | new, a deliberate-action verb | Shows each effective value and its layer, and changes one key in one layer; the config skill calls it in conversation, the next hook reads the change |
| `trellis stats` | replaces gate stats | Prints the numbers a question asks for from `events.jsonl`, as text |
| trellis feedback \<reason> | new, a deliberate-action verb | Records a wrong deny or a gate defect as a gate disagreement and opens an issue on trellis's tracker; the wrong-denies target counts these. Replaces gate feedback |
| /gate (the /tdd command) | retired | trellis config set covers it: gate allow primary becomes isolation = false in that repo's section of the user layer |

## Decisions

The owner is decided: the tool becomes a new repo under harryberg1n, renamed in the same move. The name is **trellis**.

| Decision | Status | Choice | Why |
| --- | --- | --- | --- |
| Repo owner | decided | New repo under harryberg1n | Personal tool, not company infrastructure |
| Name | decided | trellis | A trellis gives a plant structure to grow on without constraining it, as the tool does for Claude. It is short as a CLI word, and nothing called `trellis` is on this box's PATH. harryberg1n/trellis is free. 39 Go repos use the word in their name but none dominates it; keel has 101 and tether 71. It carries no vendor mark. |
| Move and rename | decided, with an open question | Once, together, after the plugin work is proven | One migration, one round of updates on every box; the phases are built in aphrollo/aphrollo-tools first. The design review's point on timing is an open question below. |
| Integration and UI | decided; changed by this proposal | Claude's tools first. Lanes are Claude's native worktrees; review and diffs are Artifact pages; only `trellis ci` on plain git is our own code; GitHub issues where a repo is on GitHub. Proposed: no Artifact dashboard and no general merge queue. | We build gate logic and data; Claude provides the interface. Artifacts live on claude.ai, so pages get aggregated data only; transcripts, tool content and client code stay on the box, and a client diff is shown while viewed, never stored in a page. |
| Analytics data | decided; changed by this proposal | Proposed: gate events with per-stage cost, git and CI events, resource use, in one `events.jsonl` in the store. Previously: everything, always, joined with OpenTelemetry and transcripts. | Local only and secrets redacted before anything is stored stay. The OpenTelemetry join, transcripts, per-owner stores and 90-day raw retention are cut (Cut or deferred). |
| Distribution | decided | A Claude Code plugin that pins one binary version; binaries come from GitHub Releases built on GitHub-hosted runners. No deploy runner, no self-hosted runner. | Hosted runners are free on a public repo, a pinned binary keeps plugin and binary in lockstep, and no box needs a Go toolchain or a runner of its own. |
| Telemetry | decided | None leaves the box. Data stays where it is produced; the release replay uses only this repo, pinned public repos and a synthetic tree. | No third-party data is gathered for now; opt-in summaries, never code, may come later. |

The migration to the new repo, in order:

1. Create harryberg1n/trellis and push the full git history, so blame and bisect keep working.
2. Re-create branch protection, required checks and the hosted-runner CI, plus a release workflow on GitHub-hosted runners that cross-compiles every platform and publishes a GitHub Release with sha256 sums. No self-hosted runner: the deploy job and deploy-prod.sh retire, and the three nightlies (flake hunt, fuzz, mutants) move to hosted runners, joined by the nightly process and memory-cap tier of F.f. Move the secrets.
3. Move the open issues, with links both ways.
4. Ship one last release of aphrollo-tools whose `update` switches every box to the new repo, and whose binary keeps an `aphrollo` alias for one release.
5. Shrink aphrollo/aphrollo-tools to refactor and the LSP client, dev, sqlc and the systemctl atom, with a pointer to trellis in its README.

- [x] Name confirmed
- [x] Detachment scope confirmed
- [x] Analytics data scope confirmed (narrowed by this proposal, pending approval)

The language stays Go. A hook process starts on every edit, and the Go binary starts in 8–9 ms against 28 ms for an empty Node process and 17 ms for an empty Python one. It ships as one static 11.8 MiB binary that cross-compiles to every platform, and the existing code carries over onto the spine. Only the plugin's manifest and config are Claude Code's own format.

Decided on 2026-10-02 for the session flow, after a review of the flow against the code; items marked "changed" follow this proposal:

- **isolation** covers every write on main to a file git would commit (code, tests, docs, config); gitignored files pass. The docs fast path keeps a docs-only lane cheap: it decides how much a gate checks, not where the change is written.
- **Fetching** happens only at SessionStart, Setup and `trellis update`; Repo start refuses a repo whose `requires` the binary does not meet, as "trellis too old here", and names `trellis update`.
- **The pin override** lives in the user layer, for the whole box; per-repo needs go through `requires`.
- **A newer pin** (changed) is used from the next start; the release replay in CI is the check, and the per-box background comparison is cut.
- **The old aphrollo install** (changed) is found by Check the box, which names the one command that removes it, with a backup.
- **trellis not ready** ends trellis for the session; `trellis update`, run through the launcher, repairs it, and later hooks do not fetch again.
- **Repo start** (changed) asks nothing: a repo is in once it has a `trellis.toml` or the user layer turns trellis on everywhere.
- **The setup record and a decline** live in the user layer, keyed by repo, never in the repo; trellis's own config file, not Claude Code's userConfig; TRELLIS\_CONFIG stands in for the user layer.
- **The managed CLAUDE.md block** is dropped once the F.a measurement shows Claude follows EnterWorktree from the deny and the injected rules.
- **pre-push** stays as the attribution check.
- **Merges are local**: CI runs under the merge verb, pre-merge-commit refuses a head without a CI verdict, merge.ff=false; with ci = github, Repo start offers branch protection, and a GitHub-side merge is recorded like an escape.
- **An escape** is only a red after a local green; (changed) it is a product escape or a gate disagreement, and targets count product escapes only. `trellis feedback` records a wrong deny as a gate disagreement.
- **/gate is retired** (changed): `trellis config set` in the user layer replaces it.
- **tdd = warn** gives the next step as guidance; **off** skips red to green but keeps guardrails and laws; Stop and SubagentStop both run the stop check.
- **Formatting** runs at PostToolUse and names the file; the tool's input is never rewritten (R1).
- **A merged lane** is removed at post-merge when no session is in it; its own session leaves with ExitWorktree, and the background sweep removes the rest.
- **Opening a PR** runs no checks of its own; escape fixes are checked at the merge gate.

## Open questions

### For the owner, to approve this proposal

Each needs a yes, a no or a change before the phase it belongs to starts.

- [ ] **Phase F before the rest of 1a.** No consumer-visible feature until F's gate is met (fixtures, budgets on Windows, first-run CI green at or above 85% over a week, inconclusive edit runs under 10%). Proposed: yes.
- [ ] **The cuts.** OpenTelemetry and the transcript join, per-owner stores, 90-day raw retention, the cloud notes ref, the Artifact dashboard, the setup conversation, the fourth config layer and `--session`, the per-box background comparison, the two-release write rule for local state, a general local merge queue, the full local CI. Proposed: all cut, each able to return as a proposal with a measured question behind it.
- [ ] **Phases 2 and 4 become stats from events**, built one question at a time, with no gate of their own. Proposed: yes.
- [ ] **One mutation authority.** CI blocks; commit-time mutation only reports, so a lane can reach CI with a survivor and learn of it there. Proposed: yes; it ends the largest class of gate disagreements and closes #1078.
- [ ] **Escapes split.** Targets count product escapes only; gate disagreements are gate defects. Proposed: yes.
- [ ] **The demotion policy and the budgets.** No catch in 4 weeks, or more disagreements than catches, moves a stage to warn or CI only; commit p95 under 60 s and merge under 5 min on Windows. Proposed: these numbers, revisited after the first month of F.b data.
- [ ] **The length cap on the agent brief.** Proposed: set from the F.b measurement of today's brief, then enforced as a CI check that fails when the brief grows past it.
- [ ] **How a host is marked production.** Proposed: a user-layer key (`host.production = true`) that local CI refuses to run under.
- [ ] **How a repo opts in without the setup conversation.** Proposed: a repo is in once it has a `trellis.toml` or the user layer turns trellis on everywhere; `trellis config set` changes either.
- [ ] **refactor and the LSP client, sqlc and dev stay in aphrollo-tools**, so trellis is the gate alone. Proposed: yes.
- [ ] **When the repo moves.** The owner decided to move after the plugin work is proven. The design review points out that the release workflow, the plugin manifest and the pin URLs all name the repo, so moving at the start of the plugin phase (1b) avoids building them twice. Not a decision of this proposal: the owner chooses.
- [ ] **#999 (first-run setup).** With the setup conversation cut, #999 narrows to defaults plus `trellis config set`. Proposed: rescope it in 1b.

### Decided 2026-10-01 and 2026-10-02

The review on 2026-10-01 found these contradictions and gaps; all were decided by 2026-10-02. Where this proposal changes a decision, the item says so.

- [x] **R1 against R13.** R1 refuses only real guardrails and leaves the rest as guidance, but R13 hard-refuses a code edit without a red. Decided: R1 names enforce-mode red to green and deny laws as guardrails; long waits and noisy output become guidance.
- [x] **One definition of an escape.** Decided: only a red after a local green is an escape (a CI red on a gated head, or a merge-gate red on a tree the commit gate passed); a survivor at commit is a plain refusal. Changed by this proposal: escapes split into product escapes and gate disagreements, targets count product escapes, and a survivor at commit is no longer a refusal (F.e).
- [x] **What remains of aphrollo-tools.** Decided: aphrollo-tools is not archived but shrunk to dev, sqlc and the exact-match systemctl atom, a small company-owned binary with a pointer to trellis; company privileges stay out of the personal repo. Changed by this proposal: refactor and the LSP client stay there too.
- [x] **Local CI on a repo with a GitHub remote.** Decided: the ci setting, auto by default, falls back to local CI when hosted jobs never start (Configuration).
- [x] **#1064 design.** Decided: the ci setting plus a per-merge --ci flag turn it on; CI runs under the merge verb, before git merge --no-ff; PRs merge locally (merge.ff=false) and a GitHub-side merge is recorded like an escape. CI has one entry point, trellis ci; the GitHub workflow is a thin wrapper around it, and local CI runs the same command in a throwaway worktree of the merge result. The verdict is stored per tree hash and reused. #1081 is settled the same way. Changed by this proposal: local CI is the hermetic checks only, serial, at low priority, never a global install, refused on a host marked production; local mutation is advisory.
- [x] **find and outline against show.** Decided: a verb stays only if Claude Code has no equivalent and the event log shows it used; find, outline and show go (Read and Grep cover them). Changed by this proposal: rename-symbol stays in aphrollo-tools, not in trellis.
- [x] **The merge gate in the change flow.** Decided: the session flow in docs/trellis-flow draws the commit, push and merge gates and CI at the hooks that run them.
- [x] **Baselines for the targets.** Decided: targets become rates per merged PR, re-baselined after 2 weeks of event-log data. Changed by this proposal: the 2026-10-02 audit is the baseline until then.
- [x] **Sessions nobody answers.** Decided: they take the defaults or a file handed in through TRELLIS\_CONFIG, and subagents inherit the main session's setup. In a cloud session the session's assigned branch is the lane; trellis detects the remote environment and makes no worktrees there, and one issue gets one cloud session. Undercover accepts that assigned branch name, matched exactly (#1089), and keeps checking what is permanent: commit and merge messages, the PR title and body, and authorship. The merge verb writes its own merge subject. The branch name stays visible on the PR page, which is accepted; post-merge deletes the branch. With the setup conversation cut, every session takes this path.
- [x] **R13 edge cases.** Decided: a characterization test that passes at once pins today's behaviour and admits only refactors whose tests stay green; a change of behaviour needs it red first. Deleting code needs no red; a test goes only with the code it covers (test\_removed). Config, docs and generated files sit outside red to green, laws still apply, and an edit to a generated file is refused, naming its generator. Adding a test is always allowed; changing an assertion while code edits are closed needs a new red. A red opens the failing test's package and the packages on its import path, from the test map.
- [x] **Measurable gates and owners.** Decided: every gate is a CI check. Setup on a new box is a throwaway-container test that enables the plugin, runs a claude -p task and asserts the gate fired and Repo ready was reached. The owner of every phase is the repo owner. Changed by this proposal: the dashboard's gate goes with the dashboard.
- [x] **The global git gate fires everywhere.** Decided: Repo start installs the git gate per repo; no global core.hooksPath. Changed by this proposal: the git PATH shim shrinks to the queue lock (F.c).
- [x] **Claude Code versions.** Decided: the minimum is the release that added the newest hook or tool trellis relies on (PostToolBatch, EnterWorktree, the SessionStart source matcher); recorded payload fixtures per hook are checked in CI (recorded in F.a); a nightly job runs a real claude -p smoke task on the latest Claude Code; below the minimum, Check the box names it and trellis stays off for the session.
- [x] **Where lanes come from.** Decided: `isolation = true` is the default, so every write on main gets a lane. On main, trellis makes the worktree itself (`git worktree add` under .claude/worktrees/, from main), the deny names its path, and Claude passes that path to EnterWorktree and repeats the edit. A builder subagent with `isolation: "worktree"` gets its worktree from Claude Code. `isolation = false` keeps a repo's work on main. trellis hooks neither WorktreeCreate nor WorktreeRemove. Nothing is redirected silently: PreToolUse updatedInput stays unused (R1).
- [x] **trellis lite for folders without git (deferred).** Decided: stays deferred, since git init is cheap; revisit only if the event log shows declined folders without git.
- [x] **Event store on short-lived machines.** Decided: the store lives in CLAUDE\_PLUGIN\_DATA; secrets are redacted at write time by known patterns plus an entropy check; trellis events purge --repo or --before deletes on request. Changed by this proposal: the cloud notes ref and the 90-day raw retention are cut; retention is set per collection in the store.

## Queue mapped onto the phases

| Phase | Open item | Why here |
| --- | --- | --- |
| 0 · gate | fanvue merges through local CI | The phase 0 gate; not yet met |
| 0 · gate | #1102 local CI ran `pip install` into a production host's global Python; #1103 parallel jobs at normal priority hit the deadline. In progress | Local CI must be hermetic, serial and low priority before fanvue relies on it |
| F.a | Windows `-p` recordings done 2026-10-02. Still to do: the Linux recordings; TaskCompleted in an interactive session; time a no-op hook through the launcher on Windows; measure lane entry after a deny; check CLAUDE\_ENV\_FILE for PowerShell; replace #1111's hand-written payloads with the recordings | Nothing below is built on a guessed payload |
| F.b | Per-stage cost and catch counts; budgets; product escapes against gate disagreements; the demotion policy; the brief length cap | Enforce only what earns its cost |
| F.c | `internal/store`, `internal/run`, `internal/git`, `internal/config`; retire the tddsplit forwarders; packages by domain; seams in one struct; the git shim shrunk to the queue lock | One way to hold state, run a process and read git |
| F.d | The lane state machine on the store; hooks as thin adapters | trellis's kernel; R13 rests on it |
| F.e | One mutation authority: CI blocks, commit-time mutation advisory; closes #1078; mutation rules leave the brief | 12 of 26 escapes in a week were this disagreement |
| F.f | Test tiers: unit with -race, integration on a template repo, process and memory-cap tests nightly behind a build tag; YAML pins become ratchet laws | Fast tests on every edit, real processes every night |
| 1a · core | Workstreams 1, 2, 3 and 5 on the store and the state machine | Red to green, guardrails, escapes and edit results, built once on the spine |
| 1a · core | Compatibility items 3 (committed-data migration), 4 (release replay) and 6 (rollback) | Items 1 and 2 landed in #1106 |
| 1a · core | #996 Windows command line too long on a merge commit (partly fixed by #1093, reopened) | Windows is first-class; the argv budget moves into `internal/run` |
| 1b · plugin | Launcher and binary, release workflow on GitHub-hosted runners; retire deploy-prod.sh and the deploy job; move the three nightlies to hosted runners | No self-hosted runner remains; the plugin fetches released binaries |
| 1b · plugin | Configuration verbs (three layers, `trellis config show` and `set`), session start, repo start, lanes on a write to main; #999 narrowed to defaults plus config set | Setup happens by itself on Claude Code's own hooks and worktrees |
| 3 · integration | `trellis ci` under the merge verb, the verdict per tree hash, local merges; Artifact pages for review and diffs | A merge completes without GitHub |
| stats | Each metric as a question needs it: time to green, refused code edits, wrong denies, product escapes per gate, enforce against warn | Numbers built when someone asks, not ahead of it |
| move | To harryberg1n/trellis, after the plugin work is proven (timing is an open question) | One migration |
| any | #963 JS/TS presets, #962 StrykerJS | Language breadth; independent of the phases |
| any | The paused lane's rest: the zig row and the PHP attribute and heredoc fixtures that #1071 deferred until the installed binary carries it; a `--dry` for top-level `install` | Small, independent |
| any | trellis lite for folders without git | Deferred: git init is cheap |
| last | #873 and #878 docs sweep | Docs follow the finished surface |
| blocked | #912 live sandbox, #865 build pre-emption | Each waits on you or an observed case |
| leaves trellis | refactor and the LSP client, sqlc, dev | Stay in aphrollo-tools |

## The whole flow

One diagram holds a whole session: Claude Code's hook lifecycle with each trellis flow at the hook that runs it, so the whole process can be checked in one place and a step with no hook under it shows as a gap. There is no after the session: the push, CI, the escape and the retro all run at a hook inside it, and a defect found later is recorded in a later session through the same loop. The diagram is generated from `docs/trellis-flow/session_flow.py` in the repo, which is its source; zoom in, or open `docs/trellis-flow/workbench.html` to run its layout check. It still draws the previous revision; if this proposal is approved, the setup conversation and the local config layer leave it in 1b (workstream 13).

<img alt="A session with trellis on Claude Code's hook lifecycle" src="trellis-flow/session-dark.svg">
