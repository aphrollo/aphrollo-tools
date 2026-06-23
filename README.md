# aphrollo-tools

First-party dev-env tooling for the Aphrollo agent platform, shipped as a single
zero-dependency Go binary: **`aphrollo`**. The goal is to move deterministic
developer work *out of the agent token stream into code* — the agent spends
tokens on judgment, not on mechanical read→grep→multi-edit→verify loops.

Design contract for every tool here:

- **Lossless** — never silently transform, truncate, or filter output.
- **Deterministic** — same inputs, same bytes out (sorted, stable).
- **Visible + idempotent** — every mutating verb is safe to re-run and prints a
  stateful, parseable receipt of what it did; fail loud with a fix suggestion
  rather than guessing. The `workspace` verbs **execute by default** (`--dry`
  previews); `refactor`/`tdd` mutations are **dry-run by default** (`--apply`
  executes). `dev` always acts now.

## Install

```sh
go build -o aphrollo ./cmd/aphrollo
# put it on PATH (coder sessions inherit PATH)
```

The refactor commands shell out to per-language LSP servers; install the ones
you need:

| Language | Extensions | Server (must be on PATH) | e2e-validated |
|---|---|---|---|
| Go | `.go` | `gopls` | ✅ |
| Rust | `.rs` | `rust-analyzer` | ✅ |
| Python | `.py` | `pyright-langserver` | ✅ |
| TS/JS | `.ts .tsx .js .jsx` | `typescript-language-server` | ✅ |

The architecture is language-agnostic (one LSP client, one registry entry per
language). Each language has a real end-to-end test against its actual server
(skipped automatically when the server isn't installed). Slow-loading servers
(notably rust-analyzer, which waits on `cargo metadata`) are handled by a
bounded retry on transient "still loading" responses.

## Usage

### Rename a symbol (and all references) across the project

```sh
# dry-run: prints a unified diff of every file that would change
aphrollo refactor rename-symbol --file internal/foo/bar.go --line 42 --symbol OldName --new-name NewName

# apply to disk
aphrollo refactor rename-symbol --file ... --line 42 --symbol OldName --new-name NewName --apply
```

Locate the target with either `--symbol NAME` (resolved to the right column on
that line, UTF-16-correct — paste it straight from a grep hit) or an explicit
`--col` (1-based UTF-16).

### Find every reference to a symbol

```sh
aphrollo refactor find-references --file internal/foo/bar.go --line 42 --symbol Name
# internal/foo/bar.go:42:6: func Name() string {
# internal/foo/baz.go:9:9:  return Name()
```

Exit codes: `0` ok, `1` runtime error, `2` usage error.

### Outline a file's symbols (without reading it)

```sh
aphrollo outline internal/refactor/session.go
# L14-17  struct Session
#   L15-15  field conn
# L39-53  method (*Session).Initialize
# L133-140 method (*Session).DocumentSymbol
```

One line per symbol — `L<start>-<end>\t<kind> <name>` — nested by containment,
1-based inclusive line numbers. Backed by the language server's
`textDocument/documentSymbol`, so it reflects what the compiler sees, not a
regex. Lets an agent map a file's shape for a fraction of the tokens a full read
costs.

### Show one symbol's source (without reading the whole file)

```sh
aphrollo show internal/refactor/session.go DocumentSymbol
# func (s *Session) DocumentSymbol(ctx context.Context, path string) (...) {
#   ...
# }
```

Prints just the named symbol's source span. Methods are reachable by their bare
name (`DocumentSymbol`) or their receiver-qualified name
(`(*Session).DocumentSymbol`); an exact match always wins over a bare-name match.

### The core coder flow: create · commit · push · submit

The autonomous-coder loop is four verbs. Every one **executes by default** (pass
`--dry` to preview), is **idempotent** (safe to re-run — a re-driven turn never
double-pushes or double-opens a PR), and prints a **stateful, parseable receipt**
of its full post-state so the calling LLM needs no follow-up git/gh call:

```sh
aphrollo workspace create <repo> <branch>   # from the main clone: worktree + deps
cd <worktree>
# … edit / test …
aphrollo workspace commit -m "feat: …"      # stage -A + commit (TDD-gated)
aphrollo workspace push                     # push + ensure a DRAFT PR exists
aphrollo workspace submit -m "<summary>"    # CI-green → flip draft to in-review
```

`commit`, `push`, and `submit` are **cwd-only** — they act on the worktree you
stand in and take no positional `<repo> <branch>` (that targeting lives on the
operator verbs `merge`/`cleanup`/`status`). `create` keeps its required
`<repo> <branch>` (it runs from the main clone).

### Create a worktree to work in (one shot)

Getting an isolated worktree to run tests or build a branch in otherwise costs
the same mechanical sequence every time — mark the (often cross-owner) repo
git-safe, `git worktree add`, mark the worktree git-safe, install dependencies
(git worktrees do **not** share the main tree's gitignored `node_modules`). The
agent paid for that dance in tool calls and tokens on every branch.

`aphrollo workspace create` (the renamed `prepare`; `prepare` stays a hidden
alias for one release) folds it into one deterministic command that **executes
by default** — pass `--dry` to preview:

```sh
# --dry: print exactly what would happen, change nothing
aphrollo workspace create ~/spaces/aphrollo/aphrollo-web feat/kanban --dry
# workspace create: aphrollo-web @ feat/kanban (new branch)
#   worktree: ~/spaces/aphrollo/.worktrees/aphrollo-web/feat-kanban
#
# steps (dry-run — run without --dry to execute the [run] steps):
#   1. [run] git config --global --add safe.directory .../aphrollo-web
#   2. [run] git -C .../aphrollo-web worktree add -b feat/kanban .../feat-kanban
#   3. [run] git config --global --add safe.directory .../feat-kanban
#   4. [run] pnpm install   (cwd .../feat-kanban)

# execute it (the default — no flag needed)
aphrollo workspace create ~/spaces/aphrollo/aphrollo-web feat/kanban
```

Every step is **idempotent** — an already-marked `safe.directory`, an existing
worktree, or a present `node_modules` is reported `[skip]` rather than redone, so
re-running `create` on a half-built workspace finishes the job without
clobbering it. The dependency step is auto-detected from the worktree root:

| Marker (priority order) | Install command | Skip when present |
|---|---|---|
| `pnpm-lock.yaml` | `pnpm install` | `node_modules/` |
| `yarn.lock` | `yarn install` | `node_modules/` |
| `package-lock.json` | `npm ci` | `node_modules/` |
| `package.json` | `npm install` | `node_modules/` |
| `go.mod` | `go mod download` | — |

The worktree lands at `<repo-parent>/.worktrees/<repo-name>/<branch-slug>`, so a
created worktree can later be `claim`ed onto the dev tier. Override the base dir
with `--into`, skip steps with
`--no-install` / `--no-safe-dir`, or force a re-install with `--reinstall`.

#### Addressing a repo by name

Every `<repo>`-arg verb (`create`, `merge`, `cleanup`, `unclaim`, `list`,
`remove`, `prune`, `claim` — and `pr`/`ship`) accepts a **bare repo name** —
`aphrollo-web`, not just a path — and resolves it the same from **anywhere under
the spaces tree**: from inside the clone, from one of its worktrees, or from a
sibling clone under the same owner. (The core coder verbs commit/push/submit are
cwd-only and take no `<repo>` arg.) A bare name is resolved in order: a path that
is itself a git repo wins as-is; else the clone you are standing in (when its
name matches); else a unique `~/spaces/*/<name>` git repo. A genuine
absolute/relative path still resolves exactly as before — so
`create aphrollo-web feat/x` run *from inside* `~/spaces/aphrollo/aphrollo-web`
no longer double-joins into `…/aphrollo-web/aphrollo-web`. Override the spaces
root with `APHROLLO_SPACES_ROOT`.

A name matching **more than one** clone (e.g. `~/spaces/a/shared` and
`~/spaces/b/shared`) is an **ambiguity error** that lists the candidates rather
than silently picking one — pass an absolute path to disambiguate. An
unresolvable name fails with an actionable message naming what was tried and the
fixes, not a bare "is not a git repository".

Companion read/cleanup subcommands:

```sh
aphrollo workspace list aphrollo-web                             # bare name, from anywhere under the spaces tree
aphrollo workspace list ~/spaces/aphrollo/aphrollo-web           # or an explicit path
aphrollo workspace remove ~/spaces/aphrollo/aphrollo-web feat/kanban
```

Exit codes: `0` ok, `1` runtime error, `2` usage error.

### Put a prepared worktree on the dev tier (claim)

`create` gets you a ready worktree; `claim` makes it the one the **dev tier**
serves, so the branch is viewable at rlndx (or driven by dev-api):

```sh
# dry-run: shows the steps it would run
aphrollo workspace claim ~/spaces/aphrollo/aphrollo-web feat/kanban
# workspace claim: …/.worktrees/aphrollo-web/feat-kanban -> dev-rlndx
#   1. [run] repoint …/.devclaim/web -> …/feat-kanban
#   2. [run] restart dev-rlndx (aphrollo dev restart rlndx)

aphrollo workspace claim ~/spaces/aphrollo/aphrollo-web feat/kanban
```

The orchestration runs **unprivileged, in this binary**: resolve the worktree
from `<repo> <branch>` (symmetric with `create` — no `.worktrees/…` path to
paste), `pnpm install` if the tree was never created, and repoint the
`.devclaim/<repo>` symlink the dev units follow (the dir is `aphrollo-dev`
group-writable, so a group member repoints it with no sudo). The **only**
privileged atom is restarting the dev unit, delegated to the in-binary
[`dev`](#dev-tier-control-plane-aphrollo-dev) control plane (`dev.Restart`),
whose exact-match `sudo systemctl restart` is the one fenced step (and that
restart also clears the rlndx vite optimizer cache for the freshly-claimed
tree).

This subcommand deliberately does **not** carry a sudo grant of its own — the
privilege stays the narrow exact-match systemctl grant in `dev`. The privileged
restart only fires on execute (not `--dry`); a missing worktree points you at `create`
rather than half-claiming, and a re-claim whose symlink already points at the
tree is reported `[skip]`.

The dev service is derived from the repo (`web → rlndx`, `api → api`); override
with `--svc`. `--into` matches a non-default `create --into`. Env overrides
(for tests): `APHROLLO_DEV_BIN` (restart fence path), `APHROLLO_DEVCLAIM_DIR`
(symlink dir), `APHROLLO_DEV_SUDO=0` (drop the `sudo` prefix; root drops it
automatically).

**Dev-tier reconciliation.** A claim repoints the dev units at a worktree that
may have drifted from the dev tier's state, so claim reconciles two things
before/around the restart:

- **api: `goose up` on the dev DB, before the restart.** A claimed api clone can
  carry migrations the isolated dev DB hasn't applied; without this the dev-api
  boots against the old schema and the handlers for new columns/tables 500 while
  older endpoints 200. claim runs `goose -dir <wt>/migrations postgres <dev-dsn>
  up` *before* the restart so the api comes up clean. The DSN defaults to the
  dev pg on `:5432` (`APHROLLO_DEV_DB_URL` to override), goose is resolved from
  `APHROLLO_GOOSE_BIN` → `$PATH` → the operator's go-install path, and the whole
  step is suppressed with `--no-migrate` or skipped when the clone has no
  `migrations/`. Forward-only against the isolated dev pg — it never touches prod.
- **rlndx: a build-before-claim advisory.** The dev-rlndx unit runs as `debian`
  and generates `.svelte-kit`/`.vite`/paraglide on first serve; if the coder
  hasn't built/tested the worktree first, those land debian-owned and EACCES the
  coder's tooling. claim prints a one-line nudge when the worktree looks unbuilt
  (no `.svelte-kit`). The root-cause fix is `UMask=0002` on the dev units
  (aphrollo-infra); this advisory is the guardrail until that deploys.

### Return the dev tier to the main clone (unclaim)

`unclaim` is the inverse of `claim`: it repoints `.devclaim/<key>` back at the
repo's **main clone** and restarts the dev unit, so the tier stops serving a
worktree. Same privilege model as `claim` — the symlink repoint is unprivileged,
the restart is the one fenced `dev.Restart` atom — and the same execute-by-default/`--dry`
contract.

```sh
aphrollo workspace unclaim                       # cwd-aware (run from inside the worktree)
aphrollo workspace unclaim ~/spaces/aphrollo/aphrollo-web feat/kanban
# unclaimed: dev-rlndx now serves ~/spaces/aphrollo/aphrollo-web
```

### Coder git verbs — commit / push / submit

These fold the mechanical git/`gh` dance into one command that emits a
**stateful, parseable receipt** (sha + ahead + delta + gate; ahead + PR#/url +
CI; the in-review handoff), so a coder lands a change without spending a tool
call each on `git add`, `git commit`, `git push`, `gh pr create`, `gh pr checks`,
and `gh pr ready` — and never needs a follow-up call to confirm what landed.
They are **cwd-only** — they act on the worktree you stand in and take no
positional args. They **execute by default**; pass `--dry` to preview.

```sh
# commit: stage (-A) + commit, honoring the TDD pre-commit gate
aphrollo workspace commit -m "feat: kanban drag-and-drop"
# committed a1b2c3d "feat: kanban drag-and-drop"
#   branch feat/kanban (3 ahead of origin/main)
#   delta 3 files changed, 42 insertions(+), 7 deletions(-)
#   gate TDD pass

# push: git push -u origin HEAD AND ensure a draft PR exists (open or reuse)
aphrollo workspace push
# pushed feat/kanban -> origin (2 commit(s))
#   https://github.com/aphrollo/aphrollo-web/tree/feat/kanban
# pr #321 draft [opened] https://github.com/aphrollo/aphrollo-web/pull/321
# ci pending

# submit: push (idempotent) → CI green → flip draft to in-review + set body
aphrollo workspace submit -m "Kanban drag-and-drop. Closes #200."
# submitted PR #321  draft -> in review
#   pushed in sync
#   ci green
#   handoff in_progress -> review
```

- **commit** stages `git add -A` by default (`--staged-only` to commit the index
  as-is) and runs the [TDD pre-commit gate](#tdd-gates-aphrollo-tdd); `--no-verify`
  is the documented escape for the gate's known false-positives. A clean tree is a
  reported no-op, not an error.
- **push** sets the upstream on a first push, reports the ahead-count + branch
  URL, and **folds the draft-PR open**: it ensures a draft PR exists — opening
  one when absent, **reusing** it when present (idempotent, never a duplicate) —
  and reports the PR number/url + the current CI state. `--force-with-lease` for a
  rebased branch.
- **submit** is the **CI-guarded handoff** that moves a card `in_progress →
  review`. It pushes (idempotent), reads the branch PR's CI **inside the verb**
  (the only `gh` check-state read, so the caller needs no extra call), and **only
  on green** flips the draft PR to in-review and sets the PR body to `-m`'s
  summary. On **red** it prints `blocked: CI red (<k> failing), NOT marked ready`
  and exits non-zero; on **pending** it prints `held: CI pending, NOT marked
  ready yet` and exits non-zero — both **re-callable** until CI goes green.
  (`submit` is the renamed, CI-guarded `ready`; `ready` stays a hidden alias.)

> `pr` and `ship` still exist as operator escapes (they take an explicit
> `<repo> <branch>` and also default to the cwd worktree), but the coder flow is
> `push` (which folds `pr`) then `submit` — not the discrete `pr`/`ship`.

### Verify — the typecheck/lint the commit gate misses

The TDD pre-commit gate runs the mechanical test suite (plus the anti-cheat and
fail-first checks), but **not** typecheck or lint. So a type regression
(`svelte-check`) or a lint failure sails past `commit`/`ship` and only turns up in
CI. `verify` closes that gap: it resolves the **affected app** and runs that app's
`{test, typecheck, lint}` trio. It is verification only — it never commits,
pushes, or mutates source. Like the other verbs it addresses the cwd's worktree
(or `<repo> <branch>`) and is dry-run by default.

```sh
# from inside aphrollo-web/apps/rlndx (or with rlndx files changed on the branch)
aphrollo workspace verify
# workspace verify: aphrollo-web @ feat/kanban  (worktree …/aphrollo-web)
#   app rlndx (apps/rlndx)
#     1. test      npx vitest run
#     2. typecheck npx svelte-check --tsconfig ./tsconfig.json
#     3. lint      npx eslint --no-error-on-unmatched-pattern src
#
# run again without --dry to execute (stops at the first failure).

aphrollo workspace verify        # runs test -> typecheck -> lint in order
```

- **dry-run by default** lists the exact ordered commands it would run, per app,
  and exits 0 without running them; running without `--dry` executes them in order, **stops at
  the first failure**, and surfaces that tool's own output.
- **App resolution** is table-driven (start: rlndx). The affected app is scoped
  from the branch's changed paths; when nothing changed resolves one, it falls
  back to the app the cwd sits in — it never runs the whole monorepo's every-app
  matrix unasked. Adding another app is a table entry, not new branching.
- The **test** command is reused from the [TDD runner detection](#tdd-gates-aphrollo-tdd)
  (so the two never drift); only the per-app typecheck and lint commands are
  table data. rlndx resolves to `vitest run` + `svelte-check` + `eslint`.

### Close the loop — merge / cleanup

After review, `merge` lands the branch's PR and `cleanup` tears down the local
worktree, so a coder owns the change end-to-end without dropping to raw `gh` and
`git worktree`:

```sh
aphrollo workspace merge         # gh pr merge --squash --delete-branch
# merged PR #321 (squash): https://github.com/aphrollo/aphrollo-web/pull/321
#   deleted branch feat/kanban
#   next: aphrollo workspace cleanup feat/kanban

aphrollo workspace cleanup feat/kanban   # git worktree remove + prune
# removed worktree …/.worktrees/aphrollo-web/feat-kanban
```

- **merge** resolves the branch's open PR (reusing the `pr` gh seam) and merges it,
  **honoring GitHub's gates** — gh refuses a non-mergeable or red-CI PR, and `merge`
  never passes `--admin`, so it cannot force past a failing check. `--squash`
  (default) / `--merge` / `--rebase`; `--keep-branch` to skip the branch delete.
  It deliberately does **not** touch the local worktree — that is `cleanup`'s job.
- **cleanup** folds `git worktree remove` + `prune` into one post-merge call. It
  takes `[repo] <branch>` (repo defaults to the cwd's main clone) and **refuses to
  remove the worktree you are standing in** — it points you at the main clone
  rather than yanking your own cwd out from under you. `--force` removes a tree
  with local changes.

> Merge stays a deliberate step: in the hub-and-spoke flow it is gated on the
> operator's "ship" + green CI, so a coder runs `merge` on instruction, not
> reflexively. The verb just makes the mechanical step one lossless call.

### Clean up stale worktrees (prune)

`prune` drops the admin records of worktrees whose directories are gone
(`git worktree prune`). git's own `--dry-run` does the preview, so it maps onto
the execute-by-default/`--dry` contract — and reports each stale entry by path:

```sh
aphrollo workspace prune                 # dry-run: "would prune N stale worktree(s)"
aphrollo workspace prune         # "pruned N stale worktree(s)"
```

### Dev-tier control plane (`aphrollo dev`)

Start/stop/restart the `aphrollo-dev` systemd stack and read its status/logs.
This replaces the retired `aphrollo-dev` bash wrapper — its tooling now lives in
this binary.

```sh
aphrollo dev up                # start the whole dev tier
aphrollo dev down [--all]      # stop api+rlndx (--all also stops infra)
aphrollo dev restart rlndx     # restart one of: api | rlndx | infra
aphrollo dev status            # unit status (unprivileged)
aphrollo dev logs [rlndx] [-n 200]
```

Unlike the `workspace` commands (which mutate source/worktrees and default to
dry-run), `dev` is a service control plane and **executes immediately**, like
`systemctl` itself. A restart of `rlndx` first clears the claimed tree's stale
vite optimizer cache (via the `.devclaim/web` symlink `workspace claim`
repoints) so it's a clean reload.

**Privilege model** — there is no wrapper script, and this binary carries **no
wildcard sudo grant**. The privileged surface is the narrowest possible:

| Verb | Privilege |
|---|---|
| `status` | none — `systemctl status` is readable by any user |
| `logs` | none — the dev users are in the `systemd-journal` group |
| `up` / `down` / `restart` | exact-match `systemctl` sudoers grants with **fixed unit names**, no wildcards |

The command builds exactly those argv — the service token is whitelisted to
`{api,rlndx,infra}` and unit names are always constructed, never caller input —
so sudo can never be steered onto a unit outside the dev tier. Env overrides
(for tests): `APHROLLO_SYSTEMCTL`, `APHROLLO_JOURNALCTL`, `APHROLLO_SPACES`,
`APHROLLO_DEV_SUDO=0`.

> The bash wrapper's `worktree add/list/remove` is subsumed by
> `aphrollo workspace create/list/remove`; `claim` by `aphrollo workspace claim`.

### Guardrail — PreToolUse policy hook (coder/devops sessions)

`aphrollo guardrail pretooluse` is a [Claude Code PreToolUse
hook](https://docs.claude.com/en/docs/claude-code/hooks): it reads the hook JSON
on stdin, inspects Bash commands, and applies a thin lossless policy.

```sh
echo '{"tool_name":"Bash","tool_input":{"command":"sleep 600"}}' \
  | aphrollo guardrail pretooluse   # exit 2, blocks with a fix suggestion
```

Policy (v1):

| Check | Outcome |
|---|---|
| Foreground `sleep`/`wait` ≥ 2s | **block** (exit 2) — suggests `run_in_background`+poll or a bounded poll across turns (matches Claude Code's own Bash tool; sub-2s pacing allowed) |
| Foreground watch/follow command (`gh … --watch`, `tail -f`, `journalctl -f`, `watch …`) | **block** (exit 2) — it never returns on its own; suggests `until <cond>; do sleep 1; done` or `run_in_background`+poll |
| Noisy command missing its quiet form (`pytest`/`cargo`/`npm`/`pip`) | **warn** (exit 0, advisory) — suggests the quiet flag; output is never truncated |
| Anything already piped/redirected, or non-Bash tools | **allow** (silent) |

Design notes:

- This is a **PreToolUse hook, not a dispatch-side check** — by design. The
  agents dispatch validator only sees the profile + workspace roots; individual
  Bash commands exist only *inside* the session turn, where a hook can see them.
- Intended wiring: agentsd injects a `hooks` block into the coder/devops
  `--settings` payload pointing at `aphrollo guardrail pretooluse`. `--settings`
  loads independently of `--setting-sources ""`, so the hook fires even though
  operator settings/hooks are otherwise disabled. (That agents change + putting
  the binary on the coder PATH are separate follow-ups.)
- **Pager-off** is handled separately as a coder shell-env default
  (`PAGER=cat`, `GIT_PAGER=cat`) via infra — cheaper and more reliable than a
  per-command hook.
- Long blocking waits are **not** moved onto an agents async queue: no such
  background-execution primitive exists (the send queue is editorial only). The
  lossless answer is to block and point at poll/background patterns.

### TDD gates (`aphrollo tdd`)

Autonomous test-driven-development enforcement, ported from the retired
`claude-code-tdd` Node hooks. Gates across the edit→commit→push lifecycle, each
near-zero false-positive (a false block wedges the agent, so the heavy checks
live where being wrong only costs a re-run):

| Subcommand | Wiring | What it does |
|---|---|---|
| `tdd pretooluse` | Claude PreToolUse hook (stdin) | Blocks (exit 2) a **test-file** edit introducing an oracle smell — real-time sleep, tautological self-comparison, focused marker (`.only`/`fit`), or a disabled test (`.skip`/`xit`/`t.Skip`/`@pytest.mark.skip`). **Warns** (test or source) on a suppression that silences a quality gate (`//nolint`, `@ts-ignore`, `# type: ignore`, coverage-ignore). |
| `tdd posttooluse` | Claude PostToolUse hook (stdin) | Runs the edited file's related tests; surfaces a RED summary. **Silent unless RED.** |
| `tdd userpromptsubmit` | Claude UserPromptSubmit hook (stdin) | Intercepts `/tdd [status\|off\|on\|reset]` — the per-session enforcement escape hatch. On any other prompt, re-injects the last RED outcome for the cwd's project so the gate survives context compaction. **Silent unless RED.** |
| `tdd sessionend` | Claude SessionEnd hook (stdin) | Deletes the per-session state file so the state dir doesn't accumulate. |
| `tdd precommit` | git `pre-commit` | Blocks a newly-**added** suppression (anti-cheat). Then **fail-first**: a commit adding both tests and source must have tests that fail without the source. Then the suite must pass. |
| `tdd prepush` | git `pre-push` | **No-op** (mechanical-only mode). The tdd gate is solely mechanical now; adversarial review is owned by the separate reviewer agent, not this binary. Kept only so a `pre-push` shim lingering from before the change exits cleanly — it **never blocks**. |

`/tdd off` is the escape hatch for spikes and non-TDD work; `/tdd on` re-enables.
The SessionStart baseline and `/tdd allow-main` from the Node original are
deliberately **not** ported — a full suite on every session start costs more than
the one first-edit false-RED it avoids, and there is no main-branch edit gate
here to toggle.

The gates share a small **policy engine**: each detector is a `policy` value in
a slice, grouped by the integrity it protects. *Oracle smells* (the test can't
fail) are near-zero-FP and block at every phase. *Suppressions* (a gate is being
silenced) have legitimate reviewed uses, so they only **warn at edit** and
**block at commit** — and the commit scan inspects only newly-added lines, so a
pre-existing suppression never blocks an unrelated change. Adding a gate is a new
entry in a slice, not new control flow.

Every detector runs against a masked copy of the source. Smell detectors mask
strings **and** comments (so a smell named in prose never trips); suppression
detectors mask strings but **keep comments** (the directives live in comments).
Either way a token mentioned only in a string never blocks — the original's
biggest false-positive class.

### sqlc drift guard (`aphrollo sqlc`)

Keeps sqlc-generated Go in sync with its `queries/*.sql` source **without** the
diff pollution that a naive `sqlc generate` produces. The pollution has a single
root cause worth internalising:

> **The whole-schema `models.go` gotcha.** sqlc emits `models.go` from the
> **entire** `migrations/` schema, so *any* unrelated migration — a new column on
> another table, a brand-new table — rewrites `models.go` even when your query is
> untouched. Run a plain `sqlc generate` to land one column and you get a diff
> carrying a backlog of unrelated drift (e.g. `CrmTicket*` structs,
> `AiEventLog.SrcOff`, an `int64 → *int64` param flip), some of which forces
> out-of-scope wrapper edits. These two commands separate *your* hunks from that
> drift.

```sh
# CI gate (run on main): regenerate every config into a temp dir, diff against the
# committed tree, exit non-zero on drift in a GATED config.
aphrollo sqlc check --repo ~/spaces/aphrollo/aphrollo-api

# ✗ sqlc-ai.yaml (gated): DRIFT — committed output differs from a clean regen
#     internal/store/postgres/aigen/models.go:  + SrcOff *int64 …  + type CrmTicketType …

# Land one query's change: regenerate, apply ONLY the hunks deriving from a query
# you changed (vs origin/main), and report the rest as pre-existing drift.
aphrollo sqlc regen sqlc-ai.yaml --scoped              # dry-run: show in-scope hunks + drift
aphrollo sqlc regen sqlc-ai.yaml --scoped --apply      # write the in-scope hunks only
```

`regen --scoped` classifies each generated symbol (func / struct / const) by
whether its name derives from a query whose text changed in the working tree
(`GetWidget` ⇒ `GetWidget`, `GetWidgetParams`, `GetWidgetRow`, `getWidget`).
Table structs in `models.go` derive from the schema, never a query, so the
whole-schema drift above is always classified **PRE-EXISTING DRIFT** and left for
a separate PR. Like the `workspace` verbs, it is **dry-run by default**; `--apply`
writes.

**Gating — clean vs intentionally post-edited.** Some generated trees are
hand-post-edited on top of sqlc's output (aphrollo-api's `sqlcgen` — see the
header of its `sqlc.yaml`), so a clean regen *always* differs and `check` must not
fail on them. The mechanism is an explicit per-config `clean: true|false` flag in
a committed sidecar `.aphrollo-sqlc.yaml` at the repo root (a **separate** file —
the sqlc configs keep their exact semantics):

```yaml
configs:
  - file: sqlc.yaml      # post-edited → reported-only: drift printed, never fails check
    clean: false
  - file: sqlc-ai.yaml   # meant to be clean → gated (the default)
    clean: true
```

A config absent from the sidecar defaults to **gated** (`clean: true`), so a newly
added config can never silently skip the gate. The external `sqlc` binary is
resolved via `APHROLLO_SQLC_BIN`, then `$PATH`, then the operator go-install path.

## Setup — `aphrollo tdd init`

One command wires the whole gate — the native replacement for
`claude-code-tdd`'s `install.sh`:

```sh
aphrollo tdd init                    # session hooks + global git gate
aphrollo tdd init --no-git           # session hooks only (skip the git gate)
aphrollo tdd init --uninstall        # remove everything again
```

`init` does two things:

1. **Session hooks** — patches `settings.json` (`$CLAUDE_CONFIG_DIR` or
   `~/.claude`) so `pretooluse` / `posttooluse` / `userpromptsubmit` /
   `sessionend` invoke the binary. Idempotent (a no-op re-run rewrites
   nothing), backs up any existing file, preserves foreign hooks (caveman) and
   other keys, and migrates out old Node `tdd-*.js` entries.
2. **Git gate** — writes the `pre-commit` shim into `~/.config/git/hooks` (or
   `--git-hooks-dir`) and points git's global `core.hooksPath` at it, so every
   repo is gated. Hand-written hooks are never clobbered. `--no-git` skips this
   layer. tdd has no pre-push gate, so a re-init also **prunes** any managed
   `pre-push` shim it finds, leaving hand-written hooks untouched.

It resolves the invoking binary via `os.Executable`, so the installed hooks
call the same binary that wrote them; ansible runs it once per session HOME.

For a single repo without the global gate, `aphrollo tdd install --apply` writes
the same shims into that repo's `.git/hooks` instead (opt-in, no `core.hooksPath`).

The tdd gate is **solely mechanical**: edit-time smell blocks + commit-time
anti-cheat/fail-first/suite. It carries no LLM or non-deterministic step —
adversarial review lives in the reviewer agent, not this binary. Mutation
testing is intentionally **not** ported (false-positive/non-determinism prone);
the fail-first + mechanical suite cover the same ground without the flakiness.

## Layout

```
cmd/aphrollo/        entry point (one-liner over internal/cli)
internal/cli/        arg parsing + subcommand dispatch (testable Run)
internal/refactor/   orchestration: detect lang → spawn server → rename/refs/outline/show
internal/lsp/        LSP types + JSON-RPC stdio client (framing, Conn, edits)
internal/diff/       deterministic unified-diff renderer
internal/guardrail/  PreToolUse policy (block long waits, warn on noisy output)
internal/tdd/        TDD gates (mechanical-only): policy engine, edit smells, anti-cheat, RED/GREEN, fail-first, install
internal/workspace/  worktree lifecycle (create/claim/unclaim/list/remove/prune/cleanup) + git verbs (commit/push/submit/pr/ship/merge)
internal/dev/        dev-tier control plane: up/down/restart/status/logs (systemd)
internal/sqlc/       sqlc drift guard: config discovery, regen-into-temp, check, scoped-by-symbol regen
```

## Known limitations (v1)

- `--col`/`--symbol` and reference columns are UTF-16 code units (the LSP
  convention), not bytes — exact for ASCII, and `--symbol` always resolves
  correctly regardless.
- Unified diffs assume newline-terminated files; a missing final newline is not
  annotated with `\ No newline at end of file`.
- Diff headers use the file path verbatim (`a/<path>`); absolute paths render as
  `a//abs/path`. The diff is for reading and for our own `--apply` (which writes
  files directly, not via `git apply`).
```
