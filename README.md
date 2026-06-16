# aphrollo-tools

First-party dev-env tooling for the Aphrollo agent platform, shipped as a single
zero-dependency Go binary: **`aphrollo`**. The goal is to move deterministic
developer work *out of the agent token stream into code* — the agent spends
tokens on judgment, not on mechanical read→grep→multi-edit→verify loops.

Design contract for every tool here:

- **Lossless** — never silently transform, truncate, or filter output.
- **Deterministic** — same inputs, same bytes out (sorted, stable).
- **Visible** — dry-run by default; show exactly what would change before it
  changes; fail loud with a fix suggestion rather than guessing.

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

### Prepare a worktree to work in (one shot)

Getting an isolated worktree to run tests or build a branch in otherwise costs
the same mechanical sequence every time — mark the (often cross-owner) repo
git-safe, `git worktree add`, mark the worktree git-safe, install dependencies
(git worktrees do **not** share the main tree's gitignored `node_modules`). The
agent paid for that dance in tool calls and tokens on every branch.

`aphrollo workspace prepare` folds it into one deterministic, dry-run-by-default
command:

```sh
# dry-run: print exactly what would happen, change nothing
aphrollo workspace prepare ~/spaces/aphrollo/aphrollo-web feat/kanban
# workspace prepare: aphrollo-web @ feat/kanban (new branch)
#   worktree: ~/spaces/aphrollo/.worktrees/aphrollo-web/feat-kanban
#
# steps (dry-run — pass --apply to execute the [run] steps):
#   1. [run] git config --global --add safe.directory .../aphrollo-web
#   2. [run] git -C .../aphrollo-web worktree add -b feat/kanban .../feat-kanban
#   3. [run] git config --global --add safe.directory .../feat-kanban
#   4. [run] pnpm install   (cwd .../feat-kanban)

# execute it
aphrollo workspace prepare ~/spaces/aphrollo/aphrollo-web feat/kanban --apply
```

Every step is **idempotent** — an already-marked `safe.directory`, an existing
worktree, or a present `node_modules` is reported `[skip]` rather than redone, so
re-running `prepare` on a half-built workspace finishes the job without
clobbering it. The dependency step is auto-detected from the worktree root:

| Marker (priority order) | Install command | Skip when present |
|---|---|---|
| `pnpm-lock.yaml` | `pnpm install` | `node_modules/` |
| `yarn.lock` | `yarn install` | `node_modules/` |
| `package-lock.json` | `npm ci` | `node_modules/` |
| `package.json` | `npm install` | `node_modules/` |
| `go.mod` | `go mod download` | — |

The worktree lands at `<repo-parent>/.worktrees/<repo-name>/<branch-slug>`, so a
prepared worktree can later be `claim`ed onto the dev tier. Override the base dir
with `--into`, skip steps with
`--no-install` / `--no-safe-dir`, or force a re-install with `--reinstall`.

Companion read/cleanup subcommands:

```sh
aphrollo workspace list ~/spaces/aphrollo/aphrollo-web            # git worktree list
aphrollo workspace remove ~/spaces/aphrollo/aphrollo-web feat/kanban --apply
```

Exit codes: `0` ok, `1` runtime error, `2` usage error.

### Put a prepared worktree on the dev tier (claim)

`prepare` gets you a ready worktree; `claim` makes it the one the **dev tier**
serves, so the branch is viewable at rlndx (or driven by dev-api):

```sh
# dry-run: shows the steps it would run
aphrollo workspace claim ~/spaces/aphrollo/aphrollo-web feat/kanban
# workspace claim: …/.worktrees/aphrollo-web/feat-kanban -> dev-rlndx
#   1. [run] repoint …/.devclaim/web -> …/feat-kanban
#   2. [run] restart dev-rlndx (aphrollo dev restart rlndx)

aphrollo workspace claim ~/spaces/aphrollo/aphrollo-web feat/kanban --apply
```

The orchestration runs **unprivileged, in this binary**: resolve the worktree
from `<repo> <branch>` (symmetric with `prepare` — no `.worktrees/…` path to
paste), `pnpm install` if the tree was never prepared, and repoint the
`.devclaim/<repo>` symlink the dev units follow (the dir is `aphrollo-dev`
group-writable, so a group member repoints it with no sudo). The **only**
privileged atom is restarting the dev unit, delegated to the in-binary
[`dev`](#dev-tier-control-plane-aphrollo-dev) control plane (`dev.Restart`),
whose exact-match `sudo systemctl restart` is the one fenced step (and that
restart also clears the rlndx vite optimizer cache for the freshly-claimed
tree).

This subcommand deliberately does **not** carry a sudo grant of its own — the
privilege stays the narrow exact-match systemctl grant in `dev`. The privileged
restart only fires on `--apply`; a missing worktree points you at `prepare`
rather than half-claiming, and a re-claim whose symlink already points at the
tree is reported `[skip]`.

The dev service is derived from the repo (`web → rlndx`, `api → api`); override
with `--svc`. `--into` matches a non-default `prepare --into`. Env overrides
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
the restart is the one fenced `dev.Restart` atom — and the same dry-run/`--apply`
contract.

```sh
aphrollo workspace unclaim                       # cwd-aware (run from inside the worktree)
aphrollo workspace unclaim ~/spaces/aphrollo/aphrollo-web feat/kanban --apply
# unclaimed: dev-rlndx now serves ~/spaces/aphrollo/aphrollo-web
```

### Git verbs — commit / push / pr / ship

These fold the mechanical git/`gh` dance into one command that emits **precise,
deterministic feedback** (sha + delta, ahead-count + URL, PR number), so a coder
session lands a change without spending a tool call each on `git add`, `git
commit`, `git push`, parsing the output, and `gh pr create`. They default to the
worktree you are **standing in** (zero args); pass `<repo> <branch>` to drive a
prepared worktree from outside it (symmetric with `prepare`/`claim`). All are
dry-run by default; `--apply` executes.

```sh
# commit: stage (-A) + commit, honoring the TDD pre-commit gate
aphrollo workspace commit -m "feat: kanban drag-and-drop" --apply
# committed a1b2c3d on feat/kanban: feat: kanban drag-and-drop
#   3 files changed, 42 insertions(+), 7 deletions(-)

aphrollo workspace push --apply          # git push -u origin HEAD
# pushed feat/kanban -> origin (2 commit(s))
#   https://github.com/aphrollo/aphrollo-web/tree/feat/kanban

aphrollo workspace pr --apply            # open (or reuse) the GitHub PR
# opened PR #321: https://github.com/aphrollo/aphrollo-web/pull/321  (main <- feat/kanban)

aphrollo workspace ship -m "feat: kanban" --apply   # commit -> push -> pr in one shot
```

- **commit** stages `git add -A` by default (`--staged-only` to commit the index
  as-is) and runs the [TDD pre-commit gate](#tdd-gates-aphrollo-tdd); `--no-verify`
  is the documented escape for the gate's known false-positives. A clean tree is a
  reported no-op, not an error.
- **push** sets the upstream on a first push and reports the ahead-count and the
  branch's github URL; `--force-with-lease` for a rebased branch.
- **pr** is idempotent — an existing open PR for the branch is reported, never
  duplicated. `--base` (default `main`), `--title`/`--body` (default: filled from
  the commits by `gh`), `--draft`. Needs the branch pushed first (it points you at
  `push` if not).
- **ship** chains the three behind one command, stopping at the first failure so a
  partial result (e.g. committed but not pushed) is resumable by the discrete verbs.

### Close the loop — merge / cleanup

After review, `merge` lands the branch's PR and `cleanup` tears down the local
worktree, so a coder owns the change end-to-end without dropping to raw `gh` and
`git worktree`:

```sh
aphrollo workspace merge --apply         # gh pr merge --squash --delete-branch
# merged PR #321 (squash): https://github.com/aphrollo/aphrollo-web/pull/321
#   deleted branch feat/kanban
#   next: aphrollo workspace cleanup feat/kanban --apply

aphrollo workspace cleanup feat/kanban --apply   # git worktree remove + prune
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
the dry-run/`--apply` contract — and reports each stale entry by path:

```sh
aphrollo workspace prune                 # dry-run: "would prune N stale worktree(s)"
aphrollo workspace prune --apply         # "pruned N stale worktree(s)"
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
> `aphrollo workspace prepare/list/remove`; `claim` by `aphrollo workspace claim`.

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
| `tdd prepush` | git `pre-push` | Adversarial LLM review of the cumulative push diff; blocks on a critical/high finding. **Fails open** if the reviewer is unavailable. |

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
2. **Git gate** — writes the `pre-commit` / `pre-push` shims into
   `~/.config/git/hooks` (or `--git-hooks-dir`) and points git's global
   `core.hooksPath` at them, so every repo is gated. Hand-written hooks are
   never clobbered. `--no-git` skips this layer.

It resolves the invoking binary via `os.Executable`, so the installed hooks
call the same binary that wrote them; ansible runs it once per session HOME.

For a single repo without the global gate, `aphrollo tdd install --apply` writes
the same shims into that repo's `.git/hooks` instead (opt-in, no `core.hooksPath`).

Mutation testing is intentionally **not** ported: it was the documented
false-positive/non-determinism offender, and the fail-first + review gates cover
the same ground without the flakiness.

## Layout

```
cmd/aphrollo/        entry point (one-liner over internal/cli)
internal/cli/        arg parsing + subcommand dispatch (testable Run)
internal/refactor/   orchestration: detect lang → spawn server → rename/refs/outline/show
internal/lsp/        LSP types + JSON-RPC stdio client (framing, Conn, edits)
internal/diff/       deterministic unified-diff renderer
internal/guardrail/  PreToolUse policy (block long waits, warn on noisy output)
internal/tdd/        TDD gates: policy engine, edit smells, anti-cheat, RED/GREEN, fail-first, review, install
internal/workspace/  worktree lifecycle (prepare/claim/unclaim/list/remove/prune/cleanup) + git verbs (commit/push/pr/ship/merge)
internal/dev/        dev-tier control plane: up/down/restart/status/logs (systemd)
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
