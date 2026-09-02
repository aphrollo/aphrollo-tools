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

- `refactor rename-symbol` / `find-references`, `outline <file>`, `show <file> <symbol>`
  — LSP-backed (one client, one registry entry per language; columns are UTF-16).
- `workspace` — worktree lifecycle (`prepare`/`claim`/`unclaim`/`list`/`remove`/
  `prune`/`cleanup`) + git verbs (`commit`/`push`/`pr`/`ship`/`merge`).
- `dev` — `up`/`down`/`restart`/`status`/`logs` (replaces the retired
  `aphrollo-dev` bash wrapper).
- `guardrail pretooluse` — Claude PreToolUse policy hook (block long fg waits, warn noisy cmds).
- `ratchet` — the law engine: `check` judges a repo against its declared
  `.ratchet/laws/*.toml`, `test` proves each law against its fixtures.
- `gate` — the TDD + law gates (`pretooluse`/`posttooluse`/`userpromptsubmit`/`sessionend`/
  `precommit`/`prepush`) + `gate init` (wires session hooks + global git gate); `tdd` is a silent alias for one release.
  Ported from the retired `claude-code-tdd` Node hooks (this binary IS the gate now).
- `docs check` — doc-reference guard: every repo path a tracked `*.md` cites must
  resolve (relative to the citing file, then repo root); exit 1 on any miss. Bar
  is zero — no baseline, no allowlist, no suppression.

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
internal/docs/       doc-reference guard: extract path citations, resolve, report misses
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
  flow. A rule about the CONSUMING repo's source text is not a detector at all:
  it is a law in that repo's `.ratchet/laws/*.toml`, run by `internal/ratchet`. Detectors run against a **masked** copy (smell detectors mask strings +
  comments; suppression detectors mask strings, keep comments) — a token only in
  a string never blocks. Keep edit-time blocks near-zero-FP; heavy checks
  (fail-first) live at commit/push where a false block only costs a re-run.
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

- Don't break the mutation contracts: `refactor`/`gate` are **dry-run by default**
  (`--apply` to write); `workspace` verbs **execute by default** (`--dry` to
  preview). `dev` acts now with no dry-run at all. Don't re-invert `workspace`
  back to `--apply`-opt-in — that opt-in is legacy.
- Don't re-port what was deliberately dropped: **mutation testing** (the
  documented FP/non-determinism offender), the SessionStart full-suite baseline,
  or `/gate allow-main` — the fail-first gate covers the ground without
  the flakiness.
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
- **What the line means:** `green (N passed)` · `red-missing-impl` (a clean RED) ·
  `red` · `red-bogus` (broken test setup, not a real RED) · `TIMEOUT` / `SKIPPED` /
  `QUEUED-SKIPPED` (**inconclusive — the code was NOT tested**) · `BUILDING (deferred)`
  (the build outran the budget and continues; its result arrives at the next hook).
  The only sanctioned manual runs: a mutation proof, a deliberate soak, or ONE
  targeted `-p <crate> <filter>` after the hook itself said TIMEOUT/SKIPPED.
- **Commit gate, cheapest first:** staged-baseline guard → ratchet laws → `cargo fmt`
  → always-run guards → clippy → workspace check → fail-first RED proof → the
  touched crates' suites. It stops at the first rejection and names the stage.
- **Laws are data:** `.ratchet/laws/*.toml` (scope + one matcher + severity), with
  baselines under `.ratchet/baselines/` that only ever go DOWN. `aphrollo ratchet
  check` judges the tree and tightens; `aphrollo ratchet test` proves each law against
  its fixtures. A new hit is admitted by the law's escape comment, NEVER by editing a
  baseline — the gate rejects a raised one.
- **Housekeeping:** `aphrollo gate stats --since 7d` (pipeline health) ·
  `aphrollo gate gc` (dry run; `--apply` reclaims stale build dirs).

_This block is written by `aphrollo gate init`. Edit the template in aphrollo, not
the block — the next init overwrites whatever is between the markers._
<!-- aphrollo:end -->
