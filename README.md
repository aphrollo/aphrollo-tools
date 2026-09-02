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
  previews); `refactor`/`gate` mutations are **dry-run by default** (`--apply`
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

`aphrollo refactor` IS the rename — flags parse directly on the verb. It is
dry-run by default; pass `--apply` to write.

```sh
# dry-run: prints a unified diff of every file that would change
aphrollo refactor --file internal/foo/bar.go --line 42 --symbol OldName --new-name NewName

# apply to disk
aphrollo refactor --file ... --line 42 --symbol OldName --new-name NewName --apply
```

Locate the target with either `--symbol NAME` (resolved to the right column on
that line, UTF-16-correct — paste it straight from a grep hit) or an explicit
`--col` (1-based UTF-16).

### Find every reference to a symbol

`aphrollo find` is a top-level read-only verb — flags parse directly on it.

```sh
aphrollo find --file internal/foo/bar.go --line 42 --symbol Name
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

`commit`, `push`, `submit`, and `update` are **cwd-only** — they act on the
worktree you stand in and take no positional `<repo> <branch>` (that targeting
lives on the operator verbs `merge`/`status`/`diff`). `create` keeps its required
`<repo> <branch>` (it runs from the main clone).

### Create a worktree to work in (one shot)

Getting an isolated worktree to run tests or build a branch in otherwise costs
the same mechanical sequence every time — mark the (often cross-owner) repo
git-safe, `git worktree add`, mark the worktree git-safe, install dependencies
(git worktrees do **not** share the main tree's gitignored `node_modules`). The
agent paid for that dance in tool calls and tokens on every branch.

`aphrollo workspace create` folds it into one deterministic command that
**executes by default** — pass `--dry` to preview:

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

Every `<repo>`-arg verb (`create`, `merge`, `diff`, `unclaim`, `list`,
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

Companion read/remove subcommands:

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
  as-is) and runs the [TDD pre-commit gate](#tdd--law-gates-aphrollo-gate); `--no-verify`
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
  ready yet` and exits non-zero — both **re-callable** until CI goes green. On a
  **conflicted** branch (`mergeable: CONFLICTING` / `mergeStateStatus: DIRTY` —
  the real reason required checks queue forever) it short-circuits the CI gate,
  prints `blocked: branch has merge conflicts, NOT marked ready` plus the fix
  (`rebase onto <base> and resolve, then re-run submit`), and exits non-zero
  without flipping. GitHub computes mergeability async, so the read briefly
  re-polls past the `UNKNOWN` window; if it never resolves it reports
  `mergeable: unknown — re-run to recheck` rather than a false all-clear. `push`
  surfaces the same conflict as a non-fatal `CONFLICT:` warning line.
  submit is **per-worktree, one repo at a time**: it acts on the cwd worktree's
  single PR — `push`/`ship` opened that draft, `submit` flips it draft → ready.
  There is **no ticket-level submit** that fans out across repos; a ticket that
  spans repos is submitted one worktree at a time.

> `pr` and `ship` still exist as operator escapes (they take an explicit
> `<repo> <branch>` and also default to the cwd worktree), but the coder flow is
> `push` (which folds `pr`) then `submit` — not the discrete `pr`/`ship`.

### Verify — the typecheck/lint the commit gate misses

The TDD pre-commit gate runs the mechanical test suite (plus the anti-cheat and
fail-first checks), but **not** typecheck or lint. So a type regression
(`svelte-check`) or a lint failure sails past `commit`/`ship` and only turns up in
CI. `verify` closes that gap: it resolves the **affected app** and runs that app's
`{test, typecheck, lint}` trio. It is verification only — it never commits,
pushes, or mutates source. Like the other `workspace` verbs it addresses the
cwd's worktree (or `<repo> <branch>`) and **executes by default** (`--dry` previews).

```sh
# from inside aphrollo-web/apps/rlndx (or with rlndx files changed on the branch)
aphrollo workspace verify --dry
# workspace verify: aphrollo-web @ feat/kanban  (worktree …/aphrollo-web)
#   app rlndx (apps/rlndx)
#     1. test      npx vitest run
#     2. typecheck npx svelte-check --tsconfig ./tsconfig.json
#     3. lint      npx eslint --no-error-on-unmatched-pattern src
#
# run without --dry to execute (stops at the first failure).

aphrollo workspace verify        # runs test -> typecheck -> lint in order
```

- **`--dry`** lists the exact ordered commands it would run, per app,
  and exits 0 without running them; the default run executes them in order, **stops at
  the first failure**, and surfaces that tool's own output.
- **App resolution** is table-driven (start: rlndx). The affected app is scoped
  from the branch's changed paths; when nothing changed resolves one, it falls
  back to the app the cwd sits in — it never runs the whole monorepo's every-app
  matrix unasked. Adding another app is a table entry, not new branching.
- The **test** command is reused from the [TDD runner detection](#tdd--law-gates-aphrollo-gate)
  (so the two never drift); only the per-app typecheck and lint commands are
  table data. rlndx resolves to `vitest run` + `svelte-check` + `eslint`.

### Review the branch's diff (diff)

`diff` prints the branch's PR diff — `git diff origin/<default>...HEAD`, the
three-dot form so it shows the branch's own changes against the merge-base, not
unrelated commits the default branch gained since. The default branch is read
from `origin/HEAD`, never hardcoded `main`. Read-only and lossless (git's bytes
verbatim); `--stat` for the diffstat summary instead of the full patch:

```sh
aphrollo workspace diff                  # full patch vs origin/<default>
aphrollo workspace diff --stat           # diffstat only
aphrollo workspace diff aphrollo-web feat/kanban   # target a worktree from outside
```

### Catch a branch up to the default branch (update)

`update` rebases the cwd worktree onto the fresh tip of `origin/<default>` and,
on a clean rebase, force-pushes (with lease) so the open PR shows the rebased
branch:

```sh
aphrollo workspace update                # fetch → rebase → push --force-with-lease
# rebased feat/kanban onto origin/main
# pushed feat/kanban -> origin --force-with-lease (3 commit(s) ahead of origin/main)

aphrollo workspace update --dry          # "behind origin/main by N; would rebase"
```

- It runs `git fetch origin`, then rebases HEAD onto `origin/<default>` (resolved,
  not hardcoded). A branch already on top of the default branch is a **no-op**
  ("already current with origin/<default>").
- On a **clean** rebase it `git push --force-with-lease` only when the branch is
  already on origin (an open PR's head); a branch never pushed is reported, not
  pushed.
- On a **conflict** it does **not** abort and does **not** push: the rebase is
  left **in progress**, the conflicted files are printed with
  `resolve, then: git rebase --continue` (or `git rebase --abort` to back out),
  and the verb exits non-zero. cwd-only.

### Catch the base clone up after a merge (sync)

`sync <repo>` brings a base clone's **local default branch** up to the remote
tip. `create`/`prepare` cut fresh worktrees from `origin/<default>` (post-fetch),
but the canonical clone's own checked-out default branch never refreshes — it
drifts further behind on every merge. `sync` is the non-destructive "catch the
clone up to origin" primitive (the post-merge cleanup path calls it):

```sh
aphrollo workspace sync aphrollo-web      # fetch → fast-forward local <default>
# fast-forwarded main to origin/main (3 commit(s))

aphrollo workspace sync aphrollo-web      # idempotent: re-running is a no-op
# main already current with origin/main [skip]

aphrollo workspace sync aphrollo-web --dry   # "would fast-forward main to origin/main (N behind)"
```

- It runs `git fetch origin` (an offline / remote-less repo is a **non-fatal
  warning** — refreshing `origin/<default>` is the minimum win), then resolves
  the default branch (never hardcoded) and **fast-forwards it — strict FF only**:
  - HEAD **is** the default branch and the worktree is **clean** → `git merge
    --ff-only origin/<default>`;
  - the default branch is **not** the checked-out one → advance its ref with
    `git update-ref` (no checkout, so a sibling worktree's files are untouched).
- It **never** `reset --hard`s, forces, touches a dirty worktree, or moves the
  branch when it has **diverged** (local commits ahead). A dirty or diverged
  clone is **left untouched** with a clear reason and **exit 0** (best-effort).
  The fetch still happens in that case. Only the default branch is synced —
  ticket branches and other worktrees are left alone. `--dry` previews and
  mutates nothing.

### Close the loop — merge / prune

After review, `merge` lands the branch's PR and `prune` sweeps the merged
worktrees, so a coder owns the change end-to-end without dropping to raw `gh` and
`git worktree`:

```sh
aphrollo workspace merge         # gh pr merge --squash --delete-branch
# merged PR #321 (squash): https://github.com/aphrollo/aphrollo-web/pull/321
#   deleted branch feat/kanban
#   next: aphrollo workspace prune
```

- **merge** resolves the branch's open PR (reusing the `pr` gh seam) and merges it,
  **honoring GitHub's gates** — gh refuses a non-mergeable or red-CI PR, and `merge`
  never passes `--admin`, so it cannot force past a failing check. `--squash`
  (default) / `--merge` / `--rebase`; `--keep-branch` to skip the branch delete.
  It deliberately does **not** touch the local worktree — that is `prune`'s job.

> Merge stays a deliberate step: in the hub-and-spoke flow it is gated on the
> operator's "ship" + green CI, so a coder runs `merge` on instruction, not
> reflexively. The verb just makes the mechanical step one lossless call.

### Sweep the merged worktrees (prune)

`prune [repo]` sweeps the repo's worktrees and removes the ones whose work is
done. A worktree is removed **only when ALL hold**: its PR is **MERGED**, the
tree is **CLEAN** (no uncommitted changes), and it is **not the worktree you are
standing in**. Anything else is **skipped with a reason** (open PR / no PR /
dirty / current), so the sweep never yanks live work. PR state is read with
`gh pr view <branch> --json state`. The stale admin-record prune
(`git worktree prune`) is folded in:

```sh
aphrollo workspace prune --dry           # lists "would prune" + "skip: … (reason)"
aphrollo workspace prune                 # removes the merged-clean worktrees
# pruned: …/.worktrees/aphrollo-web/feat-kanban (PR merged)
# skip: …/.worktrees/aphrollo-web/feat-wip (open PR)
# pruned 1 worktree(s)

aphrollo workspace prune --force         # also remove a dirty MERGED worktree
```

`prune <repo> <branch>` is the **per-ticket form**: it removes exactly that one
ticket's worktree (per-repo) instead of sweeping. It is **idempotent** — a
re-run on an already-gone worktree is a no-op success (`already gone`), not an
error — so a post-merge cleanup can re-run safely on redelivery. Like the sweep
it leaves the **local branch** in place (deleting the branch is `remove`'s job)
and folds in the stale admin-record prune:

```sh
aphrollo workspace prune aphrollo-web feat/kanban --dry   # "would prune: …"
aphrollo workspace prune aphrollo-web feat/kanban         # "pruned: …"
aphrollo workspace prune aphrollo-web feat/kanban         # "already gone: …" (re-run, still exit 0)
```

So `prune` with **no branch** performs the full merged-worktree sweep
(auto-detecting which worktrees are merged); `prune <repo> <branch>` targets a
single ticket's worktree. To remove a worktree **and** delete its local branch,
use `remove <repo> <branch>`.

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

Like the `workspace` commands, `dev` **executes by default** — but as a service
control plane it has **no `--dry` at all**; it acts immediately like `systemctl`
itself (the `workspace` verbs still take `--dry` to preview). A restart of `rlndx` first clears the claimed tree's stale
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

### TDD + law gates (`aphrollo gate`)

Autonomous test-driven-development enforcement, ported from the retired
`claude-code-tdd` Node hooks. Gates across the edit→commit→push lifecycle, each
near-zero false-positive (a false block wedges the agent, so the heavy checks
live where being wrong only costs a re-run):

| Subcommand | Wiring | What it does |
|---|---|---|
| `gate pretooluse` | Claude PreToolUse hook (stdin) | Blocks (exit 2) a **test-file** edit introducing an oracle smell — real-time sleep, tautological self-comparison, focused marker (`.only`/`fit`), or a disabled test (`.skip`/`xit`/`t.Skip`/`@pytest.mark.skip`). **Warns** (test or source) on a suppression that silences a quality gate (`//nolint`, `@ts-ignore`, `# type: ignore`, coverage-ignore). |
| `gate posttooluse` | Claude PostToolUse hook (stdin) | Runs the edited file's related tests as a build phase then a run phase under ONE budget, deferring whatever does not finish (see below); surfaces a RED summary. **Silent unless RED.** Source extensions include `.ron` — in a Rust workspace those are registries and fixtures whose edits change behaviour, resolved to the owning crate exactly as `.rs` is. |
| `gate userpromptsubmit` | Claude UserPromptSubmit hook (stdin) | Intercepts `/gate [status\|off\|on\|reset]` — the per-session enforcement escape hatch. On any other prompt, re-injects the last RED outcome for the cwd's project so the gate survives context compaction. **Silent unless RED.** |
| `gate stats` | manual | Tallies `gate.log` by stage and outcome, with per-crate timeout/deferred counts and median/max gate seconds (`--since 7d`). |
| `gate runphase` | spawned by `gate posttooluse` | The detached build/run phase's wrapper: holds the build slot, logs to the state dir, writes the result file the next hook harvests. Never typed by a human; never blocks. |
| `gate sessionend` | Claude SessionEnd hook (stdin) | Deletes the per-session state file so the state dir doesn't accumulate. |
| `gate precommit` | git `pre-commit` | Blocks a newly-**added** suppression (anti-cheat). Then **fail-first**: a commit adding both tests and source must have tests that fail without the source. Then the suite must pass. A worktree state already proven green under the exact same command (by a PostToolUse run or an earlier gate pass) is **not re-run** — the cache is keyed on the repo's git COMMON dir, so every linked worktree of one repo reuses the same proven-green facts — only green results are cached, keyed on content + runner argv (content covers tracked files AND the ignored configuration a suite reads: dotenv files and `config/` trees, never build output), so a red always re-runs with fresh output. Both gate stages build in the REPO'S OWN target dir (see below). |
| `gate commitmsg` | git `commit-msg` | Rejects a commit whose MESSAGE carries a deny pattern, quoting the offending line. Opt-in per workspace (`undercover = true`); absent key = pass through. Fires for merge commits too. |
| `ratchet check` | git `pre-commit`/`pre-merge-commit`, and manual | Judges the tree against `.ratchet/laws/*.toml` (see [Ratchet laws](#ratchet-laws-aphrollo-ratchet)). |
| `gate prepush` | git `pre-push` | **No-op** (mechanical-only mode). The gate is solely mechanical now; adversarial review is owned by the separate reviewer agent, not this binary. Kept only so a `pre-push` shim lingering from before the change exits cleanly — it **never blocks**. |

#### Gate stage order (cheapest first)

`precommit` and `premergecommit` run the same pipeline per project root and
**stop at the first rejection**, so a formatting slip costs milliseconds
instead of a full test build:

| # | stage | cost | notes |
|---|---|---|---|
| 0a | baseline guard — a STAGED baseline that ROSE | ms (one `git show` per staged baseline) | rejects `baseline-rejected`; see below |
| 0b | `ratchet check` — the repo's declared laws | ms (mtime cache) | only when `.ratchet/laws/` exists; rejects `ratchet-rejected`. A commit that stages a `.ratchet/` file also re-proves every law against its fixtures |
| 1 | `cargo fmt --check -p <touched>` | ms | compiles nothing, takes no build slot |
| 2 | `always-run` packages, their OWN invocation | seconds | a pure guard crate; bundling it into `-p ratchet -p client` made it wait for client to link |
| 3 | `cargo clippy -p <clippy-clean> --tests -- -D warnings` | front-end build | only crates declared clippy-clean |
| 4 | `cargo clippy --workspace --tests -- -D clippy::disallowed_methods -D clippy::disallowed_types` | check-level, tens of seconds warm | no codegen, but it sees EVERY crate: a lane that broke a crate nobody staged used to land green (borld `forge_jbeam/tests/conformance.rs` reached main not compiling). clippy SUBSUMES check, so a compile error fails here too, and denying exactly those two lints is what makes a `clippy.toml` law reach crates that are not on the `clippy-clean` list. Everything else stays at its default level. Rejects `check-rejected` (does not compile) or `lint-rejected` (banned API) |
| 5 | fail-first RED proof | worktree build | precommit only, and only when the staged tests ADD a declaration |
| 6 | touched crates' suites | full build + link + run | the heaviest, and therefore last |

Stages 2, 4 and 6 are all short-circuited by the green cache (same content +
argv key), so a guard crate proven green at commit is not re-run at merge.
gate.log names the stage that rejected (`fmt-blocked`, `always-run-blocked`,
`clippy-blocked`, `check-rejected`, `lint-rejected`, `mechanical-blocked`,
`queued-rejected`, `timeout-rejected`). `posttooluse`
is unchanged: one related-test run per edit.

**`green-unconstrained`** is the edit hook's one coverage note: the run was
green, the edited file is a non-test SOURCE file, and the pass count is
identical to the last green for that project — so no test came with the
change and nothing new constrains it. Fail-first cannot see this case (no test
was staged to fail), which is why it is said out loud:
`→ green-unconstrained (7 passed; no test changed with this edit — mutation
proof owed)`. It never blocks, never touches the timeout streak, and is
recorded as green for `/gate status`.

`/gate off` is the escape hatch for spikes and non-TDD work; `/gate on` re-enables.
The SessionStart baseline and `/gate allow-main` from the Node original are
deliberately **not** ported — a full suite on every session start costs more than
the one first-edit false-RED it avoids, and there is no main-branch edit gate
here to toggle.

Three further test-quality smells **warn and never deny** — they are judgement
calls, and a false deny wedges a session. Each prints one `file:line` note:

- **weak physics bar** — `assert!(x > 0.0)` in a `crates/{forge*,movement,pose,shared}`
  test: a sign check passes for a value 100x wrong ("state the closed-form
  value and tolerance").
- **generic test name** — `fn test_*`, `*_works`, `*_basic`, `*smoke*`: a name
  that describes nothing cannot say which production change makes it red.
- **unexplained tolerance** — `approx_eq` / `abs_diff_eq` /
  `assert_relative_eq` / `< EPS` / `< 1e-` with no `// tolerance: <why>` (or
  `// why:`) within two lines above: a tolerance is a hole the size of the
  tolerance until something names its consumer.

Files matching `_platform_pin` are exempt: they record what a MACHINE does, so
a sign bar or a tolerance there is the point of the file.

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

#### Where the gates build (one target dir per repo)

Both gate stages — fail-first and the suites, plus fmt/clippy — build into
the target dir the DEVELOPER builds into: `CARGO_TARGET_DIR` when the
environment names one, else `<repo>/target`. A linked worktree therefore uses
its own `target/`, exactly like a build the developer runs there; export
`CARGO_TARGET_DIR` to share one.

The gate used to build into a private `<stateDir>/cargo-target/<hash>`, which
kept it out of the developer's way and cost a SECOND full copy of the
workspace's artifacts (a measured 155 GB) plus a cold compile on every commit
of code that was already built next door. Deps are shared, workspace crates
coexist per source path, and the per-target build lock is what keeps the two
builds off each other's toes: the gate queues visibly like any other build and
rejects with `queued-rejected` if no slot comes free inside its 20-minute wait.

The fail-first worktree (still `<stateDir>/failfirst-wt/<hash>`, stable so
cargo's path-baked fingerprints stay warm) EXPORTS that same target dir — it
lives outside the repo, so cargo's default would otherwise create a third one
inside it.

#### Which cargo target an edit runs

Getting this wrong is not a slower run, it is an error — `--test x` for a
target that does not exist fails instantly and proves nothing:

| edited file | run |
|---|---|
| `<crate>/tests/x.rs` | `--test x` |
| `<crate>/tests/<dir>/**` | `--test <dir>` |
| `<crate>/src/a/b.rs` (incl. `*_tests.rs` modules) | `--lib`, filtered to `a::b::` — a `#[cfg(test)] mod` under `src/` is part of the LIB test binary, not a test target of its own |
| `<crate>/src/lib.rs`, `src/main.rs`, `src/a/mod.rs` | `--lib` (no filter: that IS the crate/module) |
| `<crate>/examples/x.rs`, `examples/x/**` | `--example x` |
| `<crate>/benches/x.rs` | `--bench x --no-run` — a bench RUN costs minutes and says nothing about correctness |

The filter dialect follows the runner: `-E 'test(/^a::b::/)'` for nextest, the
`a::b::` substring for plain `cargo test`.

#### The edit hook's budget, and deferred builds

A cold Bevy-sized build does not fit in an edit hook, and killing it at the
budget threw away both the work and the answer (268 timed-out runs in one
`gate.log`, every one a cold build that established nothing). So the edit
hook splits its cargo work into a **build phase** (`--no-run`) and a **run
phase**, and defers rather than kills:

- **One foreground budget**, `APHROLLO_POSTEDIT_BUDGET_SECS` (default 110s),
  covers build **and** run together. There is no separate build budget: the
  case that actually happens is "the build ate all of it".
- Whichever phase is still going at the budget keeps running **detached**
  (Windows: `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`; elsewhere its own
  session), so the hook's exit cannot take it down. The hook prints one line —
  `gate: → BUILDING (deferred; <project> build phase — result at the next hook)`
  — and returns immediately.
- The next `posttooluse` or `userpromptsubmit` **harvests** it and reports the
  outcome as `gate: deferred <runner> ... → green (1.3s)`. A finished build phase is followed by the now
  warm run phase, so the expensive half is never repeated.
- A result is only adopted when it describes the code on disk **now**: same
  HEAD, same edited-file content, not marked dirty.
- **A healthy build is never killed.** Another edit to the same project marks
  the running job dirty (so the harvest knows to rebuild) and leaves it alone.
  The one exception is a job past `APHROLLO_DEFERRED_MAX_SECS` (default 600s),
  which is abandoned so a wedged process cannot block a project forever.
- **A deferred build is not a timeout.** The backoff streak only moves for a
  RUN phase that outlived both the foreground budget and the deferred maximum.
- Only cargo splits: `go test --no-run` is not a flag, so every other runner
  gets one (still deferrable) phase.

Each detached phase runs under `aphrollo gate runphase --job <record>`, which
holds the build slot, writes the phase's output to
`<state-dir>/deferred/<project>.log` and — the liveness signal — writes
`<project>.result.json` when cargo exits. Jobs are keyed by PROJECT, not by
session, so a build orphaned by an ended session is still harvestable by a
later hook in any session.

### Cargo lock — per-target build slots (`aphrollo gate cargo`)

The hooks, the commit gate and the `cargo-queue` shim all take the same
advisory locks before running cargo. There are **two**, because there are two
different constraints. Every lock file — target locks, global slots, owner
records — lives in ONE directory (`os.TempDir()` in production), behind a
single seam a test overrides in one statement: a per-path override is one a
test can forget, and forgetting is what left 871 stale locks in `%TEMP%`.

```
%TEMP%/aphrollo-cargo-build.<sha256_8 of the target dir>.lock   # one build per target dir
%TEMP%/aphrollo-cargo-slot.<i>.lock                             # N global slots
```

- The **target lock** mirrors cargo's own build-directory flock: exactly one
  build per target dir (`CARGO_TARGET_DIR` when set, else `<workspace
  root>/target`). Handing out a second would give a builder a slot it then
  spends its whole budget blocked on *inside* cargo, invisibly.
- The **global slots** are the OOM/CPU governor: separate target dirs do not
  compete for a build directory, but they do compete for the box.

A build takes the target lock first, then a global slot, and releases in
reverse; if no slot is free the target lock goes straight back, so a busy box
never leaves target dirs locked by builds that never started.

- **N is 2 by default**; `APHROLLO_BUILD_SLOTS` overrides it.
- Each holder's child cargo gets `CARGO_BUILD_JOBS = totalJobs / N` (totalJobs
  = `[build] jobs` from `~/.cargo/config.toml`, else the core count), so N
  builds cost about what one uncapped build used to. A caller that already
  exported `CARGO_BUILD_JOBS` keeps its own number — the split is a default,
  never an override.
- **Parallelism only exists across DIFFERENT target dirs.** Two sessions
  sharing one `target/` (e.g. worktrees with a shared `CARGO_TARGET_DIR`) are
  one build directory and are told to wait, visibly, instead of blocking
  inside cargo. Real parallelism means separate target dirs: a worktree with
  its own `target/`, or the gate's own per-repo target under the state dir.
- **The commit gate never fails open on the queue.** The mechanical stage
  builds in the GATE-OWNED per-repo target dir (under the state dir) rather
  than the dev's own `target/`, so gate and human never contend; and if no
  slot comes free within `APHROLLO_LOCK_WAIT_SECS` the commit is **rejected**,
  naming the holder — a commit that was never tested must not land silently.
  The wait defaults to **1200 s** (two lanes committing at once genuinely
  serialise behind each other's suite) and the queued-behind line repeats once
  a minute so the wait is never silent.
  A suite TIMEOUT also REJECTS at commit time (`timeout-rejected`), naming the
  elapsed seconds: a commit whose suite never finished is a commit nobody
  tested, and the untested code would stay in history. The gate target is warm
  by then, so the retry usually finishes. The EDIT hook keeps the advisory
  behaviour — blocking an edit over a stopwatch would wedge the session.
- **The edit hook never waits.** `gate posttooluse` tries the slots once and
  reports `QUEUED-SKIPPED` if they are all busy — it used to spend 20s of its
  budget queuing behind a build that takes minutes.
- Budget knobs: `APHROLLO_POSTEDIT_BUDGET_SECS` (the edit hook's ONE
  foreground budget for build + run, default 110s),
  `APHROLLO_DEFERRED_MAX_SECS` (how long a detached phase may live before it
  is abandoned, default 600s) and `APHROLLO_LOCK_WAIT_SECS` (how long a commit
  queues for a slot, default 1200s).
- **Read-only verbs never take a slot**: `metadata`, `tree`, `fmt`,
  `locate-project`, `pkgid`, `read-manifest`, `--version`/`-V` pass straight
  through, so tool detection answers instantly while a build owns the box.
  `check` and `clippy` DO take one — they run the compiler front end into the
  shared target dir.
- **`cargo watch` is NOT a long verb**: it recompiles on every save for as
  long as it is open, so it holds a slot like any other build.
- **A long verb holds ONE slot for its whole run, and lends it.** `mutants`,
  `bench` and `install` take a slot, run a prewarm compile under it
  (`cargo check --tests` for mutants, `cargo build --benches` for bench,
  `cargo build --tests` otherwise), then RELEASE THE TARGET LOCK and keep the
  global slot until they exit. The long phase and every cargo it spawns
  inherit `APHROLLO_SLOT_TOKEN=<slot lock>`, which skips the global semaphore
  but NOT the per-target lock: each mutation copy still holds the lock for
  its own target dir, so cargo's one-build-per-target invariant survives.
  Measured 2026-09-02: without this, a four-job `cargo mutants` run had each
  inner build take a slot of its own, and both slots stayed held for hours
  while every other session queued. `cargo run` is the other split: it builds
  under a slot and launches the binary with neither the slot nor the token
  (the launched process outlives both). The prewarm is a warm-up, not a gate:
  its exit code is discarded and it is skipped outside a cargo project.
- A waiter still prints exactly one `queued behind "<cmd>" in <cwd>` line
  naming a holder, one line on acquire, and exits 75 (`EX_TEMPFAIL`) when it
  gives up (`APHROLLO_CARGO_WAIT_SECS`, default 20 min).
- **Acquiring a slot IS recording its owner** — one function writes both the
  target-dir record (who is building *here*) and the global-slot record (who
  is using up the box's capacity), and the release removes them. A caller
  asked to remember a second call eventually forgets, which is how a merge
  came to print `queued behind another build (holder unknown)`. Two records,
  not one, because the two waits are different: when the target dir is free
  and every global slot is taken, the waiter names a slot holder and adds
  *(the box is at capacity)*. A long verb hands its target dir back after the
  prewarm but keeps its slot for hours, so its slot record deliberately
  outlives its target record.

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
a separate PR. Like the `refactor`/`gate` mutations (not the execute-by-default
`workspace` verbs), it is **dry-run by default**; `--apply` writes.

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

### Git queue (`aphrollo gate git`)

Index-mutating git verbs queue behind an advisory lock so concurrent sessions
sharing a checkout never collide on `.git/index.lock`. **The lock is keyed by
what the verb actually mutates**, because the index is per worktree: keying
everything on the shared common dir made one lane's commit gate (which holds
its lock for the whole gate run) block `git add` in every other worktree of
the same repo.

| verb | lock file lives in | why |
|---|---|---|
| `add`, `commit`, `checkout`, `switch`, `reset`, `stash`, `rm`, `mv`, `rebase`, `cherry-pick`, `revert`, `am`, `merge`, `pull`, `restore --staged`, `apply --index/--cached` | `git rev-parse --git-dir` (this worktree) | they write THIS worktree's index/HEAD, and `index.lock` lives there |
| `worktree add/remove/prune`, `branch -d/-D/-m/-M/-c/-C`, `fetch`, `push`, `gc` | `git rev-parse --git-common-dir` (shared) | they write refs, the worktree registry or the object store, which every worktree reads |
| `status`, `diff`, `log`, `show`, `branch` (listing), `restore` (no `--staged`), `apply` (no index), `worktree list`, … | not locked | read-only |

One `queued behind "<cmd>" in <cwd>` line when it has to wait, one on
acquire, exit 75 after `APHROLLO_GIT_WAIT_SECS` (default 20 min). A stray
`index.lock` left by a git process that bypassed the shim is waited out too.

### Cargo workspace metadata (`[workspace.metadata.aphrollo]`)

Two opt-in lists, declared in the workspace's own `Cargo.toml` so they version
with the code they police and are reviewed in the same diff:

```toml
[workspace.metadata.aphrollo]
always-run   = ["ratchet"]            # run these packages' suites on EVERY mechanical stage
clippy-clean = ["server", "shared"]   # gate these on `clippy -D warnings` at commit
undercover = true                    # reject commit messages that name the tooling
commit-message-deny = ["^WIP:"]      # this repo's own extra deny patterns
```

- **`always-run`** — a workspace-wide guard package (its tests scan the whole
  tree) is owned by no staged file, so ownership scoping alone would run it
  only when someone edits the guard itself, which is exactly when its
  invariant is not at risk.
- **`undercover`** (bool) — turns on the `commit-msg` gate. The commit
  message is the one artefact of a session that leaves the machine and stays
  in history forever, so a repo can ask that it describe WHAT changed and
  nothing about how it was written. Built-in deny patterns, all
  case-insensitive: `^Co-Authored-By:`, `\bClaude\b`, `\bAnthropic\b`,
  `Generated with`, `\bopus-\d`, `\bsonnet-\d`, `\bhaiku-\d`, `\bfable\b`,
  `claude-code`, `\bgo/[a-z]`, `#claude-`, `anthropics/`,
  `\bAI\b\s+(assistant|generated|written)` (bare "AI" is a word in ordinary
  prose, so it only counts when it claims authorship), and the codenames
  `Capybara|Tengu`. Lines starting `#` are git's own comment lines and are
  skipped. The rejection QUOTES the offending line and names the pattern —
  an author who has to guess which of thirty lines offended will retype the
  message from memory. Absent key = the gate is inert, so installing the hook
  everywhere cannot start rejecting a repo that never asked.
- **`commit-message-deny`** (string array) — the repo's OWN extra patterns for
  that gate, e.g. `commit-message-deny = ["(?i)\\bskunkworks\\b", "^WIP:"]` (a TOML basic string, so the regex backslash is doubled).
  An unparseable entry is skipped with a stderr note, never silently disabling
  the gate nor blocking every commit.
- **`mutation-receipt`** (bool) — turns on the merge gate's receipt check.
  Fail-first proves a test FAILED once; it says nothing about whether the
  test constrains behaviour, and a test that asserts nothing satisfies
  fail-first perfectly. A MERGE needs both. With the key set,
  `premergecommit` looks up `<stateDir>/mutation-receipt.<tip_tree>.json`,
  where `<tip_tree>` is the LANE TIP's tree (`git rev-parse MERGE_HEAD^{tree}`
  — never the merge result, which nobody has mutation-tested). The file is
  written by the consuming repo's own mutation run (borld's
  `tools/mutation_gate.sh`) and carries `repo`, `branch`, `tip_tree`,
  `worktree_dirty`, `base_ref`, `mutants_total`, `caught`, `timeout`,
  `unviable`, `survivors`, `accepted`, `unaccepted` and `verdict`. The merge is
  refused (`receipt-rejected`) when there is no receipt for that tree, when
  `worktree_dirty` is set, when `verdict` is anything but `"pass"` (an
  unrecognised verdict refuses — a gate that reads an unknown word as
  permission is not a gate), or when `unaccepted` is non-empty. The receipt is
  keyed by TREE in its FILENAME, so the lookup itself is the identity check and
  two lanes measured minutes apart never read each other's answer;
  `mutants_total: 0` is a valid receipt, since a diff with nothing mutable in
  it is a real answer. `unaccepted`'s entries are opaque to the gate — the
  producing repo decides how it names a mutant — and only the first is quoted
  in the rejection, which also names the command that produces a receipt. It
  runs BEFORE any suite compiles.
- **`baselines`** (string array) — the globs the baseline guard watches, e.g.
  `baselines = [".ratchet/baselines/*.txt", "crates/ratchet/tests/*_baseline.txt"]`
  (those two are also the defaults, and a declared list replaces them). See
  [Baselines are never raised by hand](#baselines-are-never-raised-by-hand).
- **`clippy-clean`** — the quality checks run BEFORE the suites, cheapest
  first: `cargo fmt --check -p <crate>` for every TOUCHED crate is stage 1,
  and `cargo clippy -p <crate> --tests -- -D warnings` for crates on this
  list is stage 3 (see the stage-order table above). A crate that reached
  zero warnings stays there; crates not on the list are never lint-gated, so
  the gate stays usable in a tree that still carries warnings. Absent key =
  no clippy stage at all. Both run in the mechanical stage's target dir;
  clippy takes a build slot, fmt does not (it compiles nothing). A failure
  blocks the commit and names the crate and the first diagnostic.

### Ratchet laws (`aphrollo ratchet`)

A repo's code laws — "a float `.clamp()` is not a NaN guard", "modules stay
under 600 lines", "every env switch is registered", "every cited `.md` path
resolves" — are **data**, not fifteen hand-written test files each
re-deriving the same scan/baseline/escape machinery. They live in the
consuming repo under `.ratchet/`, and this binary is the engine that runs
them: at **pre-edit** time (before the write lands), at **commit** time, and
by hand.

```
.ratchet/
  laws/<name>.toml          # one rule, declared
  baselines/<name>.txt      # the ceiling it may not exceed (only ever goes down)
  fixtures/<name>/hit/…     # files the law MUST catch, listed in expected.txt
  fixtures/<name>/clean/…   # files it must stay silent on
```

#### The schema

```toml
name         = "nan-guard"                     # must equal the file stem
description  = "A float clamp is not a NaN guard"
severity     = "deny"                          # deny | warn
escape       = "// nan-safe:"                  # optional: suppresses a hit
escape_lines = 2                               # optional: how far above (default 2)
baseline     = ".ratchet/baselines/nan-guard.txt"   # optional
code_only    = true                            # optional: strip trailing // comments first

[scope]
include = ["crates/**/*.rs"]
exclude = ["**/target/**", "crates/ratchet/tests/**"]

[matcher]                                       # exactly ONE
kind    = "regex-absent"
pattern = "\\.clamp\\("
key     = "file:line-content-hash"
```

Parsing is **strict**: an unknown key, a duplicate table, a matcher key that
belongs to another kind, a regex that does not compile, or a `name` that
disagrees with the file it lives in is an error at load. A typo must not
silently disable half a rule. Scope globbing understands `*`, `?` and `**`,
`exclude` always wins, and the walk is gitignore-aware, so a law never has to
enumerate build output.

#### Matcher kinds

| kind | keys | the rule | exemplar |
|---|---|---|---|
| `line-count` | `max` | a file may not exceed `max` lines; key = file, count = lines | module-size debt |
| `regex-absent` | `pattern`, `key` | a pattern must NOT appear | the bare `.clamp(` guard |
| `regex-present` | `pattern` | every file in scope MUST contain it | a proptest that must carry an explicit seed |
| `marker-within-lines` | `trigger`, `marker`, `lines` | a `trigger` line requires a `marker` within N lines above | `// bound:` over a collection that grows |
| `registry-both-ways` | `registry_file`, `entry_pattern`, `use_pattern` | every use is registered AND every registry line is used | the dev-instrument (env switch) registry |
| `doc-path-resolves` | `pattern` | a captured `.md` path must resolve at the repo root or inside the citing file's own `crates/<x>`/`tools/<x>` unit | doc citations |
| `dep-graph-forbids` | `roots`, `forbidden`, `edges` | no root package may REACH a forbidden one (glob) through the resolved dependency graph; `edges = "normal"` (default) never follows dev/build edges, which is the whole distinction | dev-only tooling in a shipping binary |
| `file-set-containment` | `superset_file`, `subset_file`, `capture` | every capture in `subset_file` must also appear in `superset_file` | a headless stand-in whose query must refuse at least what the real one refuses |
| `json-number-ceiling` | `files`, `path`, `tolerance_pct`, `enabled_env` | a number read out of generated JSON may not exceed its baseline by more than the tolerance | a criterion bench figure nobody was reading |

The last three judge a whole TREE rather than a file at a time, and each
refuses to reach a VACUOUS verdict: a dependency walk that resolved nothing, a
capture set that came out empty, or an armed perf law with no data all fail
loudly instead of reporting green over files they never opened.

- **`dep-graph-forbids`** runs `cargo metadata --format-version 1` once and
  BFSes `resolve.nodes`, so a TRANSITIVE edge (`server -> helper -> editor`) is
  caught exactly like a direct one. The hit's key is the PATH that reaches the
  forbidden package (`server->shared->testrig`) — that is what an edge gets
  deleted from. A tree carrying a checked-in `cargo-metadata.json` is read from
  it instead, which is how the fixtures work. The verdict is cached against the
  only inputs that can change it — `Cargo.lock` and every `Cargo.toml`, by size
  and mtime — so the gate pays for the walk once per manifest change, not once
  per commit.
- **`file-set-containment`** is containment, never equality: the stand-in may
  refuse MORE than the real system, never less. A deliberate deviation puts the
  law's `escape` marker in `superset_file`, and a marker with nothing left to
  waive is itself a finding — stale waivers are how a guard quietly stops
  guarding.
- **`json-number-ceiling`** is a MEASUREMENT law: every value it reads is a
  hit, weighted by the number (rounded up), and the `tolerance_pct` is applied
  when comparing to the baseline rather than when measuring — a figure inside
  tolerance still has to lower its ceiling. `enabled_env` arms it: unset, the
  law is skipped ENTIRELY (no check and no tighten — tightening against data
  that was never generated would wipe the baseline). A glob under `target/`
  follows `CARGO_TARGET_DIR`. Its `clean/` fixture is a file the glob must
  REFUSE (criterion's `base/` copy is the natural one), which is what proves
  the reader discriminates.

`key` is `file` (baseline `<file> | <count>`) or `file:line-content-hash`
(baseline one line per occurrence, identity = file + the trimmed offending
line). Line NUMBERS are deliberately not part of the identity — inserting a
line above an offence is not a regression — while swapping one offending site
for a different one in the same file IS, which a per-file count cannot see.

#### The baseline law

A baseline is a **ceiling per key** and it only ever goes down:

- measured **above** it → a regression, reported and (for `deny`) rejected;
- measured **below** it → the run that saw the fix lowers or drops the entry
  and rewrites the file: atomically (tmp + rename), byte-stable when nothing
  moved, preserving header and mid-file comments in place and whatever line
  ending is already on disk;
- it **never raises** a count and **never adds** a key. The only way to admit
  a new hit is the law's own escape comment.

`ratchet check` tightens by default (`--no-tighten` to report only); a run
carrying `--proposed` never tightens, because the tree it measured does not
exist. The gate does not tighten either — a commit hook that rewrote a file
mid-commit would leave the lowered ceiling unstaged.

#### Baselines are never raised by hand

A baseline is a ceiling that only ever goes down, and it lives in a text file
any editor can widen — which happened: a `1048` entry was hand-edited to `1049`
to get a commit through. So the ban is mechanical. `precommit` and
`premergecommit` parse every STAGED baseline old-vs-new, in both the counted
and multiset forms, and reject a key whose count ROSE or which is NEW:

```
gate precommit: baseline-rejected: crates/ratchet/tests/module_size_baseline.txt crates/a.rs 1048 -> 1049
  baselines are written by the ratchet itself; lower the code, or use the law's escape comment
```

Lowering, removing and header edits pass — that is what a fix looks like — and
a baseline file that is new in the commit passes too, since adopting a law is
not raising a ceiling. The guarded globs default to `.ratchet/baselines/*.txt`
and `crates/ratchet/tests/*_baseline.txt`; a workspace can declare its own with
`baselines = [...]` under `[workspace.metadata.aphrollo]`, which REPLACES the
defaults.

#### Fixtures — a law nobody proved catches nothing

`ratchet test` runs each law over its own `fixtures/<law>/hit` files, requires
exactly the offences listed in `expected.txt` (`<file>:<line>` per line, or the
hit's key for a whole-tree law, which has no line to point at), and
requires `clean/` to produce none. Both directions are required: hit-only
proves a rule fires, never that it discriminates. A law with no fixtures
fails. The commit gate runs `ratchet test` whenever a commit stages anything
under `.ratchet/`.

#### Pre-edit denial

The PreToolUse hook reconstructs what a `Write`/`Edit`/`MultiEdit` would leave
on disk (old/new strings applied to the file, MultiEdit in order) and judges
that content, narrowed to the one file:

```
ratchet: nan-guard: crates/pose/src/advance.rs:212 let a = x.clamp(0.0, 1.0);
  (baseline 0, now 1; escape: // nan-safe:)
```

A `deny` law with a new hit exits 2 and the write never happens; a `warn` law
prints the line once and allows. Everything here fails **open** — a malformed
payload, an unreadable file or a broken law file must never wedge a session
over a rule that is itself broken. A narrowed run reports uses nobody
registered but never claims a registry line is stale: that needs the whole
tree.

#### Commands

```sh
aphrollo ratchet check                       # judge the tree, tighten, exit 1 on a deny regression
aphrollo ratchet check --only nan-guard      # one law
aphrollo ratchet check --format json         # what the hooks read
aphrollo ratchet check --no-tighten          # report only
aphrollo ratchet check --proposed crates/a.rs=/tmp/new.rs   # judge content not on disk
aphrollo ratchet test                        # prove every law against its fixtures
```

A repeat `check` costs milliseconds: every file's hits are cached under the
state dir, keyed by path + size + mtime **and** a hash of the law set, so a
rule that changed drops the cache instead of inheriting verdicts reached under
the old one. A repo with no `.ratchet/laws/` says `no laws` and exits 0.

### Pipeline health (`aphrollo gate stats`)

```sh
aphrollo gate stats              # the whole gate.log
aphrollo gate stats --since 7d   # just this week
```

One table: every stage (`postedit`, `precommit`, `premergecommit`) against
every outcome (`green`, `red`, `blocked`, `timeout`, `timeout-rejected`,
`queued-skipped`, `queued-rejected`, `deferred`), then the median and maximum
gate seconds and the per-crate timeout and deferral counts. A stage with no
rows still prints, so "zero timeouts" and "never ran" are not the same blank.
Read-only: it never touches the log it reads, and an unparseable line is
skipped rather than guessed at (the log is appended to by several processes).

### Disk hygiene (`aphrollo gate gc`)

Build caches this binary's own gates create and use are the biggest thing on
a Rust box's disk (a measured 417 GB `target/`, 202 GB of it
`debug/incremental`, plus orphan worktree build dirs and stray target dirs
with nothing left pointing at them). `gc` reclaims exactly six kinds of leftover and nothing else:

| category | what qualifies |
|---|---|
| idle incremental caches | `<target>/*/incremental/*` whose newest FILE is older than `--older-than` (**default 3d** — being wrong costs one recompile of that one crate) |
| dead gate dirs | `<stateDir>/failfirst-wt/<hash>` whose `origin.txt` (written at creation) names a repo that no longer exists |
| stale lock litter | orphan `.owner` records in the temp dir, idle **> 1 day** (`--lock-age`), whose lock nobody currently holds — the acquire attempt IS the liveness test — plus this binary's own `aphrollo-*-stub-*` / `*-pkgtest-*` test dirs. A `.lock` file itself is NEVER deleted: it is the mutual exclusion, and on Windows a delete-pending name makes the next open fail, which reads as "acquired" |
| stale build artifacts | cargo never deletes a SUPERSEDED metadata hash, so `deps/` keeps one set of outputs per worktree path and per profile change forever (borld measured 2026-09-02: `target/debug/deps` at 207 GB / 24,260 files, 234 distinct `server-<hash>` fingerprints). Two tiers by what a rebuild COSTS: **workspace members at 3d** (they relink in seconds) and **third-party artifacts at 14d**. Matches only cargo's own `<crate>-<hash16>` shape in `deps/`, `.fingerprint/`, `build/` and `incremental/`; anything else is left alone |
| mutants tree copies | `<target>/mutants/*` older than 1d, and ONLY while no `cargo-mutants` process is alive (those copies are the trees a live run is testing) |
| orphan worktree builds | a directory beside a repo's registered external worktrees that holds nothing but `target/` — git dropped the worktree, the build dir survived |
| stray target dirs | a directory at depth 1 under the repo root or a registered worktree root that carries cargo's own `CACHEDIR.TAG` **and** `.rustc_info.json`, is **not** the resolved target dir, and is idle **> 3d** — a hand-made `target-sky/` nobody builds into any more (33 GB found on one box). Both marker files are required, so a cache that merely carries a tag is never proposed; it takes **no build slot**, because by definition nothing is compiling into it |

```sh
aphrollo gate gc                      # dry run: path, size, reason, total
aphrollo gate gc --apply              # delete them
aphrollo gate gc --older-than 14d     # be stricter about incremental caches
aphrollo gate gc --apply --lock-age 1h  # clear today's lock litter on an idle box
```

`deps/` is reclaimable ONLY through the artifact rules above: by cargo's own
`<crate>-<hash16>` stem and an mtime bar, never by name and never wholesale.
A **registered worktree is never touched**, a gate dir with no `origin.txt` is
UNKNOWN and left alone, and every deletion inside a target dir happens while
this process HOLDS that target's build lock, so a build mid-way cannot lose an
rlib it is about to link. The dry run reports a total per tier, because the
tiers carry different risk.

Two things run it for you:

- **After a successful `git worktree remove`/`prune`** the git shim sweeps
  immediately and silently: the removed tree's leftover `target/`, any other
  orphan build dir beside the registered worktrees, and the gate dirs whose
  root just disappeared. Incremental caches are NOT in scope there.
- **At session start**, at most once per 24 h, `gate sessionstart` launches a
  DETACHED `gc --apply` (never inline — it must not stall the hook) and the
  NEXT session surfaces one line saying what the last sweep reclaimed.
  Lock litter is swept there too. Incremental pruning holds a build slot for the target dir while it deletes,
  and is skipped silently when every slot is busy — the next sweep gets it.

## Setup — `aphrollo gate init`

One command wires the whole gate — the native replacement for
`claude-code-tdd`'s `install.sh`:

```sh
aphrollo gate init                    # session hooks + global git gate
aphrollo gate init --no-git           # session hooks only (skip the git gate)
aphrollo gate init --uninstall        # remove everything again
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
   layer. The gate has no pre-push stage, so a re-init also **prunes** any managed
   `pre-push` shim it finds, leaving hand-written hooks untouched.

It resolves the invoking binary via `os.Executable`, so the installed hooks
call the same binary that wrote them; ansible runs it once per session HOME.

For a single repo without the global gate, `aphrollo gate install --apply` writes
the same shims into that repo's `.git/hooks` instead (opt-in, no `core.hooksPath`).

The gate is **solely mechanical**: edit-time smell blocks + commit-time
anti-cheat/fail-first/suite. Adversarial review lives in the reviewer agent,
not this binary. Mutation
testing is intentionally **not** ported (false-positive/non-determinism prone);
the fail-first + mechanical suite cover the same ground without the flakiness.

### The `tdd` → `gate` rename

The subcommand family is `aphrollo gate …`; `aphrollo tdd …` stays a **silent
alias** for one release so hooks and shims written before the rename keep
working. Run `aphrollo gate init` once to rewrite them: `settings.json`'s
session hooks, `~/.config/git/hooks/*`, the per-repo hooks and the
`cargo-queue` shims all move to `gate`, and the installers recognise BOTH
spellings so a re-init replaces its own older entries instead of stacking a
second hook beside them. The state dir moves `~/.claude/tdd-state` →
`~/.claude/gate-state` by MOVING the existing directory the first time any
gate runs — gate.log history, the mechanical cache and the receipts come with
it. Hook output lines are prefixed `gate:`; law lines are prefixed `ratchet:`.
The control command answers to both `/gate` and `/tdd`.

## Layout

```
cmd/aphrollo/        entry point (one-liner over internal/cli)
internal/cli/        arg parsing + subcommand dispatch (testable Run)
internal/refactor/   orchestration: detect lang → spawn server → rename/refs/outline/show
internal/lsp/        LSP types + JSON-RPC stdio client (framing, Conn, edits)
internal/diff/       deterministic unified-diff renderer
internal/guardrail/  PreToolUse policy (block long waits, warn on noisy output)
internal/ratchet/    Law engine: schema + strict loader, matchers, self-tightening baselines, fixtures
internal/tdd/        TDD + law gates (mechanical-only): policy engine, edit smells, anti-cheat, RED/GREEN, fail-first, install
internal/workspace/  worktree lifecycle (create/claim/unclaim/list/remove/prune) + git verbs (commit/push/submit/update/diff/pr/ship/merge)
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
