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
- **Visible** — two mutation models, by verb family:
  - `refactor`/`tdd` mutations are **dry-run by default**; pass `--apply` to write.
  - `workspace` verbs (`merge`/`prune`/`commit`/`push`/`ship`/… ) **execute by
    default**; pass `--dry` to preview the plan and stop. (The old `--apply` opt-in
    on these is **legacy/no-op** — you now opt OUT with `--dry`, not in with `--apply`.)
  Each step is **idempotent** — already-done work reports `[skip]`, never redone,
  so re-running on a half-built state finishes the job without clobbering it.
  Fail loud with a fix suggestion rather than guessing.

`aphrollo dev` is a service control plane, so it also **executes immediately**
like `systemctl` (no dry-run, no `--dry`). The split: `refactor`/`tdd` defer and
preview; `workspace` mutates source but acts now; `dev` controls running units
and acts now.

## Command surface (see README for usage)

- `refactor rename-symbol` / `find-references`, `outline <file>`, `show <file> <symbol>`
  — LSP-backed (one client, one registry entry per language; columns are UTF-16).
- `workspace` — worktree lifecycle (`prepare`/`claim`/`unclaim`/`list`/`remove`/
  `prune`/`cleanup`) + git verbs (`commit`/`push`/`pr`/`ship`/`merge`).
- `dev` — `up`/`down`/`restart`/`status`/`logs` (replaces the retired
  `aphrollo-dev` bash wrapper).
- `guardrail pretooluse` — Claude PreToolUse policy hook (block long fg waits, warn noisy cmds).
- `tdd` — the TDD gates (`pretooluse`/`posttooluse`/`userpromptsubmit`/`sessionend`/
  `precommit`/`prepush`) + `tdd init` (wires session hooks + global git gate).
  Ported from the retired `claude-code-tdd` Node hooks (this binary IS the gate now).

## Layout

```
cmd/aphrollo/        entry point (one-liner over internal/cli)
internal/cli/        arg parsing + subcommand dispatch (testable Run)
internal/refactor/   detect lang → spawn server → rename/refs/outline/show
internal/lsp/        LSP types + JSON-RPC stdio client
internal/diff/       deterministic unified-diff renderer
internal/guardrail/  PreToolUse policy
internal/tdd/        TDD gates: policy engine, edit smells, anti-cheat, fail-first, review, install
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
  a string never blocks. Keep edit-time blocks near-zero-FP; heavy checks
  (fail-first, review) live at commit/push where a false block only costs a re-run.
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

- Don't break the mutation contracts: `refactor`/`tdd` are **dry-run by default**
  (`--apply` to write); `workspace` verbs **execute by default** (`--dry` to
  preview). `dev` acts now with no dry-run at all. Don't re-invert `workspace`
  back to `--apply`-opt-in — that opt-in is legacy.
- Don't re-port what was deliberately dropped: **mutation testing** (the
  documented FP/non-determinism offender), the SessionStart full-suite baseline,
  or `/tdd allow-main` — the fail-first + review gates cover the ground without
  the flakiness.
- Don't add a sudo wrapper or wildcard grant — the narrow exact-match systemctl
  fence is the whole security story.
- Don't duplicate README usage here — this file is dev context only.
