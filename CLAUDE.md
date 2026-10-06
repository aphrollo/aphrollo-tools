# aphrollo-tools

One Go binary, **`aphrollo`**, that moves deterministic developer work out of
the agent's token stream into code: gates, laws, lanes, merges. Standard
library plus `golang.org/x/sys` (Windows process calls) and `pgregory.net/rapid`
(property tests). Module `github.com/aphrollo/aphrollo-tools`, go 1.26.6;
build with `go build -o aphrollo ./cmd/aphrollo`. Usage lives in
`aphrollo <verb> --help` and `README.md`; this file is developer context only.

## Design contract

- **Lossless, deterministic, visible:** never silently drop output; same input,
  same bytes; fail loud with a fix.
- **Mutating verbs execute by default; `--dry` previews.** `--apply` is a
  legacy no-op. Flags parse before or after positionals through
  `parseFlagsAnywhere`; an unknown flag is refused. Steps are idempotent
  (`[skip]` for done work). `dev` acts at once with no dry run.

## Command surface

- `refactor` (`rename-symbol`), `find`, `outline`, `show`: LSP-backed, columns in UTF-16.
- `workspace`: lanes (`create`/`claim`/`unclaim`/`list`/`remove`/`prune`) and git verbs (`commit`/`push`/`pr`/`ship`/`submit`/`merge`).
- `dev`: service control plane (`up`, `down`, `restart`, `status`, `logs`).
- `guardrail`: the PreToolUse policy; a rule lives in `internal/guardrail`, never in a hook of its own.
- `gate`: the hooks (`pretooluse`/`posttooluse`/`userpromptsubmit`/`sessionstart`/`sessionend`/`stop`/`subagentstop`/`precommit`/`premerge`/`prepush`) plus `allow`, `revoke`, `init`, `gc`, `stats`, `output`, `mutants`, `escape`; `tdd` is a silent alias.
- `ratchet`: laws in `.ratchet/laws/*.toml` (`check`, `test`, `init`, `presets`); baselines only go down, `--adopt` is the one way to raise one.
- `docs` (`check`), `sqlc` (`check`, `regen --scoped`), `ci` (`why`, `run`).
- `stats`, `why`, `release`, `changelog`, `version`, `check`, `config`, `issue`, `install`, `update`.

## Conventions

- **Strict TDD.** This repo's own gates apply. Per-language refactor work has a
  real e2e test against the LSP server, skipped when it is not installed.
- **Privilege:** no wildcard sudo, ever. The one privileged atom is `dev`'s
  exact-match `systemctl restart` on `{api,rlndx,infra}`, with argv built,
  never taken from a caller.
- **A new check is data:** a policy entry in a slice, never new control flow.
  A rule about a consuming repo's source is a law in that repo's
  `.ratchet/laws`. Detectors run on masked text, so a token in a string never
  blocks. Edit-time blocks stay near zero false positives; heavy checks live
  at commit or merge.
- **Platform code** goes in `_windows.go` / `_other.go` pairs, never an
  inline `runtime.GOOS` (law `platform_seam`).
- **Issues close on merge**, through a `Closes #n` trailer, one per line.
  Never record a status further along than the work: `TIMEOUT`, `SKIPPED` and
  `deferred` are not green. An open point is an issue (`aphrollo issue`),
  never a markdown follow-up.
- **Undercover:** commits, PRs and issues carry no attribution trailer or
  footer, and never the word "Claude"; the commit-msg gate refuses it.
- **Versions:** a PR body says `version: none|patch|minor|major` and adds one
  `changelog.d/<lane>.md` of that level; the release job on main tags.

## Working in this repo

- A new file under `internal/tdd/` needs a `<file> <package>` row in the
  `[files]` section of `tools/tddsplit/manifest.txt`, at its sorted place.
- Never hand-edit generated `export.go`, `deps_*.go` or `api_*.go`; run
  `go run ./tools/tddsplit -regen`, then
  `go test ./tools/tddsplit -run TestCommittedTree_GeneratedFilesMatchTheGenerator`.
  Test helper names must be unique across all `internal/tdd` sub-packages.
- A new package with tests needs a `main_test.go` whose TestMain calls
  `gitiso.Main` (or `tddtest.Main`).
- A new exec call site that spreads a list needs a row in
  `internal/argvbatch/callsites_test.go`.
- A new language is one `internal/lang/languages/<name>.toml` plus fixtures
  under `.ratchet/fixtures/languages/<name>/`; a change to an existing row's
  lexing raises its `view` and keeps the old row as `<name>-v<n>.toml`.
- A test that fails under load but passes alone is a broken test: fix it with
  an injected clock, a blocking fake or its own state, never a retry.
- Catch up with `git rebase origin/main`, never a merge. A green open PR takes
  no further pushes; a red one gets its fix on the same branch.

<!-- aphrollo:begin -->
## aphrollo gate
- **Hooks run the tests, not you.** Read each `gate:` and `gate: deferred` line after an edit; never re-run a suite they ran. Iterate with `go vet ./...`. Usage: `aphrollo <verb> --help`.
- **Verdicts:** `green (N passed)` · `red-missing-impl` (clean RED) · `red` · `red-bogus` · `TIMEOUT`/`SKIPPED`/`QUEUED-SKIPPED` = **not tested** · `BUILDING (deferred)`: `aphrollo gate status --wait <tree>`, never end the turn waiting. Run text: `aphrollo gate output`.
- **Read the `tdd` skill** (`~/.claude/skills/tdd/SKILL.md`) before changing code.
- **Commit gate:** laws, docs, Go vet→lint, fail-first; the merge runs the suite (`NOT RUN` = untested).
- **The primary checkout is merge-only:** work in a lane; `aphrollo gate allow primary` overrides. Refresh this block with `aphrollo install --managed-block-only --repo <lane>`.
- **Two modes, set by the request, never by habit.** *Ad hoc* (default: questions, checks, analysis, fixes, small features): you do it, with no subagents, reviewer or plan. An answer needs no lane; an edit goes in a lane (`aphrollo workspace create . lane/<name>`) you merge yourself (`aphrollo workspace merge --wait`). *Planned*: only on `/sdd` or an asked-for plan: spec, lanes, one builder each, a cold reviewer. State the mode when work starts; go from ad hoc to planned only if the user agrees.
- **Commit messages:** no attribution trailers or tool/model names (`commit-msg` rejects them).
_Managed by `aphrollo install`; do not edit._
<!-- aphrollo:end -->
