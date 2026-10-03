# aphrollo-tools

First-party dev-env tooling for the agent platform: one Go binary,
**`aphrollo`** (`/usr/local/bin/aphrollo`), on the standard library plus two
pinned modules — `golang.org/x/sys` for the Windows process and job-object
syscalls, and `pgregory.net/rapid` for the property tests. Moves deterministic
developer work *out of the agent token stream into code* — the agent spends
tokens on judgment, not mechanical read→grep→multi-edit→verify loops. The
user-facing reference is `aphrollo <verb> --help`; `README.md` maps the verbs.
This file is the **developer** context (conventions, contract, deploy).

Module `github.com/aphrollo/aphrollo-tools`, go 1.26.6. Single binary —
`go build -o aphrollo ./cmd/aphrollo`.

## Design contract (every tool obeys it)

- **Lossless** — never silently transform, truncate, or filter output.
- **Deterministic** — same inputs, same bytes out (sorted, stable).
- **Visible** — one mutation model: every mutating verb (`workspace`,
  `refactor`, `sqlc regen`, `gate gc`, `gate probe discard`, `gate split-commit`,
  `gate install`, `update`, `ratchet init`, `ratchet check`, `gate mutants hold`,
  `issue`, `gate feedback`, `gate escape record`) **executes by default**; pass
  `--dry` to print the plan and stop (`aphrollo install` and `gate init` take no
  `--dry` yet). `--apply` is accepted as a legacy no-op on the verbs that once
  needed it, and prints a one-line notice on stderr; `ratchet check --no-tighten`
  is an alias of `--dry`. Flags are honoured before or after positionals (one
  shared splitter, `parseFlagsAnywhere`), and an unknown `--flag` is refused,
  never ignored.
  Each step is **idempotent** — already-done work reports `[skip]`, never redone,
  so re-running on a half-built state finishes the job without clobbering it.
  Fail loud with a fix suggestion rather than guessing.

`aphrollo dev` is a service control plane, so it also **executes immediately**
like `systemctl` (no dry-run, no `--dry`).

## Command surface (see `aphrollo <verb> --help` for usage)

- `refactor rename-symbol` / `find`, `outline <file>`, `show <file> <symbol>`
  — LSP-backed (one client, one registry entry per language; columns are UTF-16).
- `workspace` — worktree lifecycle (`create`/`claim`/`unclaim`/`list`/`remove`/
  `prune`) + git verbs (`commit`/`push`/`pr`/`ship`/`submit`/`merge`).
- `dev` — `up`/`down`/`restart`/`status`/`logs` (replaces the retired
  `aphrollo-dev` bash wrapper).
- `guardrail pretooluse` — Claude PreToolUse policy hook (block long fg waits, warn noisy cmds). Also evaluated in-process by `gate pretooluse`, the verb every installed PreToolUse hook calls; a rule is wired by living in `internal/guardrail`, never by a hook of its own.
- `ratchet` — the law engine: `check` judges a repo against its declared
  `.ratchet/laws/*.toml` (`--adopt <law>` is the one path that creates or
  raises a baseline row, gated on the law being new or changed since HEAD; a
  baseline stamped below the `# scan-view: <n>` its files now read under is migrated
  by the tightening `check` itself, and the commit guard admits it only when it
  equals its recomputation), `test` proves each law, and each language row, against
  its fixtures, `init`/`presets` copy the
  embedded law library (`internal/ratchet/presets/{common,rust,go}`) into a
  repo via `extends`/`[params]`.
- `gate` — the TDD + law gates (`pretooluse`/`posttooluse`/`userpromptsubmit`/`sessionend`/
  `stop`/`subagentstop`/`taskcompleted`/
  `precommit`/`premerge`/`prepush`) + `allow <wall>`/`revoke <wall>` (waive or
  restore a wall, e.g. `allow primary`) + `gate init` (wires session hooks +
  global git gate) + `classify-diff` (read-only: the class CI's `changes` job
  sizes a run by, from the same per-file rules as the commit gate's fast
  paths); `tdd` is a silent alias for one release.
  Ported from the retired `claude-code-tdd` Node hooks (this binary IS the gate now).
- `docs check` — doc-reference guard: every repo path a tracked `*.md` cites must
  resolve (relative to the citing file, then repo root); exit 1 on any miss. Bar
  is zero — no baseline, no allowlist, no suppression. The rule itself is the
  ratchet engine's `doc-path-resolves` matcher (a repo's own
  `doc_reference_exists` law, else the built-in `common/doc_reference_exists`
  preset) — this subcommand is CLI surface only.
- `sqlc` — `check` regenerates every discovered sqlc config into a temp dir and
  diffs it against the committed tree, failing CI on drift in a gated config;
  `regen --scoped` (writes; `--dry` previews) regenerates and keeps only the hunks that derive from a
  query the working tree changed, backing out the rest as pre-existing drift.
  Gating (clean vs reported-only per config) comes from a committed
  `.aphrollo-sqlc.yaml` sidecar.
- `ci why` — read-only answer to "why is this run red?": resolves a PR's,
  a run id's or main's latest pipeline run through `gh` and prints each failed
  job with its failing tests, mutation survivors or infrastructure cause.
- `ci run` — the one CI entry point: runs the repo's own `pull_request` GitHub
  workflow(s) on this HEAD merged into trunk, in a throwaway worktree (`run:` steps
  under bash, `uses:` steps listed and skipped, first matrix combination only, no
  mutation). A run installs only into a scratch directory of its own (a python venv
  first on PATH, per-run npm, go, cargo, pipx, uv and rustup prefixes and caches, printed
  at the start of the run), never into the host's global toolchains, and lists as skipped, by
  name, a step that would (`sudo`, a system package manager, `pip install --user`): such a
  run is inconclusive (neither green nor red, no green stored for its tree). Jobs run one at a time in needs
  order, every step below normal priority (nice and ionice, BELOW_NORMAL_PRIORITY_CLASS on
  Windows); `--ci-jobs N` or `ci-jobs` in `aphrollo.toml` runs N at once, `--ci-timeout` or
  `ci-timeout` sets the per-step limit, and a step that reaches it is named. `workspace merge` runs it when `ci = local` (or `auto` and GitHub's
  jobs never started); the verdict is a gate.log line keyed by the merge result's
  tree, and a stored green for the same tree is reused.
- `stats` — read-only: the pipeline measures folded from the repo's event log
  (`internal/measure`; `aphrollo gate stats` reads gate.log instead), and with
  `--briefs` the token length of the managed block, the tdd skill and each
  agent brief against the section-5 caps.
- `why <seq>` — read-only: replays one deny or run result of the event log with
  its rule's counts, and the kernel's level, section and holdout arm when the
  rule table holds the rule (`internal/measure`, `internal/kernel`).
- `install` / `config` / `check` / `issue` / `update` / `version` — box setup
  (session hooks + git-hook shims in one run, and the opt-in feature table once
  per repo), that table on demand with the repo's values, read-only tree
  judgment (ratchet + docs + sqlc + doctor + the app trio), open an issue
  against the repo's remote, rebuild from `origin/main` and swap it in, and
  print the semantic version and the build stamp (`version check` holds a PR to
  the version rule). The table's rows (`internal/tdd/install/features.go`)
  are also the README's opt-in configuration rows, kept verbatim by a test.

## Layout

```
cmd/aphrollo/        entry point (one-liner over internal/cli)
internal/cli/        arg parsing + subcommand dispatch (testable Run)
internal/refactor/   detect lang → spawn server → rename/refs/outline/show
internal/lsp/        LSP types + JSON-RPC stdio client
internal/diff/       deterministic unified-diff renderer
internal/guardrail/  PreToolUse policy
internal/measure/    pure folds of the v1 event log into the pipeline measures, and the brief-length check
internal/ratchet/    Law engine: .ratchet/laws/*.toml schema, matchers, baselines, fixtures
internal/lang/       language table: one TOML row per language (comments, strings, suppression directives, test patterns), embedded defaults, per-repo .ratchet/languages rows
internal/tomlsubset/ the one TOML subset reader: language rows and ratchet laws both parse through it
internal/mask/       the one lexer, reading a language row: blanks strings and comments, keeps length and newlines
internal/tdd/        TDD + law gates: policy engine, edit smells, anti-cheat, fail-first, install
internal/tdd/shell/  bash write-target parsing (L0 of the tdd split)
internal/tdd/gitx/   git plumbing, trunk and merge-tip resolution (L0)
internal/tdd/core/   gate log, session state, runner and verdict types, file classes (L1)
internal/tdd/lock/   build locks and slots, lock dirs, machine load, budget floor (L2)
internal/tdd/suite/  suite runners, output classification, verdicts, vacuous-run and scope judges (L3)
internal/tdd/smell/  edit smells, policies and test-quality notes (L4)
internal/tdd/lawgate/ ratchet edit and commit gates, baseline guard (L4)
internal/tdd/mutation/ mutants measurement, verdicts and holds (L4)
internal/tdd/failfirst/ fail-first proofs and the edit ledger (L4)
internal/tdd/escape/ escapes, auto-escape, issues, feedback, demotion, stats (L4)
internal/tdd/postedit/ the edit hooks: post-edit, deferred phases, bash hooks, walls (L5)
internal/tdd/precommit/ the commit gate's stages and fast paths (L6)
internal/tdd/merge/  commit-message gate, pre-merge PR gate, post-merge and prune (L7)
internal/tdd/install/ install, init, agents, skills, CLAUDE.md block, doctor, shims, git gate (L7)
internal/tdd/gc/     gc sweeps (L7)
internal/argvbatch/  command-line budgets (cmd.exe 8 191, CreateProcess 32 767): split a path or package list into runs; a test lists every spread exec call site and what bounds it
internal/docs/       doc-reference guard: extract path citations, resolve, report misses
internal/workspace/  worktree lifecycle + git verbs
internal/depinstall/ dependency-install rule shared by workspace create and the PR merge gate; node_modules links
internal/gitenv/     GIT_* scrubbing, the sealed git environment for processes that run tests, maintenance-off settings
internal/gitiso/     TestMain isolation every package's tests run under: no GIT_* variables, temp dirs walled off from every repository, a temp home and git config; the hostile-environment probe that proves it
internal/rootseam/   per-worktree-root tables: the gate's stderr and the probes a test states, carried by root so the tests that use them run in parallel
internal/dev/        dev-tier control plane (systemd)
internal/ciwhy/      ci why: resolve a pipeline run through gh, summarise its failed jobs
internal/ghworkflow/  a YAML-subset reader for .github/workflows and the runner behind ci run
internal/sqlc/       sqlc drift guard: config discovery, regen-into-temp, check, scoped-by-symbol regen
```

## Conventions

- **Strict TDD** (this repo's own gates apply via global `core.hooksPath`). Per-
  language refactor work has a real **e2e test against the actual LSP server**,
  auto-skipped when the server isn't installed — keep that pattern when adding a
  language; don't fake the server.
- **Privilege model is load-bearing.** This binary carries **NO wildcard sudo**.
  The only privileged atom is `dev`'s exact-match `systemctl restart` on fixed
  unit names whitelisted to `{api,rlndx,infra}` — argv is always constructed,
  never caller input. `workspace claim` borrows that one atom; it does NOT get a
  grant of its own. Never widen this to a wildcard.
- **Adding a gate/detector** = a new `policy` entry in a slice, not new control
  flow. A rule about the CONSUMING repo's source text is not a detector at all:
  it is a law in that repo's `.ratchet/laws/*.toml`, run by `internal/ratchet`. Detectors run against a **masked** copy (smell detectors mask strings +
  comments; suppression detectors mask strings, keep comments) — a token only in
  a string never blocks. Keep edit-time blocks near-zero-FP; heavy checks
  (fail-first) live at commit/push where a false block only costs a re-run.
- **An issue closes on the merge, never before.** Every fix commit carries its
  `Closes #<n>` trailer and GitHub fires it when the lane lands on `main`. Do
  not close an issue by hand for work that is committed but unmerged: the fix is
  not in `main` yet, and if the merge is refused or the change reworked, the
  issue is already closed and nobody looks again. The general rule it comes
  from: never record a status further along than the work actually is. A
  `deferred`, `TIMEOUT` or `SKIPPED` verdict is not a green — the code was not
  tested. A merge needs the proof the gate actually demands, not one waved
  through. A new hit is admitted by the law's escape comment, never by editing
  a baseline.
- **Attribution: undercover, always.** Commit messages, PR bodies and issues say
  what changed and nothing about how they were written: no `Co-Authored-By`
  trailer, no `🤖 Generated with Claude Code` footer. `aphrollo.toml` sets
  `undercover = true` and the commit-msg gate rejects the trailer; a builder
  brief must say so up front, or its first commit is refused.

## Deploy

Go service — ships `/usr/local/bin/aphrollo` **on merge** via its OWN pipeline
(`deploy/deploy-prod.sh` on the github-runner), NOT deploy-infra (infra #186
retired the root build task). aphrollo-infra no longer force-installs it.

## Don't

- Don't break the mutation contract: every mutating verb **executes by default**
  and `--dry` previews. `dev` acts now with no dry-run at all. Don't add a verb
  that previews by default or needs `--apply`, and don't parse a verb's flags
  with a bare `fs.Parse` when it takes positionals: use `parseFlagsAnywhere`
  (or `mutFlags.parse`), or a flag after a positional is silently dropped.
- Don't re-port what was deliberately dropped: the SessionStart full-suite
  baseline, or `/gate allow-main` — the fail-first gate covers the ground
  without the flakiness. (Mutation testing came back, but on the terms that
  answered the old FP/non-determinism objection: diff-scoped, judged against a
  reason-carrying accept-list, and measured on the CI runner rather than the
  box that is trying to edit code. See `aphrollo.toml` and the `mutants` job.)
- Don't add a sudo wrapper or wildcard grant — the narrow exact-match systemctl
  fence is the whole security story.
- Don't duplicate README usage here — this file is dev context only.

## Working in this repo

- A new file under `internal/tdd/` needs a `<file> <package>` row in the
  `[files]` section of `tools/tddsplit/manifest.txt`, inserted at its sorted
  place (by file, then package). The same file name may have a row in two
  packages; the section carries no counts to update.
- A new language is one file, `internal/lang/languages/<name>.toml`, with its
  fixtures under `.ratchet/fixtures/languages/<name>/`; `aphrollo ratchet test`
  proves it and no Go changes. When the row lexes files that were read by the
  default row before it, its `view` is one above the highest in the table: a
  baseline over those files is then judged by the old reading until a tightening
  check migrates it; the scan cache is keyed by the embedded rows, so it drops
  what it read under the old table by itself. A change to the lexing of an
  existing row moves its `view` up the same way, and the row's previous lexing
  stays as a row that owns no extension (`php-v3.toml`), named by the row's
  `earlier`: a baseline at the old view is judged by that reading, not by the
  default row, until the migration.
- Never hand-edit a generated `export.go`, `deps_*.go` or `api_*.go` — they are
  `tools/tddsplit` output. Regenerate in place with
  `go run ./tools/tddsplit -regen`: no clean-tree requirement, no commit —
  it reads the working tree as it sits (tracked, staged or edited), writes
  only the generated files, and refuses a generated file whose content a
  regenerate from the committed tree cannot explain (a hand edit).
- Run `go test ./tools/tddsplit -run TestCommittedTree_GeneratedFilesMatchTheGenerator`
  before pushing.
- `mutants-at-merge = "ci"` (`aphrollo.toml`): CI's `mutants-verdict` check,
  sharded across hosted runners, refuses an unaccepted survivor, every timeout,
  and a not-covered or inconclusive mutant on a line the PR adds. A mutant its
  own package's tests miss is refused at once; only the packages in
  `mutants-integration-packages` settle against their importers' tests.
- Prefer giving concurrent lanes disjoint files. Git merges overlapping edits,
  but two lanes rewriting the same function cost a conflict round.
- A green open PR takes no further pushes; it merges as is and a follow-up goes
  in a new lane. A red PR gets its fix pushed to the same branch.

<!-- aphrollo:begin -->
## Working with the aphrollo gate

- **Where `aphrollo install` put the queue shims on the agent's PATH, `git` resolves to them** (`aphrollo gate doctor` says whether it did):
  a run through a shim QUEUES visibly behind another build instead of hanging on a silent lock, and a session never exports PATH by hand.
- **The hooks run the tests, not you.** After every Edit/Write, PostToolUse prints
  exactly ONE `gate:` line for the edit, then one `gate: deferred` line per earlier job of the session, in any tree,
  that finished since, naming its own tree and command. Read them; never re-run a suite they ran. Iterate with `go vet ./...`, which runs nothing.
- **A Bash script is fine for multi-file edits.** Each source file it changed gets what an Edit gets (gofmt, deny laws, smell checks, edit ledger, the suite once per root) on the same `gate:` line;
  the one difference is that a deny law cannot refuse a Bash write before it happens: the hit is named right after the write, with file and law, and refused at commit.
- **Before writing or changing code, read the `tdd` skill** at `~/.claude/skills/tdd/SKILL.md` (under `$CLAUDE_CONFIG_DIR` when set; `aphrollo install` writes it).
- **What the line means:** `green (N passed)` · `red-missing-impl` (a clean RED) · `red` ·
  `red-bogus` (broken test setup, not a real RED) · `TIMEOUT` / `SKIPPED` / `QUEUED-SKIPPED`
  (**inconclusive — the code was NOT tested**) · `BUILDING (deferred)` (the build outran the
  budget and continues; its result arrives at the next hook, or wait in the foreground with `aphrollo gate status --wait <tree>`, the tree the line names). The only sanctioned manual runs: a mutation proof, a deliberate soak, or ONE targeted run of the failing test after a TIMEOUT. Wanting the run's TEXT is not one of them: `aphrollo gate stats` answers what the verdict WAS, `aphrollo gate output` prints what that run actually PRINTED — assertion lines and all, unfiltered.
- **Commit gate, cheapest first:** staged-baseline guard → ratchet laws → docs check →
  suppression check → per root, a Go root runs vet→lint→fail-first. It proves the staged test RED at HEAD, then GREEN with the change, and STOPS — the
  mechanical suite runs at the MERGE; a commit prints a `NOT RUN` line naming each touched package it did not test, so an untested package is never a silent absence.
- **Laws are data:** `.ratchet/laws/*.toml` (scope + one matcher + severity), with baselines in
  the sibling `baselines` dir that only ever go DOWN. `aphrollo ratchet check` judges the tree
  and tightens; `aphrollo ratchet test` proves each law against its fixtures. A new hit is
  admitted by the law's escape comment, NEVER by editing a baseline — a raised one is rejected.
- **An open point is an ISSUE, never a markdown follow-up:** `aphrollo issue "<title>"
  --label <theme>` opens one against this repo's remote, labelled from the list it declares
  (`issue-labels`), and prints the URL as its only output — never park one in a document.
- **Escapes close the loop.** A red after a local green (CI, merge gate, survivor mutant, a
  playtest defect a check could have caught) is recorded with `aphrollo gate escape record
  <reason>`, and closed only by a stage or law named in the fix, never by a sentence in this
  file. The count only goes down; `gate stats` prints it weekly at session start.
- **The primary checkout is merge-only.** Once a repo has any linked worktree, the checkout holding
  `main` takes merges and nothing else: the Edit/Write/Bash/PowerShell hooks are a GUARDRAIL; the git queue shim,
  where it is on the agent's PATH, is the WALL (refusing `checkout -b`/`switch -c`, a move off main, a non-merge commit).
  Work in a lane: `git worktree add -b lane/<name> <parent>/.worktrees/<repo>/<name> main`; override with `aphrollo gate allow primary` (works from inside a turn; `aphrollo gate revoke primary` restores it).
  A lane refreshes this committed block with `aphrollo install --managed-block-only --repo <lane>`, never a full install: that writes git hooks into the git dir every worktree shares.
- **A merge is checked, not measured:** this repo declares no `mutants-at-merge`, so the merge gate runs the mechanical suite and NO mutation measurement; `aphrollo gate mutants run` measures THIS checkout by hand.
- **A commit is measured, and reported:** this repo declares `mutants-at-commit = true`, so the commit gate mutates the lines the commit adds, runs each mutant against the tests of its own function and names each survivor without refusing the commit; a box with no memory headroom, or a run past its wall-clock budget, prints `NOT MEASURED` for what it did not reach; `aphrollo gate mutants commit` runs it by hand.
- **Mutation findings are guidance** in this repo: a survivor is reported, never refused, so no `gate mutants prove` line is owed. A survivor on a line you add is still a test worth writing.
- **Orchestrating:** follow-ups on a lane (fix round, base merge, re-measure, red CI) resume its builder with only the delta; a fresh builder is for a new issue. A reviewer did not build the lane and re-reviews its own findings; the coordinator never edits; a brief carries only what the agent lacks.
- **Housekeeping:** `aphrollo gate stats --since 7d` (pipeline health) · `aphrollo gate gc` (reclaims stale build dirs; `--dry` lists them).
- **Commit messages** say what the change does and nothing about how it was
  written: no attribution trailers, tool names, or model names. The `commit-msg`
  hook rejects one and quotes the offending line.

_This block is written by `aphrollo install`: edit the template in aphrollo, never the block, which the next install overwrites._
<!-- aphrollo:end -->
