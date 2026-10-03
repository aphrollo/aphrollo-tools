Approved 2026-10-03.

# trellis: architecture (final)

## Executive summary

1. **Decision: a hybrid strangler.**
   - Write the spine and kernel fresh beside aphrollo: `run`, `git`, `store`, `config`, `kernel`, `render` and `measure`, about 12–15k production lines.
   - Move the parts that already work over unchanged: laws, fail-first, deferral, `gitiso` and `ghworkflow`.
   - Move to harryberg1n/trellis at the start of 1b.
   - Not greenfield: three consumer repos cannot stop, and release replay needs the old binary as its oracle.
2. **One pure kernel decides everything.** It holds a lane machine, a TDD machine per unit and one rule table. Hooks, git hooks, shims and verbs are thin adapters: payload → event → `kernel.Step` → one rendered line.
3. **A verdict is a fact about a tree.** One authority makes it once, and later stages reuse it.
   - A batch green is the commit's proof when the index is the tree that was tested.
   - CI's verdict per OS is the merge gate. It is re-checked only where trunk's change and the PR's change meet.
4. **Guide, not cage, and measured.**
   - Hard blocks only for real damage done by Claude (`CLAUDECODE=1`; a human at a terminal gets advice).
   - Other blocks stay only where a 10% holdout shows they catch more than they cost.
   - Every deny names its override. Demotion is proposed from data, and the owner confirms it.
5. **One store per repo, outside the plugin.** It holds an append-only event log and lane checkpoints keyed by branch and actor. Fold versions let two binaries share it.
6. **`run` owns every child process.** It brings kill-on-close jobs, a governor whose wait is spent inside the visible budget, and a sealed environment.
7. **Shipping.**
   - Boxes follow release tags, never `main`.
   - aphrollo goes silent in any repo that holds `trellis.toml`.
   - From F5 on, every tag is replayed on Windows and Linux before a box moves to it.
8. **The first three lanes:**
   - **F0a:** the release channel, plus aphrollo's silence switch.
   - **F0b:** hot-path fixes. The hidden 150 s slot wait goes, the edit-time lint and mutants jobs go, and the fingerprint reads the lane's own index.
   - **F1:** mutation at the level its data earns. Commit mutation becomes report-only, and CI mutation becomes a report unless a repo pins it. The note names what went unmeasured.
9. **Next:** a sharded Windows CI test job, then merge reuse of CI's per-OS verdicts. The merge gate drops from p50 261 s to about 1 s when the tree matches.
10. **Effort:** about 66 lanes and about 12 weeks, with at most 4 lanes open and 2 on the spine. F takes about 6 weeks, against the proposed 3-week timebox; the owner sets the new one.

*Sources.* Platform facts were re-checked on 2026-10-03 against the Claude Code docs (hooks, plugins, sub-agents, worktrees) and this session's environment. Repo facts are at `e813bb71`. Red-team findings are cited as C1–C17 (still-a-cage) and S1–S17 (wont-ship), in the order they were filed.

## 1. Thesis

trellis is a gate built around lanes and an event log.

- **Decisions.** One pure kernel makes every decision: the lane machine, a TDD machine per unit, and the rule table. Every Claude Code hook, git hook, shim and CLI verb is a thin adapter. It turns its payload into an event, steps the kernel once and renders one response.
- **Verdicts.** Every check has exactly one authority. Its verdict is a fact about a tree, computed once and reused.
  - A batch's green is the commit's evidence when the commit's index holds exactly the tree that was tested.
  - CI's verdict for each declared OS is the merge gate. Locally, trellis re-tests only the units where trunk's changes and the PR's change meet.
- **Feedback.** Claude hears from trellis at the tool call, in one line that names the next step: at most 60 tokens for a green, and at most 400 for a red, including the first failing assertion.
- **Denies.** A deny comes only for real damage done by Claude, or from a rule whose holdout shows it catches more than it costs. Every deny names its override. A human at a terminal is advised, never blocked.
- **Self-measurement.** The gate is held to Claude's own currency: turns, tokens and wall time. It counts its catches against a counterfactual and proposes demotion for any rule that does not pay for itself.
- **Delivery.** It is built by strangler on aphrollo and shipped by release tag, never by merge.

## 2. Shape: components and boundaries

```
 Claude Code hooks ─┐  git hooks ─┐  shims git, cargo ─┐  trellis <verb> ─┐  CI: trellis ci, gh check runs
 (plugin: hooks.json exec form · launch/ · bin/trellis wrapper · skills/trellis · agents/builder, reviewer)
                    ▼             ▼                    ▼                  ▼
 adapters   hook · githook · shim · cli        payload → kernel.Event   ·   render.Response → Claude
                │
 engine     lock → load checkpoint → kernel.Step → append events → save checkpoint → unlock → run effects
                │                       │
 kernel     PURE: lane machine · TDD machine per unit · rule table (walls, guardrails, red→green, merge) · levels
 domains    check (runners, scope, verdict cache, coalesce, defer, harvest) · laws (ratchet, lang, mask, tomlsubset)
            proof (fail-first, ledger) · integrate (pr, merge, closure, ci, escapes) · measure (folds, holdout, Decide) · render
 spine      run ──▶ git ──▶ store ──▶ config                                test-only: gitiso, rootseam
```

- **Dependencies point down only.**
  - `kernel` and `measure` import only the standard library and kernel types: no `os`, `os/exec` or `time.Now`.
  - Effects (`RunUnits`, `InstallDeps`, `KillJobs`, `WriteNote`) run outside the lock and come back as events.
  - The `go-dep-graph` law holds the layering.
  - The generated forwarders (about 1,400 symbols in some 160 files) retire package by package, inside the lane that rewrites each package. A `forwarder_count` law, whose baseline only goes down, counts them. The generator is deleted at M1 (S4).
- **Each resource has one owner.** A zero-target law with a baseline that only goes down holds each one:
  - `run` owns every child process. Its law, `exec_outside_run`, counts about 170 sites in 99 files today and replaces the three `argvbatch` inventory tests (S16).
  - `git` owns every git call.
  - `store` owns every byte under the state root.
  - `config` owns every setting and environment read.
  - `render` owns every byte Claude reads.
- **Each rule exists once, and trellis adopts rules Claude Code already enforces rather than copying them.**
  - Inside a Claude Code worktree, Claude Code itself refuses an Edit, Write or NotebookEdit aimed at the main checkout, a command whose working directory is there, and git redirected into it.
  - trellis's wall covers what that leaves: a session on trunk in the primary checkout that is not in a worktree, and Bash or PowerShell writes that land there by path. For PowerShell, Claude Code checks only the working directory.
  - The shim covers what no hook sees: bypass flags and branch moves inside scripts.
- **Nothing moves for its own sake.** `refactor`/LSP, `sqlc`, `dev` and `workspace claim` stay in aphrollo-tools.

## 3. Data model: state, events, lane lifecycle

| Concept | Meaning |
|---|---|
| Lane | A branch and its worktree. It is the unit of state, keyed by branch (trunk is `@trunk`) |
| Actor | `session_id/agent_id` from the hook payload. Git hooks and shims read `CLAUDE_CODE_SESSION_ID`, which Claude's Bash tool exports; that lets them name the actor and honour a session's `allow` without the spent-argv bridge (#894). A session is never a state key, because subagents share their parent's session id |
| Tree keys | **Worktree key:** the sha256 of HEAD's tree oid plus the sorted `(path, blob)` pairs of modified, staged and untracked paths. **Index tree:** the result of `git write-tree`. Each verdict names the key it measured. A commit maps its worktree key to its tree oid, so CI's verdict can be joined to it |
| Verdict | One authority's result for one key. A run gives green, red, red-bogus or not-tested. A stage gives pass, refuse or unmeasured. *Pending* is not a verdict |
| Rule | Anything that denies or warns. It has an id, a level (block, warn, guide, off) and counts of shadow catches, shadow passes, wrong blocks, disagreements and compliance |
| Escape | A red after a local green on the same head. A *product* escape is a CI test red or a defect on trunk; a *disagreement* is the gate contradicting itself |

**Storage.**
- **State root** (S7): `$TRELLIS_DATA`, else `%LOCALAPPDATA%\trellis` on Windows or `${XDG_STATE_HOME:-~/.local/state}/trellis` on Linux.
  - It does not depend on the plugin, so git hooks run from a terminal and shims both find it.
  - Uninstalling the plugin keeps it; `trellis eject` exports or deletes it.
  - It holds:
    - `config.toml`, the user layer;
    - `hooks/<repo>/`, the git hooks;
    - `wt/<hash>/`, the fail-first checkouts, on a short path (#1112);
    - `state/<repo>/`, where `<repo>` is the first 16 hex digits of the sha256 of the git common dir.
- **Binaries** (C4, S8) live in `${CLAUDE_PLUGIN_DATA}/bin/`. Uninstall deletes them, and reinstalling fetches them again.
  - `<version>/trellis.exe` holds the last 3 versions; `trellis.exe` is the current one.
  - The file is named `.exe` on Linux too, where the name does not matter and the folder belongs to one box. So one `hooks.json` path serves both OSes, and no extensionless file exists on Windows (#366).
  - The current binary is swapped one file at a time, using today's `swapBinary` rename-aside plus the stale sweep (#338). A directory that holds a running image is never renamed.
- **Slot locks** stay machine-wide: `%ProgramData%\trellis\locks` and `/var/tmp/trellis-locks`.

| Under `state/<repo>/` | Holds | Writer | Retention |
|---|---|---|---|
| `events-YYYY-MM.jsonl` | The only log | Engine, under the repo lock | 16 weeks |
| `lanes/<branch>.json` | Lane checkpoint `{"f":1,"fold":n,"seq":n,…}` | Engine, under the repo lock | 7 days after `removed` |
| `verdicts/<key>.json` | Runs per (runner, unit); laws; CI `{os, by, run, head, tree, conclusion, mutation}` | Written once per key | 14 days or until the lane closes; LRU 2,000 |
| `jobs/`, `out/` | Deferred runs; every run's raw output, unfiltered | One writer per file | 24 h after harvest; `out/` LRU 200 MB |
| `cache/` | Ratchet scan cache | `laws` | LRU 200 MB (unbounded today) |

**Events.** Each event is one line, for example:

`{"v":1,"seq":4182,"at":"2026-10-02T09:14:03.120Z","lane":"worktree-fix-parse","actor":"60cd…/a26c…","kind":"run.result","key":"9f2c…","unit":"internal/lane","result":"red","test":"TestOpenOnRed","ms":840}`

- **Kinds:** `lane.opened|entered|left|merged|removed`, `edit`, `run.requested|result`, `deny`, `guide`, `shadow`, `override`, `feedback`, `commit.gated`, `pr.opened`, `ci.verdict`, `merge.path`, `escape`, `trunk.diverged`, `stage.timing`, `hook.timing`, `hook.error`, `rule.level`, `config.set` and `migration`.
- **Content:** events carry metadata only, never file content. Secrets are redacted with the `secrets` law's patterns before the write.
- **Edit ledger:** a fold of the `edit` and `run.result` events.

**Lane record.**

`{f, fold, seq, branch, worktree, base, head, life, actors:{actor: last_seen}, seen:{actor: seq}, deps, tdd:{unit: {state, test, last_real}}, unproven:[unit], holds:[escape]}`

- `actors` is the session registry that post-merge needs.
- `base` is recorded at `lane.opened` (C5).
- `last_real` is seeded from the base's gated note or CI verdict, so a fresh lane does not read as untested everywhere (C13).

| From | Event | To |
|---|---|---|
| none | The first hook whose cwd is a Claude Code worktree under `.claude/worktrees/` (made by `EnterWorktree name=` after a primary-write deny, or by `isolation: worktree`) | open. Dependency install and test-build warm-up start in the background |
| open | A gated commit, then `pr.opened` | committed, then pr |
| pr | CI starts, then concludes on each declared OS | ci_pending, then ci_green, ci_red or ci_unavailable |
| ci_* | A new head is pushed | pr |
| ci_green | A merge: through the API, by local `--no-ff`, or found by post-merge ancestry | merged |
| merged | No actor for 30 min, the tree is clean, and Claude Code holds no worktree lock | removed. This one rule replaces four separate sweeps |
| open, committed | No actor, no PR, idle for 14 days | abandoned: listed by `trellis stats`, never deleted unless asked |

**TDD machine.** There is one per unit. It runs under `tdd = warn` or `enforce`; `off` freezes it. T is a new or changed test, or an existing test this lane's edits broke. Only a new or changed T earns the red→green pair the commit proof reads.

| State | Test T added or changed | Real red of T | Code edit | T green | Bogus red, or not tested | Product escape |
|---|---|---|---|---|---|---|
| closed | → pending(T) | → open(T) | Tested code is allowed. Anything else gets one guidance line per unit per lane (enforce: deny) | Stay | Stay; the cause is named | A CI test red of T → open(T); a trunk escape → held |
| pending(T) | Stay | → open(T) | Allowed while the verdict is on its way (C2) | → closed: "T passed at once; not a red" | Stay; the cause is named (the commit proof is the backstop) | As in closed |
| open(T) | Stays open. It closes only if T then passes with no code change, or T is removed | Stay | Allowed | → closed; the red→green pair is recorded | Stay: an open red survives a build break | Stay |
| held | → pending(T) | → open(T), which releases the hold | Guidance at every level (C9) | Stay: a passing test does not reproduce the escape | Stay | Stay |

- **Tested code** (S12). Two conditions: every changed line lies in a function that a passing test of the unit executes, and the edit adds no exported symbol and no new function.
  - For Go, "executes" is read from the `-coverprofile` of the last green run.
  - Otherwise it comes from the per-function test map that already exists, built after a merge by running each test alone under coverage.
  - Where neither is available, a test file of the unit must name the symbol.
- **Writes nobody parsed.** A write that no parser saw marks its unit unproven until the next real run. A not-tested result never moves a state.
- **Concurrency.**
  - There is one lock per repo: LockFileEx or flock, blocking, capped at 2 s.
  - It is held only for load, Step, append and save (p99 under 5 ms), and never across an exec.
  - Results come back as events keyed by job id, so a retried transaction is idempotent.
  - PreToolUse reads checkpoints without taking the lock.
- **Crash safety.**
  - The event is written before the checkpoint. The event is one `O_APPEND`/`FILE_APPEND_DATA` write; the checkpoint is a temp file plus rename.
  - A checkpoint that is behind the log is refolded from its `seq`.
  - A torn last line is skipped, then truncated by the next writer that holds the lock.
  - There is no fsync on the hot path.
  - Jobs carry their pid and creation time, read in-process (`/proc/<pid>/stat`, `GetProcessTimes`), never by spawning `ps`.
- **Versioning** (S9).
  - Every record carries `v`, every file `f`, and every checkpoint `fold`, the fold version of the binary that wrote it.
  - The log is append-only, so an older binary never loses a newer event.
  - A binary with a lower fold version appends events but never writes a checkpoint. A binary with a higher one refolds from the checkpoint plus the events.
  - A newer `f` is refused by name: "state format 2 needs trellis ≥ 1.6: run trellis update".
  - Local state is copied to `.bak.<version>` before a migration. Committed data migrates in its own lane, under the two-release rule.
  - Release replay runs two binaries against one store.

## 4. Hook-by-hook behaviour

- **Wiring** (C4).
  - `hooks.json` uses the exec form: `args`, no shell.
  - SessionStart runs `${CLAUDE_PLUGIN_ROOT}/launch/launch`, a native launcher shipped as both `launch` and `launch.exe`. F3 checks whether the exec form resolves the extensionless name to `.exe` on Windows. If it does not, SessionStart alone falls back to the shell form under Git Bash, and its time is measured.
  - The launcher makes `bin/trellis.exe` the pinned binary. Failing that, it uses the newest kept binary and warns once. Failing that, it installs a copy of itself, which answers every hook with a silent exit 0. Then it hands over.
  - Every other event runs `${CLAUDE_PLUGIN_DATA}/bin/trellis.exe hook <event>` directly.
  - A `trellis` wrapper in the plugin's `bin/` puts the verbs on the Bash tool's PATH.
- **Timeouts.** Matching hooks run in parallel. Claude Code's timeouts default to 600 s (UserPromptSubmit 30 s; SessionEnd hooks share 1.5 s). trellis sets each one to 3× its Go-side deadline.
- **Off here.** Applies when there is no `trellis.toml`, the user layer declines the repo, or `TRELLIS_OFF=1` is set. The hook exits 0 with no output within 50 ms and opens no store. An internal error also exits 0, records `hook.error`, and is reported once per session.
- **Git hooks** (S2, C6).
  - Repo start sets a repo-local `core.hooksPath` pointing at `<root>/hooks/<repo>/`. It overrides the global setting, which stays in place until M1.
  - The scripts call the binary by its absolute path and exit 0 if it is gone, so the commit is visibly ungated: it has no note.
  - Walls act only under `CLAUDECODE=1`. A command that clears that variable counts as a bypass.
- **Shims** (C16).
  - Native `git` and `cargo` shims live in `${CLAUDE_PLUGIN_DATA}/shims/`. They go first on PATH, through `CLAUDE_ENV_FILE`, only when SessionStart or CwdChanged finds an opted-in repo.
  - Off here they pass straight through, with a target p95 of at most 15 ms on Windows, timed in F3.

| Hook | Does | Budget | Returns to Claude |
|---|---|---|---|
| Setup (`init`, `maintenance`) | Fetches the pinned binary and verifies its sha256; runs repo start for the cwd | 30 s | Nothing (Setup output is discarded); reported at the next hook |
| SessionStart | The launcher, which fetches only when the pin is missing (10 s cap). On clear or compact when ready: no checks. Otherwise: Check the box from a cached record, schema-check the user layer, record the actor, fold the lane, and start a due gc in the background | 200 ms idle | Opted-in repos only: `additionalContext` of at most 400 tokens (this lane's open red, what Check the box found). The static rules live in the managed block |
| UserPromptSubmit | Harvest finished jobs; deliver unseen verdicts; report lane news; run repo start for a repo seen for the first time | 100 ms | One line per item |
| CwdChanged, DirectoryAdded | Repo start; CwdChanged also puts the shims on PATH | 300 ms the first time, 20 ms after | Nothing reaches Claude (per the docs); the line rides the next hook |
| PreToolUse (Edit, Write, MultiEdit, NotebookEdit, Bash, PowerShell) | Reads the checkpoint, `HEAD` and the gitdir in-process, so no git spawn. Then, in order: guardrails; where each target lands (Bash and PowerShell commands are parsed); the wall; red→green; the deny-law weight delta under a deadline. On overrun it says "law check deferred: judged at commit" and queues the law for the run (C15). A new `agent_id`'s first call carries its brief until SubagentStart delivery is recorded (C12) | 50 ms; 150 ms with a law check (timed in F4) | `permissionDecision: deny` in at most 120 tokens (rule, cause, next step, override), or guidance in at most 60. Never `updatedInput` |
| PostToolUse (Edit, Write, MultiEdit, NotebookEdit; Bash and PowerShell writes found by diff) | Formats the file (Go in-process); writes an `edit` event with the content hash; checks laws on the edited lines; requests the unit's run, coalesced per lane and unit (§6) | 300 ms plus the run's foreground budget | "formatted x.go", a warn-law line, or the run's line |
| PostToolBatch | Delivers what has finished. It becomes the run trigger only if F4 shows at least 1.5 edits per batch; then it also runs one `git status --porcelain=v2 -z` per lane to catch writes no Edit named (C3, S11) | 20 ms | One line per unit |
| SubagentStart | Records the actor on the lane its cwd names | 50 ms | At most 250 tokens: lane, `tdd` mode, open reds |
| Stop, SubagentStop | Blocks once on an unseen red for this actor under enforce, unless `stop_hook_active` is set | 100 ms | `decision: block`, with the red as the reason |
| TaskCompleted | Exits 2 while the task lane's units are red under enforce. Per the docs, exit 2 prevents completion, and `continue: false` is ignored under TaskUpdate | 100 ms | stderr: the failing test |
| SessionEnd | Drops this session's actors; kills jobs nobody else waits on | 1.5 s, shared | Nothing |
| pre-commit | A merge in progress goes to the merge gate; a docs-only commit takes a fast path. Then: baseline guard; laws (`Plan(commit)`); docs; suppressions; vet and lint. The red→green proof comes from the ledger only when the index holds exactly the measured worktree: no unstaged change, and no untracked file the key included (S6). Otherwise it is re-run at HEAD in `wt/<hash>`. Mutation runs report-only, for at most 90 s. Under `CLAUDECODE=1`, a non-merge commit on trunk in the primary checkout is refused | p95 60 s, p50 5 s | `Commit refused: [stage] cause · do: …` |
| commit-msg | Checks attribution, in an undercover repo | 50 ms | The offending line, quoted |
| post-commit | Writes a note on `refs/notes/trellis`: `gated v1 tree=<oid> proof=ledger·rerun laws=pass mutation=<k/n, unmeasured n>`; writes a `commit.gated` event | 100 ms | – |
| pre-push | Checks attribution in commits and ref names. Under `CLAUDECODE=1`, refuses a push to trunk whose first-parent chain adds a non-merge commit (C17) | 200 ms | The commit or ref, named |
| pre-merge-commit | Needs a green verdict for exactly this merged tree on every declared OS, or else runs the closure (§8); runs laws on the merged tree | About 1 s with verdicts; at most 5 min | `Merge refused: …` |
| post-merge | Writes `lane.merged`, including for a fast-forward of trunk after an API merge (#1107); removes merged lanes in the background; runs the retro | 500 ms | At the next hook: "lane X closed" |
| Shims `git`, `cargo` | Take a governor slot for builds, so a build queues visibly. Under `CLAUDECODE=1`, refuse `--no-verify`, `-c core.hooksPath`, and a move off trunk in the primary checkout | Counted against the caller | Rule, remedy, override |

**Not used:**
- PermissionRequest, PermissionDenied, PostToolUseFailure, PreCompact, PostCompact, TaskCreated, UserPromptExpansion and Elicitation.
- WorktreeCreate and WorktreeRemove, because a hook there replaces git's own worktree creation.

## 5. The agent's experience

trellis speaks at these moments, and no others:
- once per session per repo, in the brief;
- before a tool call, when a rule fires;
- after a run. A deferred result arrives at the next hook, or as a wake if it goes red;
- when it refuses a commit;
- at a merge;
- at Stop, blocking once, under enforce only.

```
trellis: green internal/lane (14 passed, 2.1s) · next: commit, or the next failing test
trellis: pending internal/lane TestOpenOnRed · run j42 queued · code edits stay open in internal/lane meanwhile
trellis: red internal/lane TestOpenOnRed · lane_test.go:41: want open, got closed · code edits open in internal/lane until it is green
trellis: red-bogus internal/store · build failed: store.go:88: undefined: lockPath · fix the setup; this is not a red
trellis: not tested internal/run · deps installing in lane fix-parse · last real: green @a1b2
trellis deny [primary-write] Write lands in the main checkout on trunk · do: EnterWorktree name=fix-parse · override: trellis allow primary-write --once · wrong? trellis feedback d-7f3a
```

- **Depth on demand.**
  - A red carries its first failing assertion, up to 12 lines.
  - `trellis output <run>` prints a whole run, unfiltered.
  - `trellis why <seq>` replays a deny or a verdict, with the rule's shadow catches, shadow passes and compliance.
  - `trellis status --line` costs no tokens. `trellis statusline on` wires it up, since plugin `settings` honour only `agent` and `subagentStatusLine`.
- **Tokens** (C13).
  - Caps: brief 400, subagent brief 250, green 60, red 400, deny 120.
  - The per-task measure counts render bytes plus the managed block, the skill and the briefs. The target is at most 2,500 tokens and at most 2 gate round trips per merged task.
  - Guidance is given once per unit per lane.
  - `render`'s golden tests hold the caps.
- **Skills, agents, worktrees.**
  - **The managed CLAUDE.md block** shrinks to at most 15 lines. It carries the worktree instruction that EnterWorktree's own description requires, "the hooks run the tests", how to read a trellis line, and the skill's name. CLAUDE.md loads into every subagent by default, so the static rules never depend on SubagentStart delivery (C12).
  - **Skill and agents.**
    - One `trellis` skill, at most 150 lines, is preloaded into the plugin's `builder` agent (`isolation: worktree`, `skills: [trellis]`).
    - Plugin agents cannot carry hooks, mcpServers or permissionMode.
    - A `reviewer` agent runs `/code-review`.
  - **Lanes** are Claude Code worktrees under `.claude/worktrees/`. Entering a worktree outside that directory needs the user's approval.
  - **Base branch.** `trellis init` commits `worktree.baseRef = "head"`. Builders and `EnterWorktree name=` started in the primary checkout then branch from local trunk; started from inside a lane, they branch from that lane, and `base` records which.
  - **Files and dependencies.** `.worktreeinclude` carries gitignored files into new worktrees. trellis installs or links dependencies at `lane.opened` (C5).
  - **CI waits** run as background Bash (`trellis ci wait`), which re-invokes Claude when CI concludes.

| Level | Rules |
|---|---|
| Block always: real damage, never demoted | **Walls on where and how work happens.** These act only under `CLAUDECODE=1`; a human gets the same line as advice (C6). They cover: a write into the primary checkout on trunk (`isolation = true`); `--no-verify`, `-c core.hooksPath` or a move off trunk there; a push to trunk or a `gh pr merge` that goes around the merge gate; a merge without a green verdict on every declared OS; discarding uncommitted work; attribution in an undercover repo. **Secrets.** A secret in a write or commit blocks for every author. It is a law with fixtures, an escape comment and a fixture-path scope, and its wrong blocks are counted (C14). **The real repo.** A test touching the real repo or the global git config cannot happen in `run`'s sealed environment, and the canary refuses the result |
| Block, earned: shadowed and demotable | A deny law whose weight in the file rises (at edit) or that regresses (at commit); the commit red→green proof; vet; an outward call that bypasses a verb; red→green under `tdd = enforce`; CI mutation and commit mutation only where a repo pins `block` |
| Guide: `additionalContext`, never a deny | Red→green under `warn` (the default); warn laws; lint; long foreground waits; re-running a suite the run already covered; noisy output; mutation survivors in a repo that opted in (report by default); not tested; escape holds |

**How an earned block keeps its level** (C7, S10).
- **The holdout.** 10% of each earned block's fires are shadowed: the rule warns instead of denying, and writes a `shadow` event. A rule whose level is pinned is never shadowed.
- **What counts.**
  - A *catch* is a shadowed fire whose flagged change later failed a downstream authority in the named scope: the commit proof, CI, or a trunk product escape.
  - A *shadow pass* is a shadowed fire whose change merged and stayed clean for 28 days.
  - A live block counts only as *compliance*.
- **Keeping the level.** A rule keeps its level while shadow catches ≥ shadow passes + wrong blocks + disagreements, over its last 10 or more shadowed fires. Until it has 10, its default or pinned level stands.
- **Changing the level.**
  - `measure.Decide` is pure and table-tested. For the first 8 weeks it only proposes changes: one SessionStart line, confirmed with `trellis rules apply`. After that it writes `rule.level` itself, with a changelog line.
  - A warn rule that preceded 2 or more product escapes is proposed for promotion.
  - `[rules]` in `trellis.toml` pins a level.

## 6. Execution model

- **`run` is the only place that starts processes.**
  - Light children (git plumbing, formatters) get a timeout and the plain environment.
  - Heavy children (builds, tests, lint, local CI, mutation) also get a governor slot, a job object or process group, and a memory cap. They run at below-normal priority when not in the foreground, keep the self-spawn guards (#997), and run in the sealed environment (`gitenv.Sealed`).
  - The first `run` lane moves `argvbatch` inside `run` and lands the `exec_outside_run` law.
- **Windows.**
  - A heavy child starts suspended, joins a job with `KILL_ON_JOB_CLOSE | JOB_MEMORY` and no breakaway, then resumes. That is today's `memcap_run_windows.go` sequence, with kill-on-close added and applied to every heavy child.
  - Closing the handle kills the whole tree, MSYS grandchildren included.
  - A deferred run is a detached `trellis.exe run --job <id>` that owns its children's job.
- **Linux.**
  - Processes get `Setpgid` and are killed with `kill(-pgid)`.
  - The memory cap is a `systemd-run` scope with `MemoryMax` and `TasksMax`, or else the RSS watchdog.
  - Never `RLIMIT_AS` or `RLIMIT_DATA`: `-race` reserves TSan shadow memory and dies under them. This is a deliberate deviation from the roadmap's "rlimits".
  - Nothing relies on `Pdeathsig`.
- **Governor.**
  - Heavy slots = clamp(min(threads/8, free GB/8), 1, 3), and a spawn needs 4 GB of headroom.
  - The queue is FIFO, and a newer request for the same lane and unit replaces a queued one.
  - The slot wait is spent inside the visible foreground budget (F0b). In 3 days, 46 edits waited out the hidden 150 s and tested nothing.
- **Lane warm-up** (C2, C5). At `lane.opened`, in the background at idle priority under the governor, `depinstall` installs or links dependencies, then the test build is warmed. For cargo that is `cargo test --no-run`; Go's shared build cache makes it nearly free for Go. Until dependencies are in, a run reports "not tested: deps installing".
- **Scoping.**
  - Units come from the language row: Go package, cargo crate, vitest related tests, pytest path.
  - `[test] reads` adds tests that read other paths.
  - A build-config edit widens the scope to the whole module.
  - `argvbatch` cuts argument lists to their budget.
- **Caching.**
  - A verdict is keyed by (runner, unit, worktree key) and reused by the run, the commit and the merge.
  - A green is cached only if the tree did not move during the run. A run whose tree moved still delivers its verdict, labelled stale (#813).
  - `-count=1` stays (#421).
  - A verdict that flips at an unchanged key is recorded as a flaky disagreement.
- **Tiers.**
  - **T0, run:** the edited units, skipping the slow tag.
    - The foreground budget per runner is clamp(p80 of its warm runs, 10 s, 60 s), 20 s by default, slot wait included. After that the run is deferred, capped at 600 s.
    - Deferred is *pending*, not *not tested*. Not tested means it timed out at the cap, was skipped, or failed to start (C2).
    - Two timeouts at one key mean SKIPPED.
  - **T1, commit:** the proof only.
  - **T2, CI:**
    - the full suite with `-race` on every OS in `ci.os` (this repo: Linux, plus a sharded, required windows-latest job; S3);
    - lint;
    - laws `--base`;
    - the mutation report.
  - **T3, nightly:**
    - whole-tree mutation;
    - fuzz;
    - flake hunt;
    - the vacuous-test probe.
  - **Merge:** a verdict lookup plus the closure (§8).
- **Mutation** (C1).
  - **At commit:** report-only, for at most 90 s. The note lists what it did not reach.
  - **In CI:** gremlins on PR-added lines, posted as a report with a neutral conclusion, outside the required checks. Timeouts and inconclusive results are facts about infrastructure and never block.
  - **Opt-in; report by default; block only when pinned** (decided 2026-10-03). A repo that declares no mutation key runs none at commit or merge. Opting in (`mutants-at-commit = true`, `mutants-at-merge = "ci"`) gives a report. There is no recorded product catch, and mutation alone turned 33% of first runs red. A repo pins `[rules] mutation = "block"` to make survivors and not-covered mutants on added lines refuse the merge.
  - **Removed:** the edit-stage mutants and lint jobs (F0b). The brief drops the `mutants prove` quoting and the loop-index rule.
  - **Under `ci = local`:** mutation runs only on a Linux box, because gremlins measures 0% on Windows. Windows reports it as unmeasured, and nightly trunk mutation is the backstop.
- **trellis's own tests (F.f).**
  - Pure kernel, laws and measure tests run under `-race` and `rapid` in under 30 s.
  - Integration tests copy a prebuilt template repo.
  - Process tests sit behind `//go:build proc` and run nightly and in windows-smoke.
  - Adapters are tested only on recorded payloads.

## 7. Rules/laws and config model

- **Laws move unchanged.** `.ratchet/laws/*.toml` stays where it is, along with:
  - fixtures, and presets with drift detection;
  - baselines that only go down, with `--adopt` for a new or changed law;
  - scan views and `earlier` rows;
  - site keys without line numbers;
  - escape comments;
  - the edit-time weight delta, fail-open at edit and fail-closed at commit;
  - a remedy on every finding.
- **One planner:** `laws.Plan{Stage, Base, Files, Overlay}` loads the laws once per process. A `rapid` property test holds the edit plan to a subset of the commit plan, which closes the #968 class (commit refusals the edit check missed).
- **One rule system.** Smells become matcher kinds with fixtures, and suppression is detected in one place.
- **Presets.** New ones: `common/secrets`, `py` (for fanvue), and a fuller `ts`. This repo's own stages (`callsiteGuardStage`, and `tddsplitManifestStage` while forwarders remain) become its own laws and leave consumers' gates.
- **Nested lanes** (S17).
  - The walk skips any directory that holds a `.git` file, and skips `.claude/worktrees/`.
  - Repo start adds that directory to `.git/info/exclude`.
  - The init PR adds it to `.gitignore`, and to the excludes of runner configs that read neither file (vitest, jest, eslint, `tsconfig`).
  - A replay canary checks that a nested lane changes no runner's test count.
- **Config** is one schema in `internal/config`, read through `tomlsubset`.
  - Layers: built-in < user (`<root>/config.toml`, with `[repo."<id>"]` sections) < repo (`trellis.toml`). A per-command flag wins over all three.
  - Plugin `userConfig` is not used: its values reach hook processes only, never git hooks or shims.

| Key | Values (default) | Layer |
|---|---|---|
| `tdd` | `enforce`, `warn`, `off` (`warn` until the A/B decides) | all |
| `isolation` | bool (`true`) | all |
| `ci` | `auto`, `local`, `github` (`auto`) | all |
| `ci.os` | list (`["linux"]`; this repo `["linux", "windows"]`) | repo |
| `mutation` | `off`, `guide`, `block` (`off`: opt-in; an opted-in repo reports, `guide`; `block` is a separate pin) | repo |
| `requires`, `undercover`, `trunk` | semver floor; bool (`false`); branch name (detected) | repo |
| `host.production`, `pin` | bool (`false`); version | user |
| `budgets` | `foreground_s` per runner (measured, else 20), `commit_s = 60`, `merge_s = 300` | repo |
| `[test]`, `[rules]` | `reads`, `slow_tag`; `<rule-id> = "block"`, `"warn"`, `"guide"` or `"off"` | repo |

- **No silent misreads.**
  - A bad key or value is named, and its layer falls back to built-in, never to off. `undercover = true # why` reads as true.
  - The 44 `APHROLLO_*` variables become three: `TRELLIS_OFF`, `TRELLIS_CONFIG` and `TRELLIS_DATA`.
  - `config show` prints each value with its layer, and `set` writes a `config.set` event.
- **Opting in is one PR.** `trellis init` writes:
  - `trellis.toml` and the managed block;
  - `worktree.baseRef = "head"` in `.claude/settings.json`;
  - the ignore and runner-exclude entries;
  - with `--protect`, branch protection (C17). Its required checks are the CI jobs for each declared OS. It never sets "require up to date", and it reports `[skip]` when protection already exists.
- **Repo start** then does only local setup:
  - a repo-local `core.hooksPath`;
  - `merge.ff=false` together with `pull.ff=only`;
  - the exclude entry and the foreign-hook check;
  - any migration, in a lane of its own.
- **Cutover exclusion** (S2).
  - From F0a on, aphrollo exits 0 silently in any repo that holds `trellis.toml`: settings hooks, git hooks and shims alike.
  - `aphrollo.toml` is read as an alias only where aphrollo has gone silent.
  - `trellis doctor` fails while two gates are live in one repo.

## 8. Integration: commit/merge/CI path

**With GitHub** (`ci = github`, or `auto` with a GitHub remote):

1. In the lane, commit (gated). `trellis pr` pushes and opens a ready PR; a draft starts no CI.
2. CI runs on `refs/pull/N/merge`. `trellis merge` reads the check runs through `gh` and stores `ci.verdict{head, tree, os, by, run, mutation}` for each declared OS.
3. If CI is not green yet, the verb returns at once:
   - **Pending:** it names `trellis ci wait <lane> --merge` to run as background Bash. That waits at most 90 min and re-invokes Claude.
   - **Never started:** `auto` falls back to local CI; `github` says "ci unavailable — not failed, no escape; retry, or --ci local".
   - **No ready PR:** "open a ready PR".
4. When CI is green on every declared OS, the verb computes `git merge-tree --write-tree <trunk> <head>`. If that equals CI's tree, it merges. If trunk has moved, it runs the **intersection closure** (C10):
   - **What it re-tests:** only units whose dependency closure holds both a file trunk changed since CI's base and a file the PR changed, plus their `[test] reads` targets. Laws run on the whole merged tree.
   - **Soundness:** it is sound when trunk's tip has its own green verdict. Without one, it widens to every unit trunk's changes reach.
   - **Per language** (S14):
     - Go uses `go list -deps` and cargo uses `cargo metadata`.
     - TypeScript runs `vitest related` within the affected workspace package.
     - Python runs the affected package's whole test tree.
     - A `merge.path` event names the path taken.
   - **Which OS:** the closure runs on the local box's OS. Trunk's push CI judges the other OSes; a red there is a product escape attributed to the pair.
   - **Outcomes:**
     - Green within 5 min: merge.
     - Red: refused, naming the interaction.
     - Over budget: the closure keeps running as a deferred job, and the verb returns `pending: delta closure j17`.
   - **No sync loop:** if trunk moves again, only its own new changes are intersected. Syncs per merge are counted.
5. The merge goes through the API with `sha=<head>` and the method the repo allows. It prefers `merge` (the `--no-ff` shape) and falls back to squash, saying so in one line. A merge commit made locally has no check runs, so a protected trunk refuses its push; that is why the GitHub path never merges locally. The verb calls the same `MergeGate` function as pre-merge-commit.
6. After a fetch, trunk fast-forwards in the primary checkout; post-merge records `lane.merged` and prunes.

**Without GitHub** (`ci = local`, or `auto` with no remote, or with CI that never starts):

1. `trellis ci` (the ported `ghworkflow`) checks out the merged tree in a throwaway worktree, on each box that serves a declared OS, and runs the workflow's `run:` steps.
   - Steps run serially, at low priority, in the sealed environment. Nothing is installed globally, and `trellis ci` refuses to run on a `host.production` box.
   - `uses:` steps are listed and skipped. Mutation runs only on Linux.
   - The verdict is stored per tree and OS.
2. `git merge --no-ff` runs in the primary checkout; this is the one write allowed there. pre-merge-commit needs verdicts for exactly the tree it is about to commit, or runs the closure against the newest tree with a verdict for the same head. That makes the step compare-and-swap by construction.
3. **Divergent trunk** (S13): GitHub is billing-locked but the remote still exists, so local trunk runs ahead of origin.
   - trellis records `trunk.diverged`, and SessionStart says so in one line.
   - Lanes keep branching from local trunk (`baseRef = head`), and pushes queue.
   - Once GitHub is back, `trellis sync` opens one PR from local trunk. CI then judges the combined tree, and it merges like any lane.

**Escapes** (C9):

- **A CI test red on a head whose note says gated green** is `escape{product, stage: ci}`. The unit goes straight to `open(T)`, with T the failing test: the CI red is the reproduction. It closes when T goes green, locally or in CI, so a red that only reproduces on another OS never strands a Windows box.
- **A mutation-only, canary, timeout or flaky red** is `escape{disagreement}`. It counts against its stage and holds nothing. A note that lists `mutation: unmeasured` never turns a mutation red into an escape.
- **On trunk:** a revert, a `regression` issue, a red trunk CI run, or `trellis escape record` is `escape{product, stage: trunk}`.
  - It holds the unit as guidance only.
  - The hold is released by a local red of a reproducing test, by a CI green of the named test on a later head, or by the merge of a lane whose `closes-by` names it.
  - The weekly blame heuristic (a fix on a merge's lines within 7 days) feeds metrics only.
- **A GitHub-side merge outside the verb** is `lane.merged{source: github}`, never an escape.

## 9. Measurement

Every measure is a pure fold in `measure` over four sources: the events, git history, CI check runs and transcript usage. `trellis stats --repo --lane --week` prints them. Nothing leaves the box.

| Measure | Captured from | Drives |
|---|---|---|
| Speed: task start to merged, p50 and p90 | `lane.opened` → `lane.merged` | Merge-budget alarms |
| First-run CI green, by cause (test, mutation, other) and by OS | The first `ci.verdict` per lane | Mutation's level; the F exit (85% or more) |
| Escaped defects per 100 merged PRs | Product escapes only (§8) | Holds; `closes-by` |
| Gate wall time per task | Σ `stage.timing` + `hook.timing` per lane | Budgets, checked by replay |
| Gate tokens per task | Render bytes ÷ 4, plus the managed block, skill and briefs; the turns between a deny or red and its clearing (`transcript_path`) | The §5 caps |
| Wrong blocks, compliance, shadow catches and passes | An override or `tdd off` within 10 min; feedback; a test deleted before merge; the vacuous probe; verdict flips; `shadow` outcomes | Levels (§5) |
| Not tested, by cause; edit → verdict delivered | `run.result`; `run.requested` → delivery | Governor and budgets; the F exit (under 10%) |
| Edits per batch; syncs per merge; closure path | `edit`, `merge.path` | The run trigger; closure soundness |

- **Budgets.** A budget breached at p95 for a week becomes one SessionStart line.
- **Line stop** (C8). It is a report and an alarm for the lead, never a gate on lanes.
  - It is computed over first-parent merges: a merge followed within 7 days by a merge whose diff overlaps its lines.
  - The owner may pause new lanes through config.

## 10. Cut / kept / adopted from Claude Code

**Cut.**
- **Moves out of trellis:**
  - `refactor`, `outline` and `show` (Claude Code's LSP tool covers them);
  - `sqlc`, `dev` and `workspace claim`;
  - the merge queue, OTel and the dashboard.
- **Setup:**
  - `install` and `gate init`;
  - the global settings hooks and the global `core.hooksPath` (at M1);
  - the setup conversation, `trellis.local.toml`, `--local` and `--session`;
  - the background verdict comparison;
  - 41 of the 44 environment variables, and the ad-hoc config readers.
- **State and code:**
  - gate.log, session-keyed state and mutation receipts;
  - the generated forwarders (by M1);
  - the walls duplicated in the shim, and the spent-argv bridge;
  - lanes under `../.worktrees/`.
- **Gate behaviour:**
  - **Edit time:** per-edit runs without coalescing; the edit-stage mutants and lint jobs; the hidden 150 s slot wait.
  - **Mutation:** blocking at commit; blocking in CI by default; `mutants prove` quoting and the loop-index rule.
  - **Merge:** re-running suites on a tree CI already judged; squash as the default; the four prune sweeps.
  - **Other:** this repo's own checks inside consumers' gates; the redundant-suite deny; a line stop that gates lanes.

**Kept**, ported nearly verbatim.
- **Laws:** the laws engine whole (`ratchet`, `lang`, `mask`, `tomlsubset`), and `docs check` as a law.
- **Proof:** fail-first in a stable HEAD checkout, and the edit ledger.
- **Verdicts:** deferral and harvest, with the four-way verdict, "the last real verdict stands" and stale labels; masked detectors.
- **Process safety:** `gitiso`, `gitenv`, `rootseam` and `argvbatch`; the #997 guards and `pidStillOurs`; slots, headroom checks and supersede.
- **Hooks and records:** block-once on Stop, SubagentStop and TaskCompleted (#1111); the post-commit note; recording of merges made outside the verb (#1107).
- **Releases and escapes:** `requires`, versions and the changelog (#1106); escapes with `closes-by`.
- **Tools:** `ghworkflow` (as `trellis ci`), `ciwhy`, `classify-diff`, the statusline and undercover.
- **Conventions:** execute-by-default with `--dry`; plan/apply/`[skip]`; the queue shim's lock.

**Adopted from Claude Code**, each checked against its docs.
- **Plugin packaging:**
  - exec-form `hooks.json` with `${CLAUDE_PLUGIN_ROOT}` and `${CLAUDE_PLUGIN_DATA}`;
  - a skill, and agents with `isolation: worktree` and `skills:` preload;
  - the plugin's `bin/` for the `trellis` wrapper.
- **Worktrees:**
  - `EnterWorktree name=` to make lanes, with `worktree.baseRef = "head"` and `.worktreeinclude`;
  - Claude Code's own isolation checks inside a worktree;
  - its worktree lock as a "do not prune" signal, and its sweep for subagent worktrees.
- **Hook events:** PostToolBatch, SubagentStart, CwdChanged, DirectoryAdded and Setup.
- **Hook signals:**
  - `stop_hook_active` and TaskCompleted exit 2;
  - `async` and `asyncRewake`;
  - `CLAUDE_ENV_FILE` in SessionStart and CwdChanged;
  - `agent_id` and `transcript_path` in payloads;
  - `CLAUDECODE` and `CLAUDE_CODE_SESSION_ID` in the Bash environment.
- **Context and tools:** CLAUDE.md loading into subagents; background Bash and Monitor; `/code-review`; the LSP tool.

**Not adopted.**
- WorktreeCreate and WorktreeRemove: they replace git's own worktree creation.
- `userConfig`: its values reach hooks only.
- Plugin `settings`: only `agent` and `subagentStatusLine` take effect.
- The plugin's `bin/` for native shims: one directory serves both OSes, so a Linux `git` binary beside `git.exe` would break command lookup on Windows.

## 11. Refactor or greenfield

**Recommendation: a hybrid strangler.**
- **Approach:** refactor inside aphrollo-tools, and write the spine and kernel fresh beside the old code.
- **Written fresh:** about 12–15k lines, test-first. The missing structure has nothing to refactor from, and bending session-keyed state into a lane store would carry its defects along.
- **Moved unchanged:** the parts that encode past incidents in 165k test lines.
- **Repo move:** to harryberg1n/trellis at the start of 1b, because the release workflow, the manifest and the pin URLs all name the repo. The owner decides.
- **Not greenfield** because:
  - the consumers cannot stop;
  - replay needs the old binary as its oracle;
  - a rewrite shows no gain for weeks, and the fix-on-fix rate has already reached 39%.

**Rules for every lane:**
- It changes at most 600 production lines in one domain. Tests are not counted, and pure moves are exempt.
- It reaches consumers only in a release tag, so a consumer moves only when its box's tag moves (S1).
- From F5 on, it ships only with release replay green.
- At most 4 lanes are open at once, and at most 2 of them on the spine.
- **The line stop for building trellis itself is relative** (S5):
  - the line is the trailing 2-week median fix-on-fix rate plus 5 points;
  - the baseline is taken the week after F1;
  - work restarts after 3 days below the line.

| # | Lanes | What | Measured, before → after |
|---|---|---|---|
| F0a | 1 | Release channel: `update` and the Linux deploy follow the newest release tag (#1106), not `main`. aphrollo goes silent where `trellis.toml` exists. doctor checks for two live gates | Consumer-visible changes: every merge → per tag |
| F0b | 2 | Hot path: the slot wait comes out of the foreground budget; lint-edit and mutants-edit jobs off; the fingerprint reads `rev-parse --git-path index` (it is 0 in every lane today); the ratchet walk skips `.git` files and `.claude/worktrees/` | infra-failed 46 per 3 days → 0; detached jobs per edit 3 → 1 |
| F1 | 1 | Mutation at the level its data earns (F.e, extended): commit report-only; CI report outside the required checks (an owner action); unmeasured listed in the note; mutation rules out of the brief. Minor version bump | First-run green 46% → about 83% (57 branches since 09-29; mutation-only reds were 33%); commit p95 |
| F0c | 2 | A sharded `test-windows` CI job, required in this repo. Then the merge gate accepts green per-OS CI verdicts when merge-tree equals CI's tree | Merge gate p50 261 s → about 1 s on a match; Windows reds found in CI |
| F3 | 1 | Spikes, committed as fixtures: launcher resolution and its p95 on Windows; off-here and shim pass-through p95; `CLAUDE_ENV_FILE` in PowerShell and in subagent Bash; context delivery at PostToolBatch and SubagentStart; edits per batch in interactive sessions; how often Claude follows an `EnterWorktree name=` deny, with and without the managed block, and from a subagent without isolation; TaskCompleted in an interactive session; CwdChanged, DirectoryAdded, Setup; `asyncRewake`; whether subagent Bash carries an agent id; the Linux recordings | Each item recorded |
| F4 | 2 | Events v1 at the final per-repo path and root from day one (S15): timings, denies, overrides, feedback, first-run CI by cause, escape class, not-tested cause, edits per batch, edit → verdict latency. Plus `trellis stats` and the brief-length check | One baseline week on both boxes |
| F5–F6 | 2 | Release replay per tag (this repo and a fanvue-sized tree, on Windows and Linux, two binaries on one store); F.f test tiers | 0 new hits; own CI `test` p50 4.0 min |
| F7–F12 | 6 | `run`: the package itself (kill-on-close, governor, in-process creation time, `argvbatch` inside, `exec_outside_run`), then call sites package by package | 170 → 0; orphans after a kill; not tested 35–40% → under 10% |
| F13–F17 | 5 | `kernel`: the lane machine; the TDD machine (pending, tested code); the rule table. Then `engine`, built against a store interface (S15), and `trellis why` | Table and `rapid` tests written first |
| F18–F21 | 4 | `git`: one client; one status call per batch; trunk resolved, never hard-coded `"main"` | Git spawns per edit 10+ → at most 1 |
| F22–F23 | 2 | `render`: the line grammar; the caps as golden tests; the `seen` rule (seen only after a delivery recorded as reaching Claude) | Tokens-per-task baseline |
| F24–F27 | 4 | `store`: checkpoints, lock and fold versions; verdicts; retention. gate.log's readers move to the events, then gate.log stops | Lost updates → 0; state size capped |
| F28–F29 | 2 | Shadow and holdout: red→green and run decisions recorded beside aphrollo's live hooks for a week | Agreement; would-be wrong blocks |
| F30–F31 | 2 | Runs coalesced on the existing trigger; PostToolBatch only if the data supports it; lint moves to the run; Stop and task checks read lane state | Not tested; gate time; tokens |
| F32–F34 | 3 | `config`: schema, layers, aliases; 44 → 3 environment variables | Misread keys → 0 |
| F35–F36 | 2 | `laws.Plan`; smells as matcher kinds | Commit refusals the edit check missed → 0 |
| A1–A3 | 3 | Red→green at PreToolUse in warn, plus the A/B (at least 30 lanes per arm). The rule table goes live: walls scoped to Claude, `secrets`, the shim cut down to its lock plus walls | Escapes and friction per arm; wall implementations 2 → 1 |
| A4–A5 | 2 | Escape split; holds as guidance; `measure.Decide` in propose mode | Disagreements leave the escape count |
| A6–A8 | 3 | Intersection closure with per-language paths; pending merges; the allowed merge method with `sha`; `ci wait` | Merge p95 at most 5 min; syncs per merge |
| B1–B2 | 2 | Plugin: native launcher, fetch, sha256 check, keep 3, `.exe` layout, file-level swap, silent stub; the release workflow on hosted runners | Off-here p95 at most 50 ms; SessionStart at most 200 ms |
| B3–B5 | 3 | `trellis init` (`--protect`, baseRef, managed block, excludes); repo start with a repo-local hooksPath; CwdChanged, DirectoryAdded; briefs, skill, agents | A new box set up in one step; brief tokens |
| B6–B8 | 3 | Deny then EnterWorktree; `lane.opened` with the dependency warm-up; actors; lifecycle prune; `why`, `feedback`, `allow`; `TRELLIS_OFF`, `eject`, `update --to` with `.bak` | Follow rate; primary-write overrides |
| B9–B12 | 4 | Cut over one repo at a time, behind `trellis.toml` and a pin, replay first: this repo, go-telegram, fanvue, then borld through the borld session | Each repo's measures |
| C1–C4 | 4 | `trellis ci` per tree and OS under the merge verb; local `--no-ff`; Linux mutation under `ci = local`; divergent trunk and `trellis sync` | A merge completes with GitHub off |
| M1 | 1 | The last aphrollo release switches boxes over; an `aphrollo` alias for one release; the forwarder generator deleted | – |

**F exits:**
- the three measures recorded for a week on both boxes;
- first-run CI green at 85% or more, with mutation counted separately;
- not tested under 10%, with pending runs excluded;
- 0 exec sites outside `run`;
- `forwarder_count` falling;
- every adapter tested on recorded payloads.

**Consumers never break.** Until B9 they run aphrollo at a release tag. Each cutover is per repo, pinned, replayed first, and reversible with `trellis eject`.

## 12. Risks and open questions; effort

**Risks**
- **Context delivery.** Context delivered at PostToolBatch and SubagentStart is documented but has not been recorded. Until F3 records it:
  - a verdict counts as seen only after a later PreToolUse or UserPromptSubmit has delivered it again (C3);
  - briefs ride on the first PreToolUse.
- **Whether Claude follows the EnterWorktree deny** (R4).
  - The managed block gives the instruction that the tool's own description requires.
  - If Claude follows it less than 80% of the time, the deny repeats and is counted as a wrong-block risk.
  - The fallback names `git worktree add .claude/worktrees/<slug>` followed by `EnterWorktree path=`.
- **Windows launcher resolution and latency.** F3 settles both. The fallback is the shell form, for SessionStart only.
- **Soundness of the delta closure.**
  - A law flags any test that opens a path outside its unit without a `reads` row.
  - Trunk's push CI covers the gap between the closure and the API merge.
  - A trunk tip without its own verdict widens the closure.
- **Windows CI cost.** Hosted Windows minutes bill at a higher rate. Under a billing lock, the Windows box supplies the verdict through `trellis ci`.
- **Holdout volume.** At about 50 denies per two weeks, most rules need months to reach 10 shadowed fires. Until then, levels stay as set and Decide only proposes.
- **Two binaries on one box.** This happens with a deferred job, a session that started before an update, or aphrollo running beside trellis. Fold versions plus the replay test cover it.
- **The strangler stalls halfway.** The zero-target laws show what is left. No more than 2 spine lanes run at once, and the relative line stop applies.
- **Uninstall deletes `${CLAUDE_PLUGIN_DATA}`.** Git hooks then exit 0 and commits read as ungated. State survives in the root.

**Open questions**, each with what it blocks
- **The `tdd` default:** warn, unless enforce wins the A/B on escaped defects at no more than +10% friction. Blocks A1.
- **This repo's mutation level** (decided 2026-10-03): mutation is opt-in, report by default, block only when pinned. This repo is opted in at the report level; `mutants-verdict` is no longer a required check, and the merge gate waits for it only where block is pinned.
- **Release cadence:** a tag per green replay, or a daily tag? Blocks F0a.
- **Doc-only writes under `isolation = true`:** they are denied as drawn; `classify-diff` gives a docs lane a one-minute CI. Blocks B6.
- **`host.production`:** blocks #1102.
- **The repo move at the start of 1b:** blocks B1.
- **The F timebox (about 6 weeks) and the lane caps:** blocks F's start.

**Effort: 66 lanes, about 12 weeks** at the caps.
- **Phase 0:** #1102 and #1103 finish beside F0, on disjoint files.
- **F:** 41 lanes, about 6 weeks. F0 and F1 land in weeks 1–2, so the measures move early.
- **1a:** 8 lanes, about 2 weeks.
- **1b:** 12 lanes, about 3 weeks.
- **Phase 3 and the move:** 5 lanes, about 1.5 weeks.

## 13. Fidelity to the target session flow

The flow is `docs/trellis-flow/session_flow.py` on main. Where roadmap PR #1114 replaces it, the roadmap wins.

| Flow step or requirement | Realised by | Status: deviation and reason |
|---|---|---|
| Session starts; SessionStart and Setup; the first response waits; under 200 ms idle; clear or compact skips the checks | `hook.SessionStart`, `hook.Setup` | Realised; timed in F3 |
| Getting the binary: pin or override; on the box?; fetch with sha256 (a mismatch is a security error); keep 3 under a lock; `trellis update`'s result in the tool output | Native launcher, `cli update` | Realised. At SessionStart the fetch runs only when the pin is missing, because a download cannot fit in 200 ms. The `.exe` layout and file-level swap follow #366 and #338 |
| A kept binary? Not ready (silent hooks, ungated commits), or the newest kept with one warning | The launcher's stub as `bin/trellis.exe` | Realised |
| Newer binary: compare in the background, stage the update | – | Cut (roadmap): a newer pin applies at the next start; per-tag replay is the check |
| Check the box | SessionStart, from a cached record | Realised. There is no merge queue to name (cut). An old install is named with its removal command, since questions are cut |
| Setup record; Anyone to answer?; FIRST START; Ask: trellis here? | `config`, `trellis init` | Cut (roadmap, R3): defaults plus `config set`; a repo is in once `trellis.toml` is committed |
| Check the settings; trellis on here?; inject the rules; `--local` and `--session` | `config`, the off-here exit, the managed block, the brief | Changed: static rules sit in the managed block, which also reaches subagents; dynamic state goes in the brief; `--local` and `--session` are cut |
| UserPromptSubmit; CwdChanged and DirectoryAdded run repo start | Hooks | Realised. The last two cannot speak to Claude, so their line rides the next hook |
| Git repo?; requires met? | Repo start, `compat` | Realised |
| Set up the repo (schema, detection, repo hooks, `merge.ff`, branch protection, migrations, `[skip]`); does the git gate run here? | Repo start, `trellis init --protect` | Realised. A repo-local `core.hooksPath` overrides the global one; `pull.ff=only` is added; protection is an init flag, not a question; migrations land in a lane |
| Tell Claude what finished | Harvest, `seen` | Realised; a deferred red wakes Claude through `asyncRewake` after F3 |
| Guardrail hit: denied, with the safe command named | Rule table, `secrets` law | Realised. Secrets detection is new; the walls act only on Claude |
| Writes into the repo; on main with isolation, a worktree is made and its path named for EnterWorktree | PreToolUse wall, Claude Code | Changed: trellis denies and names `EnterWorktree name=`; Claude Code makes the worktree (`baseRef = head`, `.worktreeinclude`) and then enforces isolation itself |
| RED TO GREEN: off?, code edit?, open here?, tested code?, enforce? | TDD machine | Realised, with `warn` as the default. It adds `pending(T)` and defines tested code (§3) |
| Test of an open red edited before its green: code edits close | TDD machine | Changed (friction): the unit stays open unless T then passes with no code change, or T is removed |
| Breaks a deny law; guidance | `laws.Plan(edit)`, `render` | Realised. An overrun is deferred to the commit, visibly |
| SubagentStart brief; SubagentStop and Stop block once; WorktreeCreate and WorktreeRemove | Hooks | Realised. The brief also rides the first PreToolUse until delivery is recorded; the worktree hooks are unused |
| TaskCompleted keeps the task open | Exit 2 | Realised (per the docs); the interactive recording is in F3 |
| Commit gate, including mutation of added lines | pre-commit, commit-msg | Changed: mutation is report-only (F.e); the ledger proof counts only when the index is the tested tree |
| post-commit note; push gate | post-commit, pre-push | Realised. The note lists what went unmeasured; pre-push checks first-parent history |
| PR verb; merge verb with ci = local?; verdict per head | `integrate` | Realised; the verdict is recorded per head, merged tree and OS |
| GitHub CI pending under 90 min: wait | `trellis ci wait`, in the background | Changed (platform): a foreground tool call stops at 10 min |
| Run started?, ready PR?, ci = auto? → local CI | `integrate.merge` | Realised verbatim |
| CI red but green locally on this head → escape; code edits close | `integrate`, kernel | Changed: a CI test red opens T; disagreements hold nothing; trunk escapes hold as guidance |
| Merge gate: a recorded verdict; suites and laws on the merged tree; CI mutation green; a conflicted merge gated at its commit | pre-merge-commit, `MergeGate` | Changed (merge in under 5 min): per-OS CI verdicts are reused, plus the intersection closure; mutation is guide by default; an API merge while GitHub is up |
| post-merge: record every merge, remove lanes no session is in, say "lane closed" | post-merge, `actors` | Realised: no actor for 30 min, a clean tree, no Claude Code lock. A GitHub-side merge is `lane.merged{github}`, not an escape |
| PostToolUse: record the edit | `hook.PostToolUse` | Realised; it also requests the coalesced run |
| PostToolBatch: tests once per batch | The run trigger | Changed: runs coalesce per lane and unit; the batch hook becomes the trigger only at 1.5 or more edits per batch (F4) |
| SessionEnd within 1.5 s | `hook.SessionEnd` | Changed: it drops this session's actors, since state is per lane |
| R1–R14 | §3–§9 | Realised. R1: walls are scoped to Claude. R4: rests on F3's follow rate. R5: timed in F3. R6: one log per repo, outside the plugin. R11: adds a required Windows verdict. R14: through the holdout |

## Appendix: Rejected critiques

1. **C1, "report-only removes the local signal."** Commit mutation still prints its survivors, so Claude sees what CI will see; only the refusal goes. The rest of C1 is accepted.
2. **C2, "a deferred run on a moved tree is discarded."** Its verdict is still delivered, labelled stale; only the cache write is withheld (#813). `pending(T)`, the per-runner budget and the warm-up are accepted.
3. **C3, "skip runs on code-only batches; run when the next call is not an edit."**
   - The next call is known only at the next PreToolUse.
   - Skipping code-only batches withholds the green that closes a red.
   - Coalescing saves the same runs without delaying any verdict.
4. **C5, "a 3 s wall budget that runs `git worktree add`."** Superseded: Claude Code makes the worktree (`EnterWorktree name=`), so PreToolUse does no git work. The lane also gets `.worktreeinclude`, the lock and the sweep for free.
5. **C6, applied to secrets.** The walls about where and how work happens act only on Claude. A secret, though, blocks every author; a human's override is `--no-verify`, which leaves the commit visibly ungated.
6. **C10, "over budget, merge anyway as unverified-delta."** That merges an untested interaction, against "what it merges does not break". Instead, the closure keeps running as a deferred job and the merge returns pending.
7. **S1, "every behaviour-changing lane behind a default-off switch."** That is ceremony: the release tag is the switch. Flags remain only where both arms are measured (F28–F29, A1).
8. **S3's fallback, "packages with `_windows.go` files and their importers."** #1112 failed in OS-agnostic path code, so the Windows verdict covers the full suite.
9. **S12, "collect coverage in every batch."** Partly rejected.
   - Go adds `-coverprofile` to the run it already makes.
   - Other runners use the existing per-function test map, or a static reference to the symbol.
   - Per-run coverage for cargo does not fit a 20 s budget.

## Changes from the draft

- **Executive summary and plan order.** An executive summary was added. The plan now opens with F0 (release channel, hot-path fixes, Windows CI with merge reuse) and F1, so the cheap wins land in weeks 1–2 (C11).
- **Mutation** (C1).
  - It starts at guide: CI reports outside the required checks, and a repo can pin it to block.
  - F1's target is restated from the data: 46% → about 83%, with mutation counted separately.
- **Release discipline** (S1, S2).
  - Boxes follow tags, never main.
  - aphrollo goes silent where `trellis.toml` exists.
  - A repo-local `core.hooksPath` overrides the global one.
  - doctor checks for two live gates.
- **Windows verdict** (S3). A required Windows verdict per OS in `ci.os`; verdicts are stored per OS.
- **Storage and binaries** (C4, S7, S8).
  - The state root moved out of the plugin.
  - Binaries stay in `${CLAUDE_PLUGIN_DATA}`, named `trellis.exe` on both OSes and swapped one file at a time.
  - A native launcher doubles as the silent stub.
- **Versioning** (S9). Checkpoints carry fold versions, and an older binary never writes one above its own.
- **Commit proof** (S6). The ledger proof counts only when the index is the tested tree.
- **TDD machine** (C2, C9, S12).
  - It gains `pending(T)` and a definition of tested code.
  - A CI test red opens T.
  - Holds are guidance at every level.
- **Runs** (C2, C3, C5, S11).
  - Runs coalesce on the existing trigger, and PostToolBatch takes over only if the data supports it.
  - A verdict counts as seen only after it is delivered.
  - The foreground budget is per runner, and a deferred run counts as pending, not not-tested.
  - Edit → verdict latency is measured.
  - Dependencies and the test build are warmed when a lane opens.
- **Lanes** (C5). A write to trunk leads to a Claude Code `EnterWorktree name=` lane with `baseRef = head`. Claude Code's isolation checks are adopted, and the prune respects its worktree lock.
- **Walls** (C6).
  - Walls are scoped to `CLAUDECODE=1`.
  - Git hooks and shims take the actor from `CLAUDE_CODE_SESSION_ID`, which retires the spent-argv bridge.
  - A merge without a green per-OS verdict moved to block-always.
- **Catches** (C7, S10).
  - A catch is now measured by a 10% holdout; live blocks count only as compliance.
  - Decide only proposes for the first 8 weeks, and pinned rules are never shadowed.
- **Line stop** (C8, S5). It is a report computed on first-parent history, and the rule for building trellis itself is relative.
- **Merging** (C10, S13, S14).
  - The intersection closure has per-language paths and returns pending instead of looping on syncs.
  - Divergent-trunk mode and `trellis sync` were added.
- **Briefs** (C12). The managed block is kept, at 15 lines or fewer, because it reaches every subagent. SubagentStart delivery was added to F3, with the first PreToolUse as the fallback.
- **Tokens** (C13).
  - The token measure counts the managed block, the skill and the briefs.
  - Guidance comes once per unit per lane.
  - `last_real` is seeded from the lane's base.
- **Smaller rule fixes.**
  - `secrets` is a law with fixtures and an escape comment (C14).
  - The edit-time law check has a deadline and a visible deferral (C15).
  - Shims go on PATH only in opted-in repos, with a pass-through budget (C16).
- **Protection and push** (C17). `init --protect`, the allowed merge method with `sha`, and a first-parent check in pre-push.
- **Generated code and exec sites** (S4, S16). Forwarders retire per package under a law, with the generator deleted at M1. `exec_outside_run` replaces the three inventory tests.
- **Store order** (S15). F4 writes the final event schema at the final path, and the engine is built against a store interface.
- **Nested lanes** (S17). Runner excludes, plus a replay canary.
- **Estimate** (C11, S5). Re-estimated at 66 lanes and about 12 weeks, with F at about 6. The lane cap counts production lines only.
- **Platform facts.** Re-checked in the Claude Code docs: the exec form, the 600 s default timeout, the 10,000-character context cap, the `CLAUDE_ENV_FILE` events, `asyncRewake`, plugin data deletion, plugin agent limits, the isolation checks, `baseRef` and `.worktreeinclude`. These replace the claude-platform map, whose 10 s limit and "hooks are synchronous" were wrong.