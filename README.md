# aphrollo-tools

One Go binary, **`aphrollo`**, that moves deterministic developer work out of
the agent token stream: LSP-backed rename and navigation, git worktree
lifecycle, the dev-tier control plane, and the TDD + law gates.

Every verb is **lossless**, **deterministic** and **idempotent** (done work
reports `[skip]`). Every mutating verb (`workspace`, `refactor`, `sqlc regen`,
`gate gc`, `gate probe discard`, `gate split-commit`, `gate install`, `update`,
`ratchet init`, `ratchet check`, `gate mutants hold`, `issue`, `gate feedback`,
`gate escape record`) executes by default and `--dry` previews; `--apply` is a
legacy no-op that prints a notice; `dev` acts immediately.

**The reference is the tool itself:** `aphrollo <verb> --help`. This file only
maps the verbs.

## Install

```sh
go build -o aphrollo ./cmd/aphrollo   # then put it on PATH
aphrollo install                      # session hooks, git gate, skills, agents, queue shims
aphrollo install --managed-block-only --repo <lane>   # re-render only the CLAUDE.md block (no hooks, no shims)
aphrollo version                      # semantic version, then the stamped commit and build time
aphrollo update                       # rebuild from origin/main and swap it in
```

The version lives in `internal/buildinfo/VERSION`, and `CHANGELOG.md` says per
version what a consumer will notice. A repo declares the oldest binary it
accepts with `requires = ">=1.4"` under `[aphrollo]` in `aphrollo.toml` (under
`[workspace.metadata.aphrollo]` in a Cargo workspace's manifest). An older
binary does not judge that repo: a hook prints one line naming the version
needed and `aphrollo update`, and lets the edit or commit through; a verb that
writes repo state or judges the tree by its laws (`ratchet`, `check`, `docs`,
`sqlc`, `install`, `workspace` bar `list`, and the like) refuses with exit 1 and
the same line. `aphrollo version check --base <ref> --body-file <file>` holds a
change to the version rule in this repo's CI.

`refactor`, `find`, `outline` and `show` need the language server on PATH:
`gopls`, `rust-analyzer`, `pyright-langserver` or `typescript-language-server`.

## Verbs

| verb | does |
|---|---|
| `aphrollo refactor` | rename a symbol across a project (LSP) |
| `aphrollo find` | references to a symbol |
| `aphrollo outline` | a file's symbols |
| `aphrollo show` | one symbol's source |
| `aphrollo workspace` | lanes (worktrees) and their git verbs: `create`, `commit`, `push`, `pr`, `submit`, `ship`, `merge [--wait <pr>... | --wait --resume]`, `prune`, `diff`, `status` |
| `aphrollo dev` | dev-tier units: `up`, `down`, `restart`, `status`, `logs` |
| `aphrollo guardrail` | PreToolUse policy for long foreground waits and noisy commands |
| `aphrollo gate` | the TDD + law gates: hook entry points, `status`, `stats`, `output`, `allow`/`revoke`, `mutants`, `escape`, `probe discard`, `split-commit`, `gc`, `classify-diff` |
| `aphrollo ratchet` | the law engine: `check`, `test`, `init`, `presets`; `check --adopt <law>` writes a new or widened law's first baseline |
| `aphrollo docs` | `docs check`: every repo path a tracked `*.md` cites must resolve |
| `aphrollo sqlc` | `check` for sqlc drift, `regen --scoped` |
| `aphrollo check` | judge the tree read-only: ratchet, docs, sqlc, doctor |
| `aphrollo ci` | `ci run`: the one CI entry point; runs the repo's own pull_request workflow(s) on this HEAD merged into trunk in a throwaway worktree (run: steps under bash, uses: steps listed and skipped, first matrix combination only, no mutation); a green is stored per tree and reused; `ci why [<pr>\|<run-id>\|--main]`: why a pipeline run is red |
| `aphrollo issue` | open an issue against this repo |
| `aphrollo feedback` | file gate feedback with the upstream tracker |
| `aphrollo status` | one-line gate state for this checkout |

## The gate in one screen

- **Edit:** after every Edit/Write the hook runs the related tests and prints
  one `gate:` line (`green`, `red-missing-impl`, `red`, `TIMEOUT`, …), which
  also names every ratchet refusal the commit would raise, with its escape.
- **Turn end:** `Stop` and `SubagentStop` block the end of a turn once when a
  deferred run finished red after Claude's last hook, with that run's gate line
  as the reason (never twice in a row; a red already shown may end the turn);
  `TaskCompleted` exits 2 with the failing tests while the task's tests are
  red. `/gate off` switches all three off.
- **Commit:** staged-baseline guard → ratchet laws → docs → vet/lint →
  fail-first (the staged test must be RED without the change). Suites are
  `NOT RUN` here and run at the merge.
- **PR:** `workspace pr`/`submit`/`ship` measure the lane's mutants first.
- **Merge:** `workspace merge` runs the suites and the mutation measurement on
  the merged tree, refuses an unaccepted survivor or timeout, and a
  not-covered or inconclusive mutant on a line the lane adds, then prints a
  retro when the PR's journey had friction. A merge made outside it (the GitHub
  web UI, `gh pr merge`, a terminal) is recorded once per commit as a `merge`
  event by=outside plus an `escape` event of verdict outside-merge, when local
  trunk takes it in: from `workspace sync`, or the post-merge hook in a repo
  with an `aphrollo.toml`, a `.ratchet` directory or a Cargo
  `[workspace.metadata.aphrollo]` table. `workspace sync --since <ref>`
  backfills a range once (`--dry` names the merges and writes nothing).
- **Long suites:** a `go test` package list the recorded per-package times say
  would not fit one run's 600s budget is cut into runs that each do, instead of
  timing out whole. The merge's `-race` list and the commit gate's plain list
  take the same path. The gate records each package's own seconds from every
  `go test` run it makes (the newest twenty, p90, weighed ×1.5; 90s for a
  package never recorded, 30s without `-race`), and a list that fits stays the
  one command it was. Every package is in exactly one run, with the flags it
  always had. Runs go side by side up to the build-slot count
  (`APHROLLO_MECH_PARALLEL` overrides), each starting through the
  memory-headroom wait and under the memory cap, all inside one overall cap
  (`APHROLLO_MECH_TOTAL_SECS`, 2700 by default). The verdict is green only if
  every run is; a run that does not finish makes the result a timeout that
  names its packages and the runs that never started, never a pass, and a run
  that fails ends the starting of new ones.
- **Walls:** the primary checkout is merge-only; discarding commands are
  refused (`gate allow <wall>` arms one command).
- **Memory:** every test, suite, lint and mutation process the gate starts runs
  under a hard memory cap, so a runaway kills only itself. On Linux with a user
  systemd manager the child runs in a transient scope (`MemoryMax`, no swap) and
  the kernel enforces it; without one, a watchdog sums the process group's
  resident memory and ends it at the cap; on Windows a job object's job memory
  limit refuses the allocation. The default cap is 75% of RAM, or the memory
  available now if smaller, split across the build slots (a mutation run gets
  the whole pool, and only its runaway worker is ended); `memory-cap` overrides
  it. A run the cap ends is reported `OOM-KILLED at <cap>` and is inconclusive,
  never red and never a timeout. A suite or measurement waits for headroom
  (an eighth of RAM, 2-8 GB, double while swap is 90% full) and is refused with
  the numbers when it never comes. Gate scratch (`GOTMPDIR`, cargo's temp) is on
  disk beside the worktrees, not in `/tmp`; the session-start sweep (every repo
  the gate worked in lately) and a pass after each merge and mutation run
  reclaim the scratch, mutation areas and stub dirs of runs that are over, and
  leave everything a live run holds.

Mutation runner contract: [docs/mutation-runner.md](docs/mutation-runner.md).
Law schema, matcher kinds and baselines: [.ratchet/README.md](.ratchet/README.md)
(generated from `internal/tdd/ratchet_laws.md`).

## Configuration

`[aphrollo]` in `aphrollo.toml`, or `[workspace.metadata.aphrollo]` in a cargo
workspace's `Cargo.toml`. `aphrollo config` prints the opt-in keys (the first
rows) with this repo's values; a repo's first `aphrollo install` prints them once.

With `undercover = true` a tool identity is refused at commit, pre-push and `workspace merge` and flagged at session start and by `gate doctor`; the Bash/PowerShell hook, the git shim, pre-push and `workspace create`/`claim`/`pr`/`ship`/`submit` refuse a tell ref name; `pr`/`ship`/`submit`, `issue`, `feedback` and the Bash/PowerShell hook (for a `gh pr`, `gh issue` or `gh api` call) check text before `gh`. The Bash/PowerShell hook judges `gh pr`/`gh issue` create, edit, comment, review and merge text, and a `gh api` request's title, body and head fields, `--input` JSON file and GraphQL mutation. Every check runs on this box before the text reaches GitHub; nothing in CI repeats it.

One ref name is let through: in a cloud session (`CLAUDE_CODE_REMOTE=true`) the branch the platform assigned, named by `APHROLLO_ASSIGNED_BRANCH=<branch>`, matched byte for byte, never by pattern. Every branch a session names itself stays refused. `workspace merge` always writes its own merge subject (the PR title and number), so GitHub's "Merge pull request #N from <branch>" line never reaches history.

| key | effect |
|---|---|
| `mutants-at-merge` | off by default: mutation measurement of the merged tree before every merge; cost: high CPU and wall-clock: a lane runs tens of mutants, each re-running its package's suite |
| `mutants-before-pr` | off by default: the same measurement before `workspace pr`/`ship`/`submit` open a PR; cost: the mutants-at-merge cost, paid before the PR opens |
| `mutants-at-commit` | off by default: mutation of the lines a commit adds, run against the tests selected for each mutant's function, before the commit lands; a survivor refuses it; cost: up to the budget of wall-clock per commit on bounded workers, under the memory cap; a box with no headroom or a busy mutation lock measures nothing and CI decides |
| `mutants-commit-budget` | 60 by default: the seconds the commit-time run may spend; mutants it does not reach are reported NOT MEASURED, never refused; cost: a higher figure holds a commit up longer on a slow box |
| `mutants-integration-packages` | none by default: package directories whose mutants stay settled against the tests of the packages that import them; every other package's mutant its own tests miss is refused at once; cost: each listed package's missed mutants run the importers' suites, nearest first, within a total time cap |
| `mutants-shards` | derived by default: the most shards one measurement splits into; only ever lowers the box's own count; cost: fewer shards: less CPU at once, longer wall-clock |
| `mutants-slots` (box) | 1 by default: measurements this box runs at once, the rest queue (fixed at 1 for now); cost: each slot runs a full shard set, so size it to cores and RAM |
| `memory-cap` | derived by default: the most memory, in GB, one test, suite or mutation run the gate starts may hold before it is killed and reported OOM-KILLED (inconclusive, never red); derived from RAM, free memory and the slot count; off disables it; cost: a cap below what a build honestly needs kills honest work |
| `memory-headroom` | derived by default: the available memory, in GB, a suite or measurement needs before it starts; below it the start waits, then is refused with the numbers; doubled while swap is 90% full; cost: a higher figure defers work on a busy box |
| `undercover` | off by default: the commit-msg gate refuses AI attribution trailers; cost: none |
| `ci` | `auto` (default), `local` or `github`: which CI judges `workspace merge`. `auto` uses GitHub's checks and falls back to local CI (`ci run`) when its jobs never start (a billing lock); `local` never waits on GitHub; `github` refuses an outage. `workspace merge --ci <mode>` beats it for one merge; every merge prints which CI judged it and why |
| `commit-message-deny` | commit-msg deny patterns |
| `undercover-extra` | extra tokens for the undercover checks, e.g. `["codename"]` |
| `always-run`, `clippy-clean` | suites run on every merge; crates gated on clippy `-D warnings` |
| `fail-first-env` | env switches for the fail-first run |
| `go-test-reads` | `"<path prefix> -> <package dir>"`: a staged path also selects that suite |
| `mutants-env`, `mutation-accept` | the mutation run's env and its accept-list |
| `retro-on`, `retro-slow-merge-minutes`, `retro-sinks` | post-merge retro triggers and questions |
| `issue-labels`, `upstream` | labels `aphrollo issue` accepts; tracker for `aphrollo feedback` |
| `docs-check`, `baselines`, `prune-lanes-on-merge`, `sdd-dir` | docs on commit, guarded baselines, lane sweep, spec root |
| `[aphrollo.precommit]` (`aphrollo.toml` only) | `"<root>" = [["tsc", "--noEmit"], ["eslint", "src"]]`: argv arrays (no shell) run in order in that non-cargo root, replacing its built-in checks (go vet/lint, the npm typecheck and lint); first failure refuses. A declared command has no HEAD baseline unless it is written `{ argv = [...], baseline = "lines" }`: then a failure is run again on HEAD's tree and refuses only over output lines HEAD's run did not print (paths and trailing whitespace aside), or when HEAD's run cannot be made |
| `[aphrollo.typecheck]` (`aphrollo.toml` only) | `"<root>" = ["svelte-check", "--tsconfig", "./tsconfig.json"]`: one argv whose first word is an npm bin, run as `node <its entry>` from that npm root's node_modules at commit, merge and `aphrollo check`. Undeclared, a root's typecheck is its package.json `typecheck` script, else its `check` script (each `&&` step run without a shell; a script that needs one is NOT RUN), else svelte-check (after `svelte-kit sync` in a SvelteKit app) when it depends on it, else tsc, those two only with a tsconfig.json. tsc, vue-tsc, svelte-check and eslint are judged by the diagnostics they add over HEAD, any other tool by the output lines it adds over HEAD's run |
| `[aphrollo.lint]` (`aphrollo.toml` only) | `"<root>" = ["eslint", "src"]`: the npm root's lint, run the same way, in place of eslint over the staged files |

## Known limitations

- LSP columns are UTF-16 code units; `--symbol` resolves regardless.
- Unified diffs assume newline-terminated files.
