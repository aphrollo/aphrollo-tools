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
aphrollo workspace push                     # push; reuse an existing PR if there is one
aphrollo workspace submit -m "<summary>"    # opens the PR READY (or flips a draft) — the handoff
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

# push: git push -u origin HEAD; reuse the PR's state in the receipt if one exists
aphrollo workspace push
# pushed feat/kanban -> origin (2 commit(s))
#   https://github.com/aphrollo/aphrollo-web/tree/feat/kanban
# no PR yet for feat/kanban — run: aphrollo workspace submit
# ci none

# submit: push (idempotent) → open the PR READY (or flip a legacy draft) → set body
aphrollo workspace submit -m "Kanban drag-and-drop. Closes #200."
# opened PR #321 ready for review: https://github.com/aphrollo/aphrollo-web/pull/321
#   already in sync
#   ci pending — review arms when green
#   handoff in_progress -> review
```

- **commit** stages `git add -A` by default (`--staged-only` to commit the index
  as-is) and runs the [TDD pre-commit gate](#tdd--law-gates-aphrollo-gate); `--no-verify`
  is the documented escape for the gate's known false-positives. A clean tree is a
  reported no-op, not an error.
- **push** sets the upstream on a first push and reports the ahead-count + branch
  URL. It **never opens a PR** — `submit` is the sole opener, so CI fires exactly
  once, at the handoff, instead of once on a draft's `opened` event and again on
  `ready_for_review`. If an OPEN PR already exists for the branch, push reuses
  it in the receipt (number/url + the current CI state); if none exists yet, it
  says so and points at `submit`. `--force-with-lease` for a rebased branch.
- **submit** is the **one-shot handoff** that moves a card `in_progress →
  review`. It pushes (idempotent), reads the branch PR's CI **inside the verb**
  (the only `gh` check-state read, so the caller needs no extra call), and is the
  **sole opener** of the PR: when none exists yet it opens one **READY** (never
  draft), when a draft already exists (a legacy or hand-opened PR) it flips it
  ready, and when one is already ready it is a no-op `[skip]`. The open/flip
  happens unconditionally — on green, red, or pending CI alike — and sets the PR
  body to `-m`'s summary; a problem (red CI, a merge conflict, still-pending CI)
  is repaired by a follow-up `push` to the same PR, never a re-submit — the
  receipt names it (`ci red (<k> failing) — push a fix`, `merge conflict —
  rebase onto <base> + push`, `ci pending — review arms when green`). The
  server-side `AllOpenGreen` gate, not this verb, is what actually holds the
  reviewer until CI is green. GitHub computes mergeability async, so the read
  briefly re-polls past the `UNKNOWN` window; if it never resolves it reports
  `mergeable: unknown — push to recheck` rather than a false all-clear. `push`
  surfaces the same conflict as a non-fatal `CONFLICT:` warning line.
  submit is **per-worktree, one repo at a time**: it acts on the cwd worktree's
  single PR. There is **no ticket-level submit** that fans out across repos; a
  ticket that spans repos is submitted one worktree at a time.

> `pr` and `ship` still exist as operator escapes (they take an explicit
> `<repo> <branch>` and also default to the cwd worktree), but the coder flow is
> `push` then `submit` — not the discrete `pr`/`ship`. `submit`, not `push`, is
> the one that opens the PR.

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
  - HEAD **is** the default branch → `git merge --ff-only origin/<default>`.
    Git, not a pre-check, judges whether that is safe: an unrelated dirty file
    is carried across untouched, and only a dirty path the incoming commits
    themselves touch makes git refuse — sync prints git's own reason
    (`main could not fast-forward: <git's reason>`) and **exits 0**;
  - the default branch is **not** the checked-out one → advance its ref with
    `git update-ref` (no checkout, so a sibling worktree's files are untouched).
- It **never** `reset --hard`s, forces, or moves the branch when it has
  **diverged** (local commits ahead). A diverged clone is **left untouched**
  with a clear reason and **exit 0** (best-effort). The fetch still happens in
  that case. Only the default branch is synced — ticket branches and other
  worktrees are left alone. `--dry` previews and mutates nothing.

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
| `gate pretooluse` | Claude PreToolUse hook (stdin) | Blocks (exit 2) a **test-file** edit introducing an oracle smell — real-time sleep, tautological self-comparison, focused marker (`.only`/`fit`), or a disabled test (`.skip`/`xit`/`t.Skip`/`@pytest.mark.skip`). Judged over the lines the edit ADDS, so a file that already carries one (a platform skip) is still editable; a genuinely necessary one is admitted by `// skip-ok: <why>` or `// real-time: <why>` on its line or the line above, and logged `smell-escape:<policy>`. **Warns** (test or source) on a suppression that silences a quality gate (`//nolint`, `@ts-ignore`, `# type: ignore`, coverage-ignore). |
| `gate posttooluse` | Claude PostToolUse hook (stdin), matching the edit tools AND `Bash` | Runs the edited file's related tests as a build phase then a run phase under ONE budget, deferring whatever does not finish (see below); surfaces a RED summary. **Silent unless RED.** Source extensions include `.ron` — in a Rust workspace those are registries and fixtures whose edits change behaviour, resolved to the owning crate exactly as `.rs` is. |
| `gate userpromptsubmit` | Claude UserPromptSubmit hook (stdin) | Intercepts `/gate [status\|off\|on\|reset]` — the per-session enforcement escape hatch. On any other prompt, re-injects the last RED outcome for the cwd's project so the gate survives context compaction. **Silent unless RED.** |
| `gate stats` | manual | Tallies `gate.log` by stage and outcome, with per-crate timeout/deferred counts and median/max gate seconds (`--since 7d`), the open escape count and its oldest, and any `demote-candidate:` check. Read-only: it names the candidates, and `gate escape sync` is what opens their issues. |
| `gate escape` | manual + the `escape-closure` CI job | `record` a red that arrived after a local green, `sync` the ones recorded offline (and open the false-positive issue for a demotion candidate), `list` the open ones, `verify-closure <pr>` to refuse a PR that closes one without changing a check. See [The escape loop](#the-escape-loop-aphrollo-gate-escape). |
| `gate runphase` | spawned by `gate posttooluse` | The detached build/run phase's wrapper: holds the build slot, logs to the state dir, writes the result file the next hook harvests. Never typed by a human; never blocks. |
| `gate sessionstart` | Claude SessionStart hook (stdin) | Injects the TDD-skill nudge, the previous session's disk-sweep result when it freed something, and — for a repo with a workspace manifest and no laws dir under `.ratchet` (an empty dir counts as none) — ONE line saying the gate is running suites only and pointing at [Ratchet laws](#ratchet-laws-aphrollo-ratchet). Never more than one extra line each, never blocks. |
| `gate sessionend` | Claude SessionEnd hook (stdin) | Deletes the per-session state file so the state dir doesn't accumulate. |
| `gate precommit` | git `pre-commit` | Blocks a newly-**added** suppression (anti-cheat). Then **fail-first**: a commit adding both tests and source must have tests that fail without the source. Then the suite must pass. A worktree state already proven green under the exact same command (by a PostToolUse run or an earlier gate pass) is **not re-run** — the cache is keyed on the repo's git COMMON dir, so every linked worktree of one repo reuses the same proven-green facts — only green results are cached, keyed on content + runner argv (content covers tracked files AND the ignored configuration a suite reads: dotenv files and `config/` trees, never build output), so a red always re-runs with fresh output. Both gate stages build in the REPO'S OWN target dir (see below). |
| `gate commitmsg` | git `commit-msg` | Rejects a commit whose MESSAGE carries a deny pattern, quoting the offending line. Opt-in per workspace (`undercover = true`); absent key = pass through. Fires for merge commits too. |
| `gate postcommit` | git `post-commit` | Writes `refs/notes/gate` on the commit just made — `green <tree>` — when a root group's suite actually RAN green for exactly that tree. A cache hit is not that, so an amend (which re-runs the gate and hits the cache) leaves no note, which is the right answer for a commit no suite has run against. The note is what lets CI tell a red on a gated tip from a red on an ungated one; the git shim pushes the ref alongside a branch push. Then, for a commit on a lane branch in a repo opted in (`mutation-receipt = true`, in `[workspace.metadata.aphrollo]` for a Cargo workspace or a root `aphrollo.toml` otherwise), starts that lane's mutation run detached and at below-normal process priority — spawned by `gate mutants run --job <file>`. The same verb WITHOUT `--job` is the hand-typed entry point: it builds the same job for the checkout it is standing in and runs it in the foreground, under the same box-wide lock. A commit on `main`/`master`, a repo not opted in, or a box the run cannot fit on a drive skips silently. Never blocks — the commit already exists. |
| `ratchet check` | git `pre-commit`/`pre-merge-commit`, and manual | Judges the tree against `.ratchet/laws/*.toml` (see [Ratchet laws](#ratchet-laws-aphrollo-ratchet)). |
| `gate prepush` | git `pre-push` | **No-op** (mechanical-only mode). The gate is solely mechanical now; adversarial review is owned by the separate reviewer agent, not this binary. Kept only so a `pre-push` shim lingering from before the change exits cleanly — it **never blocks**. |
| `gate premerge` | git `pre-merge-commit` | Runs ONLY the mechanical stage over the merge's staged files — no fail-first (a fresh test's RED/GREEN belongs to the authoring commit, already proven by `precommit` there) and no anti-cheat suppression scan (same reasoning) — so a git merge, which never fires `pre-commit`, still proves the COMBINED result compiles and passes before it lands. `gate premergecommit` is the pre-rename spelling, kept as a silent alias for one release; every line the routine prints starts `gate premerge:`. |
| `gate allow` / `gate revoke` | manual | `allow <wall>` waives a wall for the session (`primary` today; `discard` joins later); bare `allow` (or `revoke`) lists the active waivers. See [Waivers](#waivers) below. |

#### Waivers

A wall's refusal and its doc read the same, because every wall shares one
mechanism: `aphrollo gate allow <wall>` waives it for
the session, `aphrollo gate revoke <wall>` restores it, and a bare `gate
allow` (or `gate revoke`) lists every active waiver as `<wall> since
<RFC3339> by <session>`, or `no waivers`. The scope is a property of the
wall, not of the verb — `allow primary` is session-scoped, because a lane's
worth of edits needs it; a later wall can be one-shot instead. `primary` is
the primary-checkout merge-only rule (worktrees stay editable; the checkout
holding `main` refuses a write when the repo has any linked worktree);
`gate primary-edits on|off` and `/tdd primary-edits on|off` are the
pre-rename spellings, kept as silent aliases for one release.

#### Gate stage order (cheapest first)

`precommit` and `premerge` (alias: `premergecommit`) run the same pipeline
per project root and **stop at the first rejection**, so a formatting slip
costs milliseconds instead of a full test build:

| # | stage | cost | notes |
|---|---|---|---|
| 0a | baseline guard — a STAGED baseline that ROSE | ms (one `git show` per staged baseline) | rejects `baseline-rejected`; see below |
| 0b | `ratchet check` — the repo's declared laws | ms (mtime cache) | only when the laws dir under `.ratchet` exists; rejects `ratchet-rejected`. A commit that stages a `.ratchet/` file also re-proves every law against its fixtures |
| 0c | `docs check` over the staged `*.md` | ms | rejects `docs-rejected`; only for a repo that asked (below) |
| 1 | `cargo fmt --check -p <touched>` | ms | compiles nothing, takes no build slot |
| 1f | gofmt, in-process (Go roots) | ms (no process spawn) | judges the STAGED (index) blob of every touched `.go` file with `go/format`, never the bytes on disk — a Windows checkout whose tracked files predate this repo's `.gitattributes` may still hold CRLF there, and a CRLF file is never gofmt-clean. Rejects naming the file(s); the fix is `gofmt -w <file> && git add <file>` (the stage judges the index, not the working tree), or — for that old-checkout case specifically — a ONE-TIME `git add --renormalize .` so the index picks up the LF `.gitattributes` now demands. Nothing to renormalize is a silent no-op |
| 1g | `go vet ./...` (Go roots) | seconds | CI parity: the gate must run the checks that decide whether the branch is green |
| 2g | `golangci-lint run --allow-serial-runners <touched packages>` (Go roots) | a full analysis pass | scoped to the packages the commit actually touches (`.` for the root package, `./dir` per distinct package below it) rather than the whole-module wildcard — the same reasoning as clippy's `-p <touched>` above: a two-file commit re-analyzing the whole module pays for every package it did not touch. Skipped with ONE `lint-skipped` log line when the binary is not installed — never a rejection over a tool nobody has. `--allow-serial-runners` because golangci-lint takes a MACHINE-WIDE lock: a second one anywhere on the box otherwise makes this one exit 3 with "parallel golangci-lint is running", a rejection that says nothing about the code. A local version that differs from the one the workflow pins logs ONE `lint-version-drift` line and still runs — a mismatch is not a defect in the code, but a green commit followed by a red CI job is the failure this stage exists to prevent |
| 2 | `always-run` packages, their OWN invocation | seconds | a pure guard crate; bundling it into `-p ratchet -p client` made it wait for client to link |
| 3 | `cargo clippy -p <clippy-clean> --tests -- -D warnings` | front-end build | only crates declared clippy-clean |
| 4 | `cargo clippy --workspace --tests -- -D clippy::disallowed_methods -D clippy::disallowed_types` | check-level, tens of seconds warm | no codegen, but it sees EVERY crate: a lane that broke a crate nobody staged used to land green (borld's `forge_jbeam` conformance test reached main not compiling). clippy SUBSUMES check, so a compile error fails here too, and denying exactly those two lints is what makes a `clippy.toml` law reach crates that are not on the `clippy-clean` list. Everything else stays at its default level. Rejects `check-rejected` (does not compile) or `lint-rejected` (banned API) |
| 5 | fail-first RED proof | worktree build | precommit only, and only when the staged tests ADD a declaration |
| 6 | touched crates' suites | full build + link + run | the heaviest, and therefore last |
| 7 | `cargo test -p <touched> --doc` | one rustdoc run per crate that has a doc fence | **nextest does not run doctests at all**, so a `compile_fail` proof — the only way to assert something must NOT compile — would never execute. Scoped by a grep of the crate's `src/`: a crate with no doc fence buys no run |

When the workspace's nextest configuration declares a `[profile.gate]` table,
every GATE nextest run (the touched crates' suite, the always-run guards, and
the fail-first proof) passes `--profile gate`, while `posttooluse` keeps the
default: the gate runs while other sessions build, and a CPU-bound test that
takes 40-50 s alone walks past nextest's 60 s default under that load — one
profile fixes that in one place, where the alternative was a growing habit of
per-test exemptions that weaken the suite permanently. An edit-time run keeps
the default deliberately, so a slow test is still reported rather than hidden.

Stages 2, 4, 6 and 7 are all short-circuited by the green cache (same content +
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

#### A shell edit reaches the same hooks (matcher `Bash`)

`sed -i`, a heredoc, `gofmt -w`, a generator — every one of them edits the tree
without firing the edit hooks, so the gate used to stop at the shell: the same
change denied through `Edit` landed silently through `Bash` and no suite ran.
`gate init` therefore wires a SECOND group on both edit events, matching `Bash`:

- **PreToolUse** snapshots the repo the command runs in — `git status
  --porcelain` plus the size and mtime of every tracked SOURCE file — into the
  session state. No repo, no snapshot; it never blocks, and it cannot deny a
  smell, because the content it would judge does not exist until the command
  has run (the commit gate is where a smell introduced through the shell is
  caught).
- **PostToolUse** diffs that snapshot and puts every changed source or test
  file (`ClassifyFile != Ignore`) through the identical post-edit path an
  `Edit` takes, leaving one `bash-edit:<file>` line in gate.log per file.

Two bounds. A project's suite runs ONCE however many files the command
rewrote — the suite answers for all of them at once, so a per-file loop would
charge the same build twenty times — and the call stops after ONE deferred
build, since twenty detached builds from a single `gofmt -w .` would all
describe a tree that had already moved on. The snapshot is consumed by the
PostToolUse that reads it, so a stale one never attributes somebody else's
change to a later command.

#### State files carry a schema

Every JSON file the gate writes — the session state, a deferred job and its
phase result, the mechanical green cache — carries `"schema"`, and `gate.log`
carries a sibling `gate.log.meta`. Two binaries can share one state dir (a box
mid-upgrade, two checkouts), and without the stamp a reader has only two
options and both are wrong: guess at unknown fields, silently dropping what the
other binary recorded, or reset the file, destroying it. So a file at a NEWER
schema reads as ABSENT and is left exactly where it is — nothing is written
back over it — with one `state-newer:<file>` line in gate.log. JSON that does
not parse at all is renamed to `<file>.corrupt-<ts>` and logged
`state-corrupt:<file>`, once per process: a torn write is evidence, and a state
file that vanished silently is the one failure nobody can debug afterwards.
`gate stats` refuses to tally a `gate.log` written at a newer schema rather
than produce a wrong number.

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
| `<crate>/src/lib.rs`, `<crate>/src/main.rs`, `<crate>/src/a/mod.rs` | `--lib` (no filter: that IS the crate/module) |
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
different constraints, and each lives where its SCOPE is — not in the per-user
temp dir, where a second account got a private copy of both and the box quietly
ran twice the builds the governor allows:

```
<target-dir>/.aphrollo/build.lock                 # one build per target dir, shared by construction
%ProgramData%\aphrollo\locks\aphrollo-cargo-slot.<i>.lock   # N global slots (Windows)
/var/tmp/aphrollo-locks/aphrollo-cargo-slot.<i>.lock        # N global slots (elsewhere)
```

The machine-wide dir is created world-writable and sticky, and lock files are
created `0666`: a lock file a second account cannot even OPEN reads as HELD
forever, because the open fails closed on purpose. When neither shared
location is writable the slots fall back to the temp dir with a warning —
builds then serialise per user only. `SetLockDirForTest` still moves every lock
file in one statement (a per-path override is one a test can forget, and
forgetting is what left 871 stale locks in `%TEMP%`); under that override the
per-target lock keeps the old flat, hashed layout, because a test names target
dirs that must not be created.

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
sharing a checkout never collide on git's own `index.lock`. **The lock is keyed by
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

### The queue shims are executables, not batch files

The queue dir shadows `cargo` and `git` on PATH. On Windows that shadow used to
be a `.cmd`, and a batch file cannot forward an argument list: cmd.exe strips
`^` (so `git rev-parse MERGE_HEAD^{tree}` arrived as `HEAD{tree}` and every
merge was refused for having no lane tip, and nextest's `-E test(/^mod::/)`
would arrive mangled the same way) and re-splits anything quoted.

So the Windows shims are **copies of the aphrollo binary** named `cargo.exe`
and `git.exe`, and the binary dispatches on the name it was invoked under:
`cargo`/`cargo.exe` runs `gate cargo`, `git`/`git.exe` runs `gate git`, any
other name reads its arguments as usual. No interpreter sits between the caller
and the process, so the argv arrives verbatim. The extensionless POSIX `cargo`
and `git` sh scripts stay for Git Bash.

`gate init` installs the copies, refreshes one whose size or mtime drifted from
the binary, and **deletes `cargo.cmd`/`git.cmd`** under both `--uninstall` and a
normal run, reporting each removal. A copy that is currently running cannot be
replaced; that is reported and the old copy keeps working until the next init.
The gate's own git resolution skips the whole queue dir rather than only shim
scripts — a `git` lookup tries `git.exe` first, and the exe shim reads as an
opaque binary.

A merge the gate rejects leaves `MERGE_HEAD` behind, refusing every other session
sharing the checkout until someone runs `git merge --abort` by hand. The shim recognizes
its own rejection (a fresh marker, no real conflict) and runs that abort for you, printing
one `gate: merge rejected` line — a real conflict or an unrelated failure is left untouched.

### Cargo workspace metadata (`[workspace.metadata.aphrollo]`)

The opt-in keys, declared in the workspace's own `Cargo.toml` so they version
with the code they police and are reviewed in the same diff:

```toml
[workspace.metadata.aphrollo]
always-run   = ["ratchet"]            # run these packages' suites on EVERY mechanical stage
clippy-clean = ["server", "shared"]   # gate these on `clippy -D warnings` at commit
undercover = true                    # reject commit messages that name the tooling
commit-message-deny = ["^WIP:"]      # this repo's own extra deny patterns
sdd-dir = "docs/sdd"                 # where the `sdd` skill puts a feature's spec tree
docs-check = true                    # judge staged *.md for dangling repo-relative citations
issue-labels = ["netcode", "gameplay", "physics", "animation", "client-ui", "quality", "product"]
```

- **`issue-labels`** (string array) — the themes this repo files open points
  under, and the list `aphrollo gate issue --label` and `gate escape record
  --label` judge a label against. A label outside it is refused with the list
  in the message, because the common case is a typo and a typo opens a theme
  nobody ever filters on; `--new-label` admits a deliberate new one. A repo
  that declares no list is not checked at all. A repo that is not a cargo
  workspace declares the same key as `[aphrollo] issue-labels` in an
  `aphrollo.toml` beside its root.
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
- **`mutation-receipt`** (bool) — turns on the merge gate's receipt check. Read
  from whichever manifest the repo has: `[workspace.metadata.aphrollo]` in a
  Cargo workspace's `Cargo.toml`, `[aphrollo]` in a root `aphrollo.toml`
  otherwise.
  Fail-first proves a test FAILED once; it says nothing about whether the
  test constrains behaviour, and a test that asserts nothing satisfies
  fail-first perfectly. A MERGE needs both. With the key set,
  `premerge` (alias: `premergecommit`) looks up `<stateDir>/mutation-receipt.<tip_tree>.json`,
  where `<tip_tree>` is the LANE TIP's tree (`git rev-parse MERGE_HEAD:`
  — never the merge result, which nobody has mutation-tested). The file is
  written by the consuming repo's own mutation run (borld's
  `mutation_gate.sh`). The schema, exactly as the producer writes it:
  `repo` (string — a directory name or a path to the repo/git dir, compared by
  name), `branch` (string), `tip_tree` (string), `worktree_dirty` (bool),
  `base_ref` (string), `base_sha` (string), `mutants_total`, `caught`,
  `timeout`, `unviable`, `accepted` (ints), `survivors` and `unaccepted`
  (ARRAYS of mutant names — the count is the array's length; the entries are
  opaque to the gate), `verdict` (string) and `finished_at` (RFC3339). A real
  receipt is checked in at `internal/tdd/testdata/mutation-receipt.borld.json`
  and decoded by the suite, because a schema whose only reader is its own
  writer is untested by construction — this pair disagreed in production
  (`survivors` declared an int against an array) and refused every merge.
  The merge is
  refused (`receipt-rejected`) when there is no receipt for that tree, when
  `worktree_dirty` is set, when `verdict` is anything but `"pass"` (an
  unrecognised verdict refuses — a gate that reads an unknown word as
  permission is not a gate), or when `unaccepted` is non-empty. `base_sha` is
  what `base_ref` RESOLVED to when the run took its diff: a ref name is not a
  base (`origin/main` moves), so a receipt that carries one must match
  `git merge-base MERGE_HEAD HEAD` or the merge is refused — it measured
  different lines. A receipt with no `base_sha` is an older producer's: it is
  accepted and logged `receipt-unpinned`, so an unverifiable proof is counted
  rather than mistaken for a verified one. The receipt is
  keyed by TREE in its FILENAME, so the lookup itself is the identity check and
  two lanes measured minutes apart never read each other's answer;
  `mutants_total: 0` is a valid receipt, since a diff with nothing mutable in
  it is a real answer. `unaccepted`'s entries are opaque to the gate — the
  producing repo decides how it names a mutant — and only the first is quoted
  in the rejection, which also names the command that produces a receipt. It
  runs BEFORE any suite compiles.
- **`mutants-local`** (bool, default `true`) — where the proof is MEASURED.
  A Cargo repo has no runner that will do it, so the post-commit hook starts a
  detached run on the box and the key can stay unwritten. A repo whose pipeline
  can run the tool says `mutants-local = false` and the hook stops: a Go mutant
  is judged by re-running its WHOLE package (26 s for `internal/tdd` on the
  Linux runner against 207 s on a Windows box, times the 1626 mutants gremlins
  finds in that one package), so which machine measures decides whether the
  proof is affordable. Only an explicit `false` turns it off — a repo that has
  said nothing keeps the behaviour it has.
  **`false` also stands the MERGE gate down.** It has to: the post-commit run is
  the only producer of a local receipt, and the one the runner writes is signed
  with the RUNNER's machine key, so a gate that kept demanding one would refuse
  every lane merge forever. `mutationReceiptStage` logs `receipt-measured-in-ci`
  and passes; the required CI check is what refuses the merge instead. So the
  pair `mutation-receipt = true` + `mutants-local = false` means "the proof is
  required, and CI is the judge" — do not set the second without a pipeline job
  that runs `gate mutants go --diff`, or nothing judges the lane at all.
- **`mutation-accept`** (string array) — the survivors somebody signed off on,
  each `"<file>:<line> <MUTATOR> # why it is acceptable"`. The reason is not
  decoration: an entry without one is not an accepted survivor. This is the
  list `aphrollo gate mutants go --diff <base>` judges against.
- **`aphrollo gate mutants go --diff <base> [--receipt <path>] [--store <dir>]`**
  is the CI half of the Go runner: it runs gremlins over `<base>..HEAD` in the
  current checkout, writes and signs the same receipt a local run writes, and
  EXITS NON-ZERO on a survivor the accept-list does not carry. gremlins' own
  exit code is not the verdict — it fails a run that misses its efficacy
  threshold, which is a bar about the whole module, and the bar here is the
  accept-list. Exit 2 is a bad invocation (no base: an unscoped run measures
  everything), exit 1 is a failed check, a run that produced no report, a
  mutant that timed out (an unmeasured mutant is not a result), or a run that
  measured ZERO mutants over a diff that DID change production Go — a scope
  matching nothing is what a stale base looks like. A zero is a real answer
  only when there was nothing to mutate, so the runner lists `<base>..HEAD`
  first: if no changed file is a non-test `.go` outside a `testdata` tree it
  writes a signed zero-mutant receipt, prints `0 mutable Go lines in
  <base>..HEAD: nothing to judge` and exits 0 without starting the tool. A
  diff git cannot read counts as mutable: "I could not tell" is never the
  reason a check passes. `--store <dir>` overrides where the outcome cache
  (see [The outcome cache](docs/mutation-runner.md)) reads and writes
  `outcomes.json`, in place of the machine-local `<gate-state>/mutants/<repo-token>/`
  a detached local run uses — CI has no persistent gate-state directory
  between jobs, so this repo's own pipeline (`.github/workflows/pipeline.yml`)
  points `--store` at an `actions/cache` path keyed on the head branch
  instead, carrying prior verdicts across pushes to the same PR. Omitted, CI
  still runs, it just measures every mutant fresh each push. The same verb
  with `--job <file>` instead is the detached local run. aphrollo-tools runs it as
  the required `mutants` check in `.github/workflows/pipeline.yml`.
- **`docs-check`** (bool) — turns on the staged-markdown citation stage for a
  cargo workspace. A Go module is opted in by being one (aphrollo's own CI
  already runs the check), and any repo can opt in with a `.aphrollo/docs-check`
  file at its root. Doc conventions are not universal, so a repo that said none
  of the three is never judged on them.
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

<!-- ratchet-spec:begin -->
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
schema       = 1                               # optional: the law schema this file is written for
name         = "nan-guard"                     # must equal the file stem
description  = "A float clamp is not a NaN guard"
severity     = "deny"                          # deny | warn
escape       = "// nan-safe:"                  # optional: suppresses a hit
escape_lines = 2                               # optional: how far above (default 2)
baseline     = ".ratchet/baselines/nan-guard.txt"   # optional
code_only    = true                            # optional: strip trailing comments first
comment_prefix = "#"                           # optional: what opens one (default "//")
contiguous   = true                            # optional: suppression must be in the comment run above
trigger_exclude = "^\s*(pub )?use "            # optional: lines that can never be a trigger

[scope]
include = ["crates/**/*.rs"]
exclude = ["**/target/**", "crates/ratchet/tests/**"]
ignore_gitignore = false                        # optional: judge gitignored files too
min_files = 40                                  # optional: fewer matched files is a regression
alias = "tier1"                                 # optional: base Include on a named set (below)

[matcher]                                       # exactly ONE
kind    = "regex-absent"
pattern = "\\.clamp\\("
key     = "file:line-content-hash"
```

Parsing is **strict**: an unknown key, a duplicate table, a matcher key that
belongs to another kind, a regex that does not compile, or a `name` that
disagrees with the file it lives in is an error at load. A typo must not
silently disable half a rule.

`schema` is the exception, and only in one direction. It is the version this
law file is written for; absent means `1`, which is every law written before
the key existed. A law declaring a version ABOVE the one the binary supports
is read **leniently** — the keys the binary knows still apply, the ones it has
never heard of are skipped, and it is never a hard error, so a repo whose laws
moved ahead of a box's binary does not wedge that box. It is not silent
either: the run prints one line naming the law and both versions, and the gate
leaves `ratchet-law-newer:<law>` in its log, because a rule read with half its
keys skipped otherwise reports clean exactly like a rule that is being obeyed.
At the supported schema an unknown key stays an error — that is the typo
protection, and it only makes sense where the binary claims to understand the
file. A `schema` that is not a positive integer is a broken law, not a future
one. Scope globbing understands `*`, `?` and `**`,
`exclude` always wins, and the walk is gitignore-aware, so a law never has to
enumerate build output. A repo that ignores a whole extension hides the files
some laws are entirely about (borld ignores `*.md`, which is every doc a
citation law reads) — `ignore_gitignore = true` opts THAT law into the ignored
files, and the fix is never to weaken the repo's `.gitignore` for a guard's
benefit. `.git` is never walked, opt-out or not.

**Scope aliases.** A `scopes.toml` file under `.ratchet/` names reusable file
sets so several laws that share a boundary (Tier-1, presentation, …) state
the glob list once:

```toml
[sets]
tier1 = ["crates/movement/**", "crates/pose/**", "crates/numeric/**"]
```

A law's `[scope].alias` resolves to that set's globs, merged as the BASE of
its own `include` (a law may still widen further with its own entries; both
apply). `ratchet check` refuses a law whose alias names a set `scopes.toml`
never declares — one line naming the law and the alias — and reports, as a
warning, a set no law's alias uses. `ratchet test` resolves an alias the same
way, through the same `LoadLaws` call, so a fixture proves a law through its
alias exactly like it proves one through a literal `include` list.

A scope is a claim about coverage, so the engine judges it too. `min_files`
is the floor below which a clean verdict is not a verdict: a law whose globs
quietly stopped matching (a crate renamed, a `**` dropped) reports green over
files it never opened, and falling under the floor is the finding
`scope-floor`. An `include` entry that names ONE file rather than a set is a
citation: when it is gone the finding is `missing-scope-file | <path>`, which
says something different from "the set is empty". Both are whole-tree
questions, so a narrowed run (`--proposed`, `--files`) does not ask them.

Suppression is bounded by structure, not by arithmetic. `contiguous = true`
makes an `escape` or a `marker` count only when it sits on the trigger's own
line or in the COMMENT RUN directly above it — consecutive comment lines and
single-line attributes, broken by the first code or blank line. Counting lines
instead lets one `// nan-safe:` exempt an unrelated call four lines below,
across code it says nothing about. `comment_prefix` is what opens a comment in
the language being scanned, so a TOML or shell law strips `#` comments and a
commented-out entry stops satisfying a `regex-present` law. `trigger_exclude`
disqualifies a line from ever BEING a trigger, which is what an import needs:
putting `use` in the marker regex instead exempts everything in the window
below the import.

`direction` says WHERE the marker lives: `above` (default) is the
comment-above-the-declaration shape, `below` is a block that carries its own
configuration — a `proptest!` block's `#![proptest_config(…)]` sits on the NEXT
line, and looking up only reported 30 seeded blocks as unseeded — and `both`
accepts either. `contiguous` applies in whichever direction is chosen.

#### Matcher kinds

| kind | keys | the rule | exemplar |
|---|---|---|---|
| `line-count` | `max`, `count`, `unit_split` | a file may not exceed `max` lines; key = file, count = lines (`count = "code"` drops blank and comment-only lines, by the file's own `//`/`/* */`/`#` syntax) | module-size debt |
| `regex-absent` | `pattern`, `key`, `count` | a pattern must NOT appear; `count = "matches"` counts every call on a line, not the line | the bare `.clamp(` guard |
| `path-regex-absent` | `pattern` | the repo-relative PATH must not match; key = the path, no line | a filename carrying a plan-item stamp or a serial letter |
| `regex-present` | `pattern` | every file in scope MUST contain it | a proptest that must carry an explicit seed |
| `marker-within-lines` | `trigger`, `marker`, `lines`, `contiguous`, `direction` | a `trigger` line requires a `marker` within N lines above (or below, or either), or in the comment run beside it | `// bound:` over a collection that grows |
| `registry-both-ways` | `registry_file`, `entry_pattern`, `use_pattern` | every use is registered AND every registry line is used; the LAST non-empty capture of a use match is the name, so an alternation with one group per branch works | the dev-instrument (env switch) registry |
| `doc-path-resolves` | `pattern` | a captured path must resolve relative to the CITING file's own directory, then the repo root, then inside its own `crates/<x>`/`tools/<x>` unit | doc citations |
| `dep-graph-forbids` | `roots`, `forbidden`, `edges`, `min_reachable` | no root package may REACH a forbidden one (glob) through the resolved dependency graph; `edges = "normal"` (default) never follows dev/build edges, which is the whole distinction | dev-only tooling in a shipping binary |
| `file-set-containment` | `superset_file`, `subset_file`, `capture` | every capture in `subset_file` must also appear in `superset_file` | a headless stand-in whose query must refuse at least what the real one refuses |
| `json-number-ceiling` | `files`, `path`, `tolerance_pct`, `enabled_env` | a number read out of generated JSON may not exceed its baseline by more than the tolerance | a criterion bench figure nobody was reading |
| `symbol-removed` | `pattern` (exactly one capture group) | a symbol captured at `--base <ref>` must still be captured somewhere in scope at the current tree, or be admitted by a tombstone comment naming it and a reason | a deleted test, invisible to every file-at-a-time law |

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
  per commit. `roots = "*"` is every workspace package, which is how a rule
  like "no package may reach the scratch crate" is stated without re-listing
  the workspace forever; under the wildcard a package that depends on nothing
  is a leaf rather than a broken walk, so `min_reachable` is what answers
  vacuity there — a walk that reached fewer packages than the floor fails
  loudly instead of reporting clean.
- **`line-count`**'s `count = "code"` judges only lines that carry CODE: blank
  lines and comment-only lines (by extension — `//` and `/* */` for
  `.rs`/`.go`/`.ts`, `#` for `.py`/`.sh`/`.toml`) never count toward `max`. A
  baseline recorded under the default `count = "text"` still compares fine —
  a code count is always LOWER, so switching never looks like a regression —
  but the ceiling is now stale, and `check` says so once per site rather than
  silently tightening it away on the next run. `unit_split` (a regex) judges
  the file as TWO units against the same `max` when it matches a line — the
  matching line opens the second unit — keyed `<path>` and `<path>#tests`, so
  a test module inline with its source does not inflate the module's own debt
  and vice versa; a file the regex never matches is one unit, unchanged.
- **`doc-path-resolves`**'s resolution order is citing-file-relative first —
  a markdown link is written relative to the file that holds it — then the
  repo root, then the `crates/<x>`/`tools/<x>` locality convention — the first
  one that names a real file or directory wins.
- **`path-regex-absent`** judges the NAME, never the contents: a probe file
  called `task19_buckling.rs` is the offence, and reading it would never show
  that. Hits carry no line, so `expected.txt` in its fixtures lists bare paths.
- **`registry-both-ways`** reads uses out of whatever the scope includes, source
  or not: put `tools/**/*.sh` in `include` and a switch read only by a shell
  script counts as a use, so it is neither reported unregistered nor reported
  stale.
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
  reads from `CARGO_TARGET_DIR` when the environment sets one and
  `<root>/target` when it does not, while the KEY keeps its `target/` prefix
  either way — otherwise an environment variable would rewrite every baseline
  entry. Its `clean/` fixture is a file the glob must
  REFUSE (criterion's `base/` copy is the natural one), which is what proves
  the reader discriminates.

`key` is `file` (baseline `<file> | <count>`) or `file:line-content-hash`
(baseline one line per occurrence, written `<path> | <trimmed line>`).

For a line-keyed law the identity is the **trimmed offending line, and only
that**: the baseline is a MULTISET of offending text over the whole workspace,
and the path is written down for the reader rather than compared. So a `git mv`
or a crate rename is not a regression — the same lines are still there, in the
same number — while adding one more occurrence of a line already at its
ceiling IS one, wherever it lands, which a per-file count cannot see (it would
read the new file as a brand-new key and the old file as unchanged). Line
NUMBERS are not part of the identity either: inserting a line above an offence
changes nothing. Swapping one offending site for a DIFFERENT line still
regresses, because the new text is a new identity at a ceiling of zero.
Tightening rewrites each surviving row's path from a site the scan actually
found, so a row never dangles at a file that has moved, and drops the rows
whose text no longer appears that many times. A count-keyed (`file`) law
measures a property OF a file — its length — so there the path IS the
identity and a rename is a new key at a ceiling of zero.

The pre-edit hook judges ONE file, so it cannot see a workspace total: an
added line whose text is already at its ceiling somewhere else is caught by
the whole-tree run at commit, not by the write.

#### Diff-scoped kinds

`symbol-removed` answers a different question from every other kind above:
not "does the tree, right now, obey the rule" but "did something the tree
used to carry silently vanish". It needs the OTHER side of a diff, so it
takes `--base <ref>` (git `pre-commit`/`pre-merge-commit` pass `HEAD`
automatically; `ratchet check` run by hand needs `--base` given) and, for
every file its `[scope]` matches, collects every name `pattern`'s one
capture group caught at `base` and at the current tree — the CURRENT tree,
never `base` twice, so `--proposed` and a staged `--files` narrowing are
honored on the tip side exactly as every other kind honors them. A name
present at `base` and absent everywhere at tip is a hit, keyed `<base
path>:<name>` — the base path, not wherever (if anywhere) the name turns up
again, so a plain rename reports under the OLD name while a name that moved
to a different file, unchanged, reports nothing at all. The one way through
besides restoring it is a tombstone comment left where the symbol stood:
`// ratchet: <law name> <symbol>: <reason>` (or `#`), with a REASON after
the colon — a stub with nothing after it admits nothing. Run with no base at
all (the pre-edit hook's shape: one `--proposed` file and nothing else) the
law answers nothing rather than guessing, and says so once in a note rather
than reporting a false clean. It carries no baseline file of its own: every
hit is a regression from a ceiling of zero, paid down only by a tombstone or
a restore, never by a floor row creeping up.

#### The baseline law

A baseline is a **ceiling per key** and it only ever goes down:

- measured **above** it → a regression, reported and (for `deny`) rejected;
- measured **below** it → the run that saw the fix lowers or drops the entry
  and rewrites the file: atomically (tmp + rename), byte-stable when nothing
  moved, preserving header and mid-file comments in place and whatever line
  ending is already on disk. A line-keyed row is also re-pathed from the sites
  the scan found, so the same run that leaves the count alone still stops a
  row from naming a file that has moved;
- it **never raises** a count and **never adds** a key. The only way to admit
  a new hit is the law's own escape comment.

`ratchet check` tightens by default (`--no-tighten` to report only); a run
carrying `--proposed` never tightens, because the tree it measured does not
exist. The gate does not tighten either — a commit hook that rewrote a file
mid-commit would leave the lowered ceiling unstaged.

At commit and merge time the scan sees TRACKED files only: every path in the
index, judged as the index has it, which is exactly what the commit will
contain. An untracked or ignored file is part of no commit — another session's
scaffolding in a shared checkout, a scratch note, a generator's leftovers —
and a merge refused over one cannot be cleared by changing anything in the
merge. Staging the file is what makes it answer. Pre-edit denial is the other
side and judges the file being written whether git has seen it or not.

#### Baselines are never raised by hand

A baseline is a ceiling that only ever goes down, and it lives in a text file
any editor can widen — which happened: a `1048` entry was hand-edited to `1049`
to get a commit through. So the ban is mechanical. `precommit` and
`premergecommit` parse every STAGED baseline old-vs-new, in both the counted
and multiset forms, and reject a key whose count ROSE or which is NEW — counting a multiset row by
its TEXT, the identity the engine uses, so a legitimate re-path is not read as
a brand-new key:

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

A fixture tree is laid out the way the REPO is, because the fixture root
stands in for the repo root and the law's own `include` globs decide what it
reads — an `include` of `crates/**/*.rs` reaches the first of these and never
the second:

```text
hit/crates/a/src/bare.rs
hit/bare.rs
```

A fixture the scope could never reach fails the test rather than being
skipped — otherwise a typo in `include` disarms the law in the real tree while
its fixtures stay green, which is the exact failure fixtures exist to catch.

A fixture is never compiled — the ratchet reads it as text, exactly like the
tree it stands in for — which is what the gate's own `<path> has no owning
cargo package — not tested` line is reporting: correct and harmless, but it
means an invalid fixture never fails as a syntax error. A typo in a string
literal or an unbalanced brace still scans, the law still counts whatever its
matcher sees, and the mistake surfaces as a wrong hit count rather than a
compiler error. That is tolerable for a matcher that only reads lines; it
sharpens for a `code_only` law, whose comment-stripping depends on the file
actually parsing — a fixture with an unterminated string or an unbalanced
block comment can make the law look right against input no compiler would ever
accept. Where a law reads STRUCTURE rather than lines, write the fixture so it
would compile, and keep it small enough to check by eye.

#### Pre-edit denial

The PreToolUse hook reconstructs what a `Write`/`Edit`/`MultiEdit` would leave
on disk (old/new strings applied to the file, MultiEdit in order) and judges
that content, narrowed to the one file:

```
ratchet: nan-guard: crates/pose/src/advance.rs:212 let a = x.clamp(0.0, 1.0); (baseline 0, now 1) — escape: // nan-safe: <why> on the line or the line above
```

Every hit is ONE line and every line ends with the way through, so the fix is
legible from the denial and nobody has to open the law: the escape comment to
write and where it may sit, `— no escape: lower the code` for a law that has
none, and `— split the file; the ceiling is N` for a count-keyed law, which is
paid down rather than waived. The line is bounded at 160 characters and it is
the OFFENDING TEXT that gets truncated — a remedy that scrolled off the end is
a remedy nobody read.

A `deny` law with a new hit exits 2 and the write never happens; a `warn` law
prints the line once and allows. Everything here fails **open** — a malformed
payload, an unreadable file or a broken law file must never wedge a session
over a rule that is itself broken. A narrowed run reports uses nobody
registered but never claims a registry line is stale: that needs the whole
tree.

#### Adopting a baseline (`--adopt`)

`check`'s tighten only ever LOWERS or REMOVES a row — never creates a file,
never raises one — which is correct for the everyday path but leaves no way
for a law to land its FIRST baseline, or for a deliberately widened one to
land its next: a brand-new deny law with real hits gets no baseline file at
all, and widening an existing law's scope just reports every newly-reached
hit as a regression forever. `ratchet check --adopt <law>` is the deliberate
door: it writes that law's baseline from what the tree measures RIGHT NOW,
raising or creating rows as needed, allowed only when the law has no baseline
file yet, or its `.toml` differs from HEAD — otherwise it refuses with one
line naming the law and the reason, so `--adopt` can never quietly launder an
unrelated raise. The commit-time staged-baseline guard (below) knows the same
rule: a raised or brand-new row is accepted when the law that owns the
baseline is ITSELF staged with a `[matcher]` or `[scope]` change in the same
commit, logged `baseline-adopted:<law>:<rows>`, and refused exactly as before
otherwise.

#### The law library (`ratchet init` / `ratchet presets`)

A known-good law does not have to be typed from scratch in every repo: this
binary embeds a set of PRESETS — ordinary law bodies, grouped `common` /
`rust` / `go`, with a `{{name}}` slot wherever a value is repo-specific (a
banned pattern, an env-prefix list, a dependency-graph root).
`aphrollo ratchet presets` lists every one, group/name and the params its
template asks for; `aphrollo ratchet init --preset common,rust --param
pattern=TODO\( --param prefixes=BORLD` copies each preset in those groups
into `.ratchet/laws/*.toml`, substituting `--param name=value` into its `{{name}}`
slots and adding `extends = "preset:<group>/<name>"` plus a `[params]` table
recording what it used. A preset with an unfilled slot is `[skip]`ped, named,
with what `--param` it still needs — nothing half-rendered is ever written —
and a name already under `laws/` is `[skip]`ped too: `init` is idempotent,
safe to re-run over a partly-adopted set.

The written file is otherwise an ORDINARY law: `extends` and `[params]`
change nothing about how it is scanned. What they buy is drift detection —
`ratchet check` re-renders the named preset with the law's own `[params]` and
warns, once per law, when its `[matcher]` no longer matches: a fork nobody
flagged as one. `doc_reference_exists` is also how `aphrollo docs check`
finds its rule when a repo declares no law of its own — see [Doc-reference
guard](#doc-reference-guard-aphrollo-docs-check).

#### Commands

```sh
aphrollo ratchet check                       # judge the tree, tighten, exit 1 on a deny regression
aphrollo ratchet check --only nan-guard      # one law
aphrollo ratchet check --format json         # what the hooks read
aphrollo ratchet check --no-tighten          # report only
aphrollo ratchet check --proposed crates/a.rs=/tmp/new.rs   # judge content not on disk
aphrollo ratchet check --adopt nan-guard     # write nan-guard's baseline from the tree (new or widened law only)
aphrollo ratchet check --base HEAD~1         # judge a diff-scoped law (symbol-removed) against that ref
aphrollo ratchet test                        # prove every law against its fixtures
aphrollo ratchet presets                     # list every embedded preset and its params
aphrollo ratchet init --preset common,rust --param pattern=TODO\( --param prefixes=BORLD
```

A repeat `check` costs milliseconds: every file's hits are cached under the
state dir, keyed by path + size + mtime **and** a hash of the law set, so a
rule that changed drops the cache instead of inheriting verdicts reached under
the old one. A repo with no laws dir under `.ratchet` says `no laws` and exits 0.

<!-- ratchet-spec:end -->

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
Read-only about the log: it never touches the one it reads, and an unparseable
line is skipped rather than guessed at (the log is appended to by several
processes). Two more numbers follow the table: `open escapes: N (oldest Nd)`,
and one `demote-candidate: <check>` line per check whose refusals rose two
weeks running (below).

#### Auto-demotion — a check that refuses more every week

A check that denies more work each week is doing one of two things: catching a
real regression in how code gets written, or refusing work that was correct.
The second is invisible from inside — every individual denial looks
reasonable, and the trend lives only in `gate.log`. So the log is read for it:
a check whose `pretooluse-denied:` plus `smell-escape:` count rose in EACH of
the last two weeks is named a demote candidate, and where `gh` can reach the
remote it becomes one `false-positive` record in the escape loop, deduplicated
against the open issue titles so a check re-flagged every week does not collect
a new issue every week. It is a prompt to look, never a verdict — the answer
may be to fix the check, narrow it, or demote it from deny to warn. An
`override-` entry is a session turning the whole gate off rather than a check
refusing anything, so it never counts toward a candidate.

### The escape loop (`aphrollo gate escape`)

An ESCAPE is a red that arrived after a local green: CI failed on a commit the
gate passed, a merge gate refused what precommit allowed, a mutant survived, a
playtest found a defect some check could have seen. It is the gate's only
direct evidence about what it is MISSING, and it is normally lost — noticed in
a terminal, fixed, forgotten, and the same class escapes again a month later.

```sh
aphrollo gate escape record "clippy warning reached main" --from-ci "build (ubuntu-latest)"
aphrollo gate escape record "the bound law refuses a fixed-capacity field" --kind false-positive
aphrollo gate escape sync            # open issues for whatever was recorded offline
aphrollo gate escape list            # the open ones
aphrollo gate escape verify-closure 321   # the escape-closure CI job runs this; exit 1 on a FAIL
```

`record` writes a schema-stamped line to `<stateDir>/escapes.jsonl` FIRST and
opens the issue second, so a missing `gh` or a repo with no GitHub remote costs
an issue and never the evidence; `sync` is the catch-up. Both create the label
they need (`gh label create --force`, idempotent) before asking for it, because
a fresh repository has neither, and both report what `gh` itself said when a
call fails -- "exit status 1" names none of the three things that actually go
wrong here (no label, no auth, no network).

`sync` is also where a DEMOTION CANDIDATE becomes an issue: `gate stats` names
every check whose refusals rose in each of the last two weeks, but it is a
read-only report and never writes to anybody's tracker. `sync` is the verb that
reaches GitHub deliberately, so it opens the one false-positive issue per
candidate that does not have one already. The issue is labelled
`escape` or `false-positive` and carries a fixed body — **what got through**,
**which stage should have caught it**, and a `closes-by:` line — because an
escape is closed by a LAW or a STAGE named in the fix, never by a sentence in a
document.

`verify-closure <pr>` is what makes that mechanical, as a CI job: for every
labelled issue the PR closes -- named in its body OR in any of its commit
messages, since GitHub honours the keyword in both -- the diff must touch one
of four things:

| what counts as changing a check | what does NOT |
|---|---|
| any file under the consuming repo's laws dir (`laws/` under its `.ratchet`) | anything else under that tree |
| non-test Go under `internal/tdd/` or `internal/ratchet/` -- the code that performs a check | the markdown beside it, and `_test.go` files nobody named |
| the workspace-ROOT `Cargo.toml`, changed inside its `[workspace.metadata.aphrollo]` table | a crate's own manifest, or a dependency bump in the root one |
| a source or test file the issue's `closes-by:` line names | a `closes-by:` naming a document |

It prints `#N ok` or `#N FAIL` per issue and exits 1 on any FAIL. An issue
carrying neither label is somebody else's and is left alone.

The count only goes down, and it is printed where it cannot be ignored: `gate
stats` states it, and the first session start of each week gets ONE line —

```
aphrollo: last 7d — green 87%, queued-skipped 4%, denies 3, overrides 1, open escapes 2 (oldest 11d)
```

— stamped like the disk sweep's last run, so it fires once per seven days
rather than on every session start.

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

### Doc-reference guard (`aphrollo docs check`)

Agent behaviour on this box is driven by prose — layered `CLAUDE.md` files plus
per-repo docs. A citation that points at a path which no longer exists silently
misdrives every session that loads the doc, and nothing else catches it.

```sh
aphrollo docs check                 # scan the cwd repo (default)
aphrollo docs check path/to/repo    # scan another repo root
aphrollo docs check README.md docs  # narrow to pathspecs in the cwd repo
```

This command is a CLI surface only: the extraction and resolution rule is the
[ratchet engine](#ratchet-laws-aphrollo-ratchet)'s own `doc-path-resolves`
matcher, one implementation for both a repo's own law and this default. It
reads tracked `*.md` (`git ls-files`) and judges each against a repo's own
`doc_reference_exists` law, when its `.ratchet/laws/*.toml` declares one,
else the built-in `common/doc_reference_exists` preset — a markdown link/image target
`[..](path)`, or an inline-code token that looks like a repo path (a slash
plus a file extension, or a multi-segment trailing-slash directory), resolved
first relative to the citing file, then the repo root, then the
`crates/<x>`/`tools/<x>` locality convention. `http(s)`/`mailto` URLs, bare
`#anchors`, and absolute/home paths are never captured as citations at all —
the matcher's own character class excludes them, not a special case. A
citation inside a fenced code block is judged exactly like one outside it: the
matcher is a stateless per-line scan and does not track fences, which a repo
wanting fence-awareness answers with a narrower `pattern` in its own law.
Every miss prints as

```
file:line: unresolved reference: <path>
```

and the command exits non-zero, so it drops straight into CI or a pre-commit
hook. **The bar is zero**: no baseline file, no allowlist, no suppression comment
— a rule with an escape hatch decays. If a doc must mention a bare concept
(`node_modules/`) or a path in *another* repo, keep it out of path-citation form
(drop the slash, or describe it in prose) rather than reaching for a suppression.

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
   `sessionend` invoke the binary, and points `statusLine` at `gate
   statusline`. Idempotent (a no-op re-run rewrites nothing), backs up any
   existing file, preserves foreign hooks and other keys, and migrates out old
   Node `tdd-*.js` entries. The Node plugin's leftover hook SCRIPTS go too —
   `<config-dir>/hooks/tdd-*.js`, `<config-dir>/hooks/tdd-*.sh`,
   `<config-dir>/hooks/tamper.js` and `<config-dir>/hooks/caveman-statusline.sh`,
   each removal reported once — because a settings entry still pointing at one
   double-fired every event.
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

### Upgrading in place — `aphrollo gate self-install`

`aphrollo gate self-install` rebuilds `./cmd/aphrollo` from the checkout it is
run in, renames the currently-running binary aside as `aphrollo.stale-<unix>`,
moves the freshly built one into its place, reclaims stale copies nothing
still holds open, then runs `init` so hooks and skills pick up whatever the
rebuild changed. `--bin` targets a binary other than the default install
path, `--no-init` skips the trailing `init`, and flags after a bare `--` are
forwarded to it.

### The managed CLAUDE.md block

A session that does not know the gate exists fights it: it re-runs suites the
hooks already ran, reads a `TIMEOUT` as a pass, hand-edits a baseline to get a
commit through. All of that is documented — here, in a file the session is not
reading. So `gate init` writes the operating instructions into the one file a
Claude session always reads, between markers this tool owns:

```
<!-- aphrollo:begin -->
## Working with the aphrollo gate
...
<!-- aphrollo:end -->
```

~25 lines: the PATH line for the queue shims, "the hooks run the tests — read
the one `gate:` line", what each outcome means (including which ones mean the
code was NOT tested), the commit-gate stage order, where the laws and baselines
live and how a new hit is admitted, the housekeeping commands, and — only for a
workspace with `undercover = true` — the commit-message rule.

- A repo that keeps a `CLAUDE.md` gets the block on every `gate init`; one that
  does not is left alone unless you pass `--claude-md`, which creates the file.
- WHICH repo is named, not inferred: `--repo <path>` (default: the working
  directory's repo), and the run prints the file it wrote.
- An existing block is replaced **in place**, never duplicated or moved: it may
  have been put somewhere deliberate. A file hand-edited mid-block (one marker
  left) has the orphan dropped and a whole block appended.
- Running twice is byte-identical, CRLF included — the file is a source file in
  the consuming repo, and a block that churned would show up as a diff at every
  session start.
- The text has ONE source (`internal/tdd/claudemd.go`), so a fix reaches every
  repo the next time init runs there. Edit that, never the block.

### The `tdd` skill

`gate init` also writes `<config-dir>/skills/tdd/SKILL.md`. The gate proves the
RED→GREEN *outcome* mechanically; it cannot tell whether the test was worth
writing, and the rules that answer that used to live in a marketplace plugin
that may not be installed. They now ship with the binary that enforces the
outcome: the RED→GREEN loop and what each `gate:` classification means, the
test-quality rules (name the break, derive expectations independently, no change
detectors, DAMP over DRY, real code over mocks, never weaken a test or a
baseline), the mutation proof for code that already exists, and evidence before
any completion claim. It is language-neutral — Rust and Go both run this gate.
The skill also carries the `/tdd status|off|on|allow-main|reset` frontmatter and
so replaces the old `/tdd` command stub, which init retires when it finds one
that mentions aphrollo (a hand-written stub is left alone). Managed like the
CLAUDE.md block: a second init is byte-identical, a hand edit is overwritten, the
source is `internal/tdd/tddskill.md`, and `--uninstall` removes it. The
session-start nudge points at this skill, so the thing it names always exists.

### The `sdd` skill

`gate init` also writes `<config-dir>/skills/sdd/SKILL.md`, the feature-level
counterpart to `tdd`: `tdd` says how one change is made, `sdd` says how a
feature gets from a question to merged code. `/sdd <slug>` runs four phases —
brainstorm one question at a time to a `spec.md` (problem, decisions each
carrying the rejected alternative on the same line, boundaries, acceptance
criteria as testable statements); plan it into lanes in `plan.md`, each lane
naming its files, its tests NAMED FOR THE BREAK they catch, and their
closed-form expectations, small enough for one builder; execute one lane per
builder with the plan text verbatim, under the `tdd` skill, reviewed cold and
merged on a green gate; close by moving the durable facts to their homes
(design docs, the crate's `decisions.md`, the followups index) and deleting the
spec tree in the merge.

The spec dir comes from `[workspace.metadata.aphrollo] sdd-dir`, default
`docs/sdd`. Managed exactly like the `tdd` skill: byte-identical on a re-run, a
hand edit is overwritten, source `internal/tdd/sddskill.md`, removed by
`--uninstall` only when the file still carries the marker.

### The managed agents

`gate init` writes `<config-dir>/agents/{builder,reviewer,researcher}.md`. The
gate's own conduct assumes all three exist: a **builder** that edits under the
gate (RED first, mutation proof for existing code, the gate's line is the
evidence, one mixed commit per task), a **reviewer** with no implementation
context that reports findings and a `mergeable` / `needs fixes (N)` verdict,
and a **researcher** that only locates and traces and refuses to fix. They
ship with the binary that enforces their rules so the two cannot drift.

Same management contract as the skills: idempotent, refreshed when the
binary's copy moves on, and `--uninstall` removes only files still carrying the
`Written by aphrollo gate init` marker — an agent of the same name that you
wrote is yours and survives.

### `aphrollo gate doctor`

One line per install check, `ok` or `FAIL <fix>`, exit 1 if anything failed. It
changes nothing; each check names its own fix, because "something is wrong with
the install" is not actionable.

```sh
aphrollo gate doctor
# ok    hook binary
# ok    hook timeouts
# FAIL  shim dir on PATH — PATH starts with C:\Program Files\Git\cmd, not
#       C:\Users\olive\bin\cargo-queue — put the queue dir first
# ok    batch shims removed
# ok    lock dirs writable — C:\ProgramData\aphrollo\locks
# ok    retired /tdd command
# FAIL  managed skills and agents — agents/reviewer.md (edited) — run `aphrollo gate init`
```

The checks: every managed hook runs the SAME binary and it is this build (size
and mtime, drift naming both paths) · each hook's `timeout` is at least the one
init writes, since the harness kills the hook process from outside before its
own deadline and cleanup can fire · the queue dir is first on the USER's PATH
(read from `HKCU\Environment` on Windows, not from this process's environment,
which is whatever a profile prepended) and holds the shims · no `.cmd` shim is
left · the machine-wide lock dir is writable · `<config-dir>/commands/tdd.md` is gone · every
managed skill and agent is byte-identical to the template the binary carries.

In a cargo workspace it also checks the CI clippy list is DERIVED from
`[workspace.metadata.aphrollo] clippy-clean` through
`<repo>/tools/clippy_clean_list.sh`: a workflow naming crates by hand gates nothing
the day a crate is added to the manifest. A workspace with no workflow at all
is a warning, not a failure.

### The statusline badge

`gate init` points `settings.json`'s `statusLine` at `aphrollo gate statusline`,
which reads the session payload on stdin and prints ONE badge:

```
[aphrollo]            green  — the gate is armed, nothing to report
[aphrollo]            red    — a run for THIS project failed and still stands
[aphrollo:off]        gray   — this session ran `/gate off`; edits are not gated
[aphrollo:deferred]   yellow — a detached build for this project is running
[aphrollo:mutants]    yellow — a cargo-mutants run holds this project's target
[aphrollo:queued]     yellow — the last run only queued; the suite never started
```

The BADGE carries the state, in its own colour. A tag goes inside the brackets
where the colour is not enough: yellow has three causes, so it names which, and
OFF says so in text because a badge whose colours are stripped — by a log, a
screenshot, a statusline that drops SGR — must never read an ungated session as
armed. Red and green are colour-only: both mean the gate is running, and a word
the colour already carries is a word a session stops reading.

A red is retired by either of two things, so the badge is never stale. ANY
green outcome logged for this project clears it, from any stage — post-edit,
pre-commit, pre-merge-commit or the post-Bash harvest — so a fix that lands
through a commit clears the badge at the next render rather than waiting for
the next edit. And a red older than 30 minutes with nothing logged after it is
dropped outright, with nothing rendered in its place: the badge is a real-time
signal or it is noise, and one false red teaches a reader to ignore the true
one. The hooks write the state; the statusline only reads it.

The tag is reserved for what changes what to do next, in that order; a
statusline that reports every healthy state is one nobody reads. It never
fails — a malformed payload, a missing session or an unreadable log all render
the plain badge, because a statusline runs on every prompt render and has
nowhere to report an error. A `statusLine` command naming
`caveman-statusline.sh` or `tdd-statusline.sh` is replaced; any other command
is yours and is left alone.

`userpromptsubmit` also appends a small reply-style block (`internal/tdd/style.md`,
~80 tokens) to additionalContext on every prompt, replacing a third-party
plugin's per-prompt style injection, and `sessionstart` includes it once so it
survives compaction; `/tdd style terse|plain` toggles it (default terse; env
`APHROLLO_REPLY_STYLE=plain` sets the machine default).

### The in-repo law spec (a README inside `.ratchet`)

`gate init` also drops the "Ratchet laws" section above into the repo it
initialises, as a README inside `.ratchet`, whenever that dir exists
(`--ratchet-readme` writes it regardless). Laws are edited by whoever owns the
repo, and until now the only spec for the schema lived in THIS file — on the
machine that installed the binary, at a path nothing in the consuming repo can
cite. Now a law, a CLAUDE.md or a doc can point at that in-repo README and the
citation resolves for everyone. The file is managed: a second init is
byte-identical, a hand edit is overwritten, and the source is this README's
`ratchet-spec` section (a test in `internal/tdd` fails if the two drift).

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
internal/docs/       doc-reference guard: extract path citations from tracked *.md, resolve, report misses
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

## The queue bypass, and what it is worth

A mutation run goes around the build queue: it owns its own target dir, so it
contends with nothing, and making it wait behind an editor's build is the
delay this whole design exists to remove. The check is a SHAPE — the target
dir must sit under `<parent>/.worktrees/<repo>/mutants` — rather than a path
handed over in an environment variable.

It is not a security boundary, and it is not meant to be. Any process on the
box can set `APHROLLO_QUEUE=bypass` with a target dir of that shape and skip
the queue. The harm is bounded to that one target dir: a bypassing run holds
no lock, so it cannot make anything else wait, and it can only disturb builds
that share the directory it was pointed at. What makes the tolerance
manageable is that every bypass is COUNTED — one `queue-bypass` line per
process, which `aphrollo gate stats` reports beside every other waiver.
