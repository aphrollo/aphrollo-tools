# aphrollo-tools

First-party dev-env tooling for the agent platform: one Go binary,
**`aphrollo`** (`/usr/local/bin/aphrollo`), on the standard library plus two
pinned modules — `golang.org/x/sys` for the Windows process and job-object
syscalls, and `pgregory.net/rapid` for the property tests. Moves deterministic
developer work *out of the agent token stream into code* — the agent spends
tokens on judgment, not mechanical read→grep→multi-edit→verify loops. `README.md`
is the full user-facing command reference; this file is the **developer**
context (conventions, contract, deploy).

Module `github.com/aphrollo/aphrollo-tools`, go 1.26.6. Single binary —
`go build -o aphrollo ./cmd/aphrollo`.

## Design contract (every tool obeys it)

- **Lossless** — never silently transform, truncate, or filter output.
- **Deterministic** — same inputs, same bytes out (sorted, stable).
- **Visible** — two mutation models, by verb family:
  - `refactor`/`gate` mutations are **dry-run by default**; pass `--apply` to write.
  - `workspace` verbs (`merge`/`prune`/`commit`/`push`/`ship`/… ) **execute by
    default**; pass `--dry` to preview the plan and stop. (The old `--apply` opt-in
    on these is **legacy/no-op** — you now opt OUT with `--dry`, not in with `--apply`.)
  Each step is **idempotent** — already-done work reports `[skip]`, never redone,
  so re-running on a half-built state finishes the job without clobbering it.
  Fail loud with a fix suggestion rather than guessing.

`aphrollo dev` is a service control plane, so it also **executes immediately**
like `systemctl` (no dry-run, no `--dry`). The split: `refactor`/`gate` defer and
preview; `workspace` mutates source but acts now; `dev` controls running units
and acts now.

## Command surface (see README for usage)

- `refactor rename-symbol` / `find`, `outline <file>`, `show <file> <symbol>`
  — LSP-backed (one client, one registry entry per language; columns are UTF-16).
- `workspace` — worktree lifecycle (`create`/`claim`/`unclaim`/`list`/`remove`/
  `prune`) + git verbs (`commit`/`push`/`pr`/`ship`/`submit`/`merge`).
- `dev` — `up`/`down`/`restart`/`status`/`logs` (replaces the retired
  `aphrollo-dev` bash wrapper).
- `guardrail pretooluse` — Claude PreToolUse policy hook (block long fg waits, warn noisy cmds).
- `ratchet` — the law engine: `check` judges a repo against its declared
  `.ratchet/laws/*.toml` (`--adopt <law>` is the one path that creates or
  raises a baseline row, gated on the law being new or changed since HEAD),
  `test` proves each law against its fixtures, `init`/`presets` copy the
  embedded law library (`internal/ratchet/presets/{common,rust,go}`) into a
  repo via `extends`/`[params]`.
- `gate` — the TDD + law gates (`pretooluse`/`posttooluse`/`userpromptsubmit`/`sessionend`/
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
  `regen --scoped` regenerates and keeps only the hunks that derive from a
  query the working tree changed, backing out the rest as pre-existing drift.
  Gating (clean vs reported-only per config) comes from a committed
  `.aphrollo-sqlc.yaml` sidecar.
- `install` / `check` / `issue` / `update` / `version` — box setup (session
  hooks + git-hook shims in one run), read-only tree judgment (ratchet + docs +
  sqlc + doctor + the app trio), open an issue against the repo's remote,
  rebuild from `origin/main` and swap it in, and print the build stamp.

## Layout

```
cmd/aphrollo/        entry point (one-liner over internal/cli)
internal/cli/        arg parsing + subcommand dispatch (testable Run)
internal/refactor/   detect lang → spawn server → rename/refs/outline/show
internal/lsp/        LSP types + JSON-RPC stdio client
internal/diff/       deterministic unified-diff renderer
internal/guardrail/  PreToolUse policy
internal/ratchet/    Law engine: .ratchet/laws/*.toml schema, matchers, baselines, fixtures
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
internal/docs/       doc-reference guard: extract path citations, resolve, report misses
internal/workspace/  worktree lifecycle + git verbs
internal/dev/        dev-tier control plane (systemd)
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

- Don't break the mutation contracts: `refactor`/`gate` are **dry-run by default**
  (`--apply` to write); `workspace` verbs **execute by default** (`--dry` to
  preview). `dev` acts now with no dry-run at all. Don't re-invert `workspace`
  back to `--apply`-opt-in — that opt-in is legacy.
- Don't re-port what was deliberately dropped: the SessionStart full-suite
  baseline, or `/gate allow-main` — the fail-first gate covers the ground
  without the flakiness. (Mutation testing came back, but on the terms that
  answered the old FP/non-determinism objection: diff-scoped, judged against a
  reason-carrying accept-list, and measured on the CI runner rather than the
  box that is trying to edit code. See `aphrollo.toml` and the `mutants` job.)
- Don't add a sudo wrapper or wildcard grant — the narrow exact-match systemctl
  fence is the whole security story.
- Don't duplicate README usage here — this file is dev context only.

<!-- aphrollo:begin -->
## Working with the aphrollo gate

- **`cargo` and `git` resolve to the queue shim** (`which cargo` prints a path under
  `C:/Users/olive/bin/cargo-queue`); the user PATH and the shell profiles put it first, so a session never exports
  PATH by hand. A run through the shim QUEUES visibly behind another build instead of
  hanging on a silent lock; if `which` prints the raw toolchain, the profile is broken: say so.
- **The hooks run the tests, not you.** After every Edit/Write, PostToolUse prints
  exactly ONE `gate:` line. Read it; never re-run a suite it just ran. Iterate with
  `cargo check -p <crate> --tests`, which runs nothing.
- **What the line means:** `green (N passed)` · `red-missing-impl` (a clean RED) · `red` ·
  `red-bogus` (broken test setup, not a real RED) · `TIMEOUT` / `SKIPPED` / `QUEUED-SKIPPED`
  (**inconclusive — the code was NOT tested**) · `BUILDING (deferred)` (the build outran the
  budget and continues; its result arrives at the next hook). The only sanctioned manual runs: a mutation proof, a deliberate soak, or ONE targeted `-p <crate> <filter>` after a TIMEOUT. Wanting the run's TEXT is not one of them: `aphrollo gate stats` answers what the verdict WAS, `aphrollo gate output` prints what that run actually PRINTED — assertion lines and all, unfiltered.
- **Commit gate, cheapest first:** staged-baseline guard → ratchet laws → docs check →
  suppression check → per root: cargo sequential (fmt→guards→clippy→check→fail-first);
  a Go root also runs vet/lint first. It proves the staged test RED and STOPS — the
  mechanical suite runs at the MERGE; a commit prints a `NOT RUN` line naming each touched crate it did not test, so an untested crate is never a silent absence.
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
  `main` takes merges and nothing else: the Edit/Write/Bash/PowerShell hooks are a GUARDRAIL, the
  git shim (refusing `checkout -b`/`switch -c`, a move off main, a non-merge commit) is the WALL.
  Work in a lane: `git worktree add -b lane/<name> <parent>/.worktrees/<repo>/<name> main`; override
  with `aphrollo gate allow primary` (works from inside a turn; `aphrollo gate revoke primary` restores it).
- **A merge is measured, not certified:** the pre-merge gate runs this lane's mutation measurement in the foreground and refuses an unaccepted survivor by name; `aphrollo gate mutants run` measures THIS checkout the same way before you merge.
- **Housekeeping:** `aphrollo gate stats --since 7d` (pipeline health) · `aphrollo gate gc` (dry run; `--apply` reclaims stale build dirs).

_This block is written by `aphrollo install`. Edit the template in aphrollo, not
the block — the next init overwrites whatever is between the markers._
<!-- aphrollo:end -->
