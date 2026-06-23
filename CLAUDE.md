# aphrollo-tools

First-party dev-env tooling for the agent platform: one zero-dependency Go
binary, **`aphrollo`** (`/usr/local/bin/aphrollo`). Moves deterministic
developer work *out of the agent token stream into code* — the agent spends
tokens on judgment, not mechanical read→grep→multi-edit→verify loops. `README.md`
is the full user-facing command reference; this file is the **developer**
context (conventions, contract, deploy).

Module `github.com/aphrollo/aphrollo-tools`, go 1.26.4. Single binary —
`go build -o aphrollo ./cmd/aphrollo`.

## Design contract (every tool obeys it)

- **Lossless** — never silently transform, truncate, or filter output.
- **Deterministic** — same inputs, same bytes out (sorted, stable).
- **Visible + idempotent** — every mutating verb is **safe to re-run**: already-
  done work reports `[skip]` / reuses (a re-driven turn never double-pushes or
  double-opens a PR), so re-running on a half-built state finishes the job
  without clobbering it. Each verb prints a **stateful, parseable receipt** of
  its full post-state (sha, ahead-count, PR #/url, CI) so the calling LLM never
  needs a follow-up git/gh call to confirm what landed. Fail loud with a fix
  suggestion rather than guessing.

**Execute-by-default with `--dry` by exception.** The `workspace` mutating verbs
(create, commit, push, submit, ship, pr, merge, update, claim, unclaim, remove,
prune) **EXECUTE BY DEFAULT**; pass **`--dry`** to print the plan and stop. This
is deliberate — the autonomous-coder flow wants apply-on-default, and
idempotency is the safety net that makes it safe. (`refactor`/`tdd` mutations
keep the older dry-run-by-default + `--apply` model; only `workspace` inverted.)
`aphrollo dev` is a service control plane, so it also executes immediately like
`systemctl` (it never had a dry-run). Read-only verbs (status/list/outline/show/
find-references) are unchanged.

## Command surface (see README for usage)

- `refactor rename-symbol` / `find-references`, `outline <file>`, `show <file> <symbol>`
  — LSP-backed (one client, one registry entry per language; columns are UTF-16).
- `workspace` — the 4 core coder verbs (`create`·`commit`·`push`·`submit`) +
  worktree lifecycle (`claim`/`unclaim`/`list`/`remove`/`prune`) + the
  operator/outside verbs (`update`/`diff`/`merge`/`status`/`verify`) + the
  still-present `pr`/`ship`. `create` is the renamed `prepare` (kept as a hidden
  alias); `submit` is the renamed, CI-guarded `ready` (also a hidden alias).
  `prune` is the merged-worktree sweep — it removes a worktree only when its PR
  is MERGED, the tree is CLEAN, and it is not the cwd, skipping the rest with a
  reason and folding in the stale admin-record prune; `cleanup` is a hidden alias
  to that sweep. `push` folds the draft-PR open; `submit`
  push→CI-gate→flip-to-review. `update` rebases the cwd worktree onto
  origin/<default> and force-pushes (with lease) on a clean rebase, leaving a
  conflict in progress; `diff` prints the branch's PR diff vs origin/<default>
  (read-only). The coder verbs (commit/push/submit) and `update` are **cwd-only**
  (operate on the worktree you stand in); merge/status/diff also take an explicit
  `<repo> <branch>`. Plus `verify`
  (run the affected app's `{test, typecheck, lint}` trio — the typecheck/lint the
  commit gate does not cover). Every `<repo>` arg accepts a **bare name**
  (`aphrollo-web`) resolved from anywhere under the spaces tree (`resolveMainRepo`
  in `internal/workspace/target.go`): the clone you stand in, one of its
  worktrees, or a unique `~/spaces/*/<name>` sibling — never double-joined against
  cwd. A real abs/rel path still wins as-is; an ambiguous name (matches >1 clone)
  errors and lists the candidates. Override the spaces root with
  `APHROLLO_SPACES_ROOT`.
- `dev` — `up`/`down`/`restart`/`status`/`logs` (replaces the retired
  `aphrollo-dev` bash wrapper).
- `guardrail pretooluse` — Claude PreToolUse policy hook (block long fg waits, warn noisy cmds).
- `tdd` — the TDD gates (`pretooluse`/`posttooluse`/`userpromptsubmit`/`sessionend`/
  `precommit`/`prepush`) + `tdd init` (wires session hooks + global git gate).
  Ported from the retired `claude-code-tdd` Node hooks (this binary IS the gate now).
  The gate is **solely mechanical**: edit-time smell blocks + commit-time
  anti-cheat/fail-first/suite. `prepush` is a **mechanical no-op** (never blocks),
  kept only for back-compat with a lingering pre-push shim; `tdd init` prunes that
  stranded shim. Adversarial review is owned by the **separate reviewer agent**,
  not this binary.

## Layout

```
cmd/aphrollo/        entry point (one-liner over internal/cli)
internal/cli/        arg parsing + subcommand dispatch (testable Run)
internal/refactor/   detect lang → spawn server → rename/refs/outline/show
internal/lsp/        LSP types + JSON-RPC stdio client
internal/diff/       deterministic unified-diff renderer
internal/guardrail/  PreToolUse policy
internal/tdd/        TDD gates (mechanical-only): policy engine, edit smells, anti-cheat, fail-first, install
internal/workspace/  worktree lifecycle + git verbs
internal/dev/        dev-tier control plane (systemd)
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
  flow. Detectors run against a **masked** copy (smell detectors mask strings +
  comments; suppression detectors mask strings, keep comments) — a token only in
  a string never blocks. Keep edit-time blocks near-zero-FP; the heavy mechanical
  check (fail-first + suite) lives at commit where a false block only costs a
  re-run. (The only git gate is pre-commit; there is no pre-push gate.)
- **Attribution: honest here.** This is first-party tooling, not a client-facing
  undercover repo — the `🤖 Generated with Claude Code` footer + `Co-Authored-By`
  are fine (matches aphrollo-agents; per the box `~/CLAUDE.md` per-repo rule).
  (Coder/devops agent sessions suppress the byline via `includeCoAuthoredBy:false`
  regardless — that's their global default, not this repo's call.)

## Deploy

Go service — ships `/usr/local/bin/aphrollo` **on merge** via its OWN pipeline
(`deploy/deploy-prod.sh` on the github-runner), NOT deploy-infra (infra #186
retired the root build task). aphrollo-infra no longer force-installs it.

## Don't

- Don't break the verb contracts: `workspace` mutations **execute by default**
  (`--dry` to preview), while `refactor`/`tdd` mutations stay **dry-run by
  default** (`--apply` to execute). `dev` always acts now. Each kept its model on
  purpose — don't homogenize them.
- Don't re-port the offenders this gate excludes: **mutation testing**
  (false-positive/non-determinism prone), the SessionStart full-suite baseline,
  or `/tdd allow-main` — the fail-first + mechanical suite cover the ground
  without the flakiness.
- **tdd is solely mechanical.** Edit-time smell blocks + commit-time
  anti-cheat/fail-first/suite; `prepush` is a no-op. Don't add an LLM or any
  non-deterministic call to this binary's gate — adversarial review belongs to
  the reviewer agent, not here.
- Don't add a sudo wrapper or wildcard grant — the narrow exact-match systemctl
  fence is the whole security story.
- Don't duplicate README usage here — this file is dev context only.
