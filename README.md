# aphrollo-tools

One Go binary, **`aphrollo`**, that moves deterministic developer work out of
the agent token stream: LSP-backed rename and navigation, git worktree
lifecycle, the dev-tier control plane, and the TDD + law gates.

Every verb is **lossless**, **deterministic** and **idempotent** (done work
reports `[skip]`). `workspace` verbs execute by default (`--dry` previews);
`refactor`, `sqlc regen`, `gate gc` and `gate probe discard` preview by default
(`--apply` writes); `dev` acts immediately.

**The reference is the tool itself:** `aphrollo <verb> --help`. This file only
maps the verbs.

## Install

```sh
go build -o aphrollo ./cmd/aphrollo   # then put it on PATH
aphrollo install                      # session hooks, git gate, skills, agents, queue shims
aphrollo version                      # stamped commit and build time
aphrollo update                       # rebuild from origin/main and swap it in
```

`refactor`, `find`, `outline` and `show` need the language server on PATH:
`gopls`, `rust-analyzer`, `pyright-langserver` or `typescript-language-server`.

## Verbs

| verb | does |
|---|---|
| `aphrollo refactor` | rename a symbol across a project (LSP) |
| `aphrollo find` | references to a symbol |
| `aphrollo outline` | a file's symbols |
| `aphrollo show` | one symbol's source |
| `aphrollo workspace` | lanes (worktrees) and their git verbs: `create`, `commit`, `push`, `pr`, `submit`, `ship`, `merge [--wait <pr>...]`, `prune`, `diff`, `status` |
| `aphrollo dev` | dev-tier units: `up`, `down`, `restart`, `status`, `logs` |
| `aphrollo guardrail` | PreToolUse policy for long foreground waits and noisy commands |
| `aphrollo gate` | the TDD + law gates: hook entry points, `status`, `stats`, `output`, `allow`/`revoke`, `mutants`, `escape`, `probe discard`, `gc`, `classify-diff` |
| `aphrollo ratchet` | the law engine: `check`, `test`, `init`, `presets`, `--adopt` |
| `aphrollo docs` | `docs check`: every repo path a tracked `*.md` cites must resolve |
| `aphrollo sqlc` | `check` for sqlc drift, `regen --scoped` |
| `aphrollo check` | judge the tree read-only: ratchet, docs, sqlc, doctor |
| `aphrollo ci` | `ci why [<pr>\|<run-id>\|--main]`: why a pipeline run is red |
| `aphrollo issue` | open an issue against this repo |
| `aphrollo feedback` | file gate feedback with the upstream tracker |
| `aphrollo status` | one-line gate state for this checkout |

## The gate in one screen

- **Edit:** after every Edit/Write the hook runs the related tests and prints
  one `gate:` line (`green`, `red-missing-impl`, `red`, `TIMEOUT`, …).
- **Commit:** staged-baseline guard → ratchet laws → docs → vet/lint →
  fail-first (the staged test must be RED without the change). Suites are
  `NOT RUN` here and run at the merge.
- **PR:** `workspace pr`/`submit`/`ship` measure the lane's mutants first.
- **Merge:** `workspace merge` runs the suites and the mutation measurement on
  the merged tree, refuses an unaccepted survivor or timeout, then prints a
  retro when the PR's journey had friction.
- **Walls:** the primary checkout is merge-only; discarding commands are
  refused (`gate allow <wall>` arms one command).

Mutation runner contract: [docs/mutation-runner.md](docs/mutation-runner.md).
Law schema, matcher kinds and baselines: [.ratchet/README.md](.ratchet/README.md)
(generated from `internal/tdd/ratchet_laws.md`).

## Configuration

`[aphrollo]` in `aphrollo.toml`, or `[workspace.metadata.aphrollo]` in a cargo
workspace's `Cargo.toml`. `aphrollo config` prints the opt-in keys (the first
rows) with this repo's values; a repo's first `aphrollo install` prints them once.

With `undercover = true` a tool identity is refused at commit, pre-push and `workspace merge` and flagged at session start and by `gate doctor`; the Bash/PowerShell hook, the git shim, pre-push and `workspace create`/`claim`/`pr`/`ship`/`submit` refuse a tell ref name; `pr`/`ship`/`submit`, `issue`, `feedback` and the Bash/PowerShell hook (for a `gh pr`, `gh issue` or `gh api` call) check text before `gh`; CI's `undercover-text` job (`aphrollo ci undercover-text`) strips a tool footer that landed on a PR or comment and fails on a tell in the PR's commits.

| key | effect |
|---|---|
| `mutants-at-merge` | off by default: mutation measurement of the merged tree before every merge; cost: high CPU and wall-clock: a lane runs tens of mutants, each re-running its package's suite |
| `mutants-before-pr` | off by default: the same measurement before `workspace pr`/`ship`/`submit` open a PR; cost: the mutants-at-merge cost, paid before the PR opens |
| `mutants-shards` | derived by default: the most shards one measurement splits into; only ever lowers the box's own count; cost: fewer shards: less CPU at once, longer wall-clock |
| `mutants-slots` (box) | 1 by default: measurements this box runs at once, the rest queue (fixed at 1 for now); cost: each slot runs a full shard set, so size it to cores and RAM |
| `undercover` | off by default: the commit-msg gate refuses AI attribution trailers; cost: none |
| `commit-message-deny` | commit-msg deny patterns |
| `undercover-extra` | extra tokens for the undercover checks, e.g. `["codename"]` |
| `always-run`, `clippy-clean` | suites run on every merge; crates gated on clippy `-D warnings` |
| `fail-first-env` | env switches for the fail-first run |
| `go-test-reads` | `"<path prefix> -> <package dir>"`: a staged path also selects that suite |
| `mutants-env`, `mutation-accept` | the mutation run's env and its accept-list |
| `retro-on`, `retro-slow-merge-minutes`, `retro-sinks` | post-merge retro triggers and questions |
| `issue-labels`, `upstream` | labels `aphrollo issue` accepts; tracker for `aphrollo feedback` |
| `docs-check`, `baselines`, `prune-lanes-on-merge`, `sdd-dir` | docs on commit, guarded baselines, lane sweep, spec root |
| `[aphrollo.precommit]` (`aphrollo.toml` only) | `"<root>" = [["tsc", "--noEmit"], ["eslint", "src"]]`: argv arrays (no shell) run in order in that non-cargo root, replacing its built-in checks (go vet/lint, tsc/eslint); first failure refuses |

## Known limitations

- LSP columns are UTF-16 code units; `--symbol` resolves regardless.
- Unified diffs assume newline-terminated files.
