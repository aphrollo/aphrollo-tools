package install

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// A session that does not know the gate exists fights it: it re-runs suites
// the hooks already ran, reads a TIMEOUT as a pass, hand-edits a baseline to
// get a commit through. All of that is written down — in aphrollo's README,
// which the session is not reading. So `gate init` writes the operating
// instructions into the one file a Claude session always reads, inside markers
// this tool owns: the block is REPLACED on every init, never duplicated, and
// the text has exactly one source (below), so a fix reaches every repo the
// next time init runs there.
const (
	claudeMDBegin = "<!-- aphrollo:begin -->"
	claudeMDEnd   = "<!-- aphrollo:end -->"
)

// BlockFlags are the repo's own declarations the managed block states, and
// the ONLY inputs it takes: the block is committed into the repo's CLAUDE.md
// and re-rendered by every box that runs install, so anything read from the
// box (a shim dir, a resolved home path, whether a file is written there yet)
// would dirty that tracked file on the next install anywhere else (#874).
type BlockFlags struct {
	// Undercover adds the commit-message rule for a repo that asked for it.
	Undercover bool
	// MutantsAtMerge states what THIS repo's merge actually requires rather
	// than what the tool can be told to do.
	MutantsAtMerge bool
	// MutantsBeforePR is mutants-before-pr: `workspace pr`/`ship`/`submit`
	// measure the lane first. Either switch brings in the mutation rules.
	MutantsBeforePR bool
	// MutantsAtMergeCI and MutantsBeforePRCI are the "ci" spelling of those
	// keys: the measurement is CI's `mutants-verdict` check and the local
	// gate measures nothing. Each implies its switch above.
	MutantsAtMergeCI, MutantsBeforePRCI bool
	// Cargo, Go and Npm are the toolchains whose manifests the repo carries;
	// the block names only their commands and commit stages (#889). Whether
	// the queue shims are on the agent's PATH is a fact about the box, so the
	// block states the shims as a condition and never as a flag.
	Cargo, Go, Npm bool
}

// measures reports whether the repo measures mutants at all, which is what
// brings the mutation rules into the block.
func (f BlockFlags) measures() bool { return f.MutantsAtMerge || f.MutantsBeforePR }

// ClaudeMDBlock renders the managed block for a repo declaring f.
func ClaudeMDBlock(f BlockFlags) string {
	var b strings.Builder
	b.WriteString(claudeMDBegin + "\n")
	b.WriteString("## Working with the aphrollo gate\n\n")
	b.WriteString("- **Where `aphrollo install` put the queue shims on the agent's PATH, " + shimToolsPhrase(f) + " to them** (`aphrollo gate doctor` says whether it did):\n")
	b.WriteString("  a run through a shim QUEUES visibly behind another build instead of hanging on a silent lock, and a session never exports PATH by hand.\n")
	b.WriteString("- **The hooks run the tests, not you.** After every Edit/Write, PostToolUse prints\n")
	b.WriteString("  exactly ONE `gate:` line for the edit, then one `gate: deferred` line per earlier job of the session, in any tree,\n")
	b.WriteString("  that finished since, naming its own tree and command. Read them; never re-run a suite they ran. Iterate with " + iterateCommands(f) + ", which runs nothing.\n")
	// A subagent (`builder`, `researcher`, `Explore`, ...) never gets the
	// session-start nudge — SessionStart context is not forwarded to it — but
	// project instructions ARE, so this is the one place a subagent with no
	// Skill tool can learn where the `tdd` skill lives instead of searching
	// the filesystem for it. The path is the default one, spelled from the
	// home dir, so every box renders the same line.
	b.WriteString("- **Before writing or changing code, read the `tdd` skill** at `~/.claude/skills/tdd/SKILL.md` (under `$CLAUDE_CONFIG_DIR` when set; `aphrollo install` writes it).\n")
	b.WriteString("- **What the line means:** `green (N passed)` · `red-missing-impl` (a clean RED) · `red` ·\n")
	b.WriteString("  `red-bogus` (broken test setup, not a real RED) · `TIMEOUT` / `SKIPPED` / `QUEUED-SKIPPED`\n")
	b.WriteString("  (**inconclusive — the code was NOT tested**) · `BUILDING (deferred)` (the build outran the\n")
	// The route to the run's TEXT rides on this same bullet rather than a
	// bullet of its own: the block is bounded at a length a session reads in
	// one glance, and the rule it belongs to — what a manual run is FOR — is
	// stated right here. Wanting the output was the commonest reason to
	// re-run a suite the gate had just run, and `gate stats` cannot answer
	// it, so a session told only about stats meets the refusal with no route.
	b.WriteString("  budget and continues; its result arrives at the next hook, or wait in the foreground with `aphrollo gate status --wait <tree>`, the tree the line names). The only sanctioned manual runs: a mutation proof, a deliberate soak, or ONE targeted run of the failing test after a TIMEOUT. Wanting the run's TEXT is not one of them: `aphrollo gate stats` answers what the verdict WAS, `aphrollo gate output` prints what that run actually PRINTED — assertion lines and all, unfiltered.\n")
	b.WriteString("- **Commit gate, cheapest first:** staged-baseline guard → ratchet laws → docs check →\n")
	b.WriteString("  suppression check → per root, " + rootStages(f) + ". It proves the staged test RED at HEAD, then GREEN with the change, and STOPS — the\n")
	b.WriteString("  mechanical suite runs at the MERGE; a commit prints a `NOT RUN` line naming each touched package it did not test, so an untested package is never a silent absence.\n")
	b.WriteString("- **Laws are data:** `.ratchet/laws/*.toml` (scope + one matcher + severity), with baselines in\n")
	b.WriteString("  the sibling `baselines` dir that only ever go DOWN. `aphrollo ratchet check` judges the tree\n")
	b.WriteString("  and tightens; `aphrollo ratchet test` proves each law against its fixtures. A new hit is\n")
	b.WriteString("  admitted by the law's escape comment, NEVER by editing a baseline — a raised one is rejected.\n")
	b.WriteString("- **An open point is an ISSUE, never a markdown follow-up:** `aphrollo issue \"<title>\"\n")
	b.WriteString("  --label <theme>` opens one against this repo's remote, labelled from the list it declares\n")
	b.WriteString("  (`issue-labels`), and prints the URL as its only output — never park one in a document.\n")
	b.WriteString("- **Escapes close the loop.** A red after a local green (CI, merge gate, survivor mutant, a\n")
	b.WriteString("  playtest defect a check could have caught) is recorded with `aphrollo gate escape record\n")
	b.WriteString("  <reason>`, and closed only by a stage or law named in the fix, never by a sentence in this\n")
	b.WriteString("  file. The count only goes down; `gate stats` prints it weekly at session start.\n")
	b.WriteString("- **The primary checkout is merge-only.** Once a repo has any linked worktree, the checkout holding\n")
	b.WriteString("  `main` takes merges and nothing else: the Edit/Write/Bash/PowerShell hooks are a GUARDRAIL; the git queue shim,\n")
	b.WriteString("  where it is on the agent's PATH, is the WALL (refusing `checkout -b`/`switch -c`, a move off main, a non-merge commit).\n")
	b.WriteString("  Work in a lane: `git worktree add -b lane/<name> <parent>/.worktrees/<repo>/<name> main`; override with `aphrollo gate allow primary` (works from inside a turn; `aphrollo gate revoke primary` restores it).\n")
	// The merge line is about THIS repo, not about the tool: a conditional
	// ("with `mutants-at-merge = true` ...") makes a reader go and find out
	// which half applies to them, which is the errand the block exists to
	// save them.
	if f.MutantsAtMergeCI {
		b.WriteString("- **A merge is measured in CI:** this repo declares `mutants-at-merge = \"ci\"`, so the merge gate measures nothing locally and refuses to merge unless CI's `mutants-verdict` check passed on the PR head; `aphrollo gate mutants run` measures THIS checkout by hand.\n")
	} else if f.MutantsAtMerge {
		b.WriteString("- **A merge is measured, not certified:** the pre-merge gate runs this lane's mutation measurement in the foreground and refuses an unaccepted survivor by name; `aphrollo gate mutants run` measures THIS checkout the same way before you merge.\n")
	} else {
		b.WriteString("- **A merge is checked, not measured:** this repo declares no `mutants-at-merge`, so the merge gate runs the mechanical suite and NO mutation measurement; `aphrollo gate mutants run` measures THIS checkout by hand.\n")
	}
	// The rules that exist only because this repo measures mutants. The
	// builder agent is one file per user and reaches every repo, so they live
	// here, where they reach only a repo that declared the measurement.
	if f.measures() {
		who := "this repo measures mutants"
		if f.MutantsAtMergeCI || f.MutantsBeforePRCI {
			who = "CI's `mutants-verdict` measures this repo's mutants, and the local box does not"
		}
		b.WriteString("- **Mutation rules** (" + who + "): quote one `aphrollo gate mutants prove --file <f> --old <expr> --new <expr> --want-fail <Test>`\n")
		b.WriteString("  KILLED line per new condition; UNREADABLE proves nothing. A mutant nobody can observe is removed by rewriting the code, not by an accept-list entry.\n")
		b.WriteString("  A timed-out mutant is refused like a survivor, so never compute a scan or loop index as an expression: no `i++` in a loop that already\n")
		b.WriteString("  steps `i`; consume a flag's value with a `skip` bool over a range loop; advance a scan with `i += n`, never `i - n`.\n")
	}
	b.WriteString("- **Orchestrating:** follow-ups on a lane (fix round, base merge, re-measure, red CI) resume its builder with only the delta; a fresh builder is for a new issue. A reviewer did not build the lane and re-reviews its own findings; the coordinator never edits; a brief carries only what the agent lacks.\n")
	b.WriteString("- **Housekeeping:** `aphrollo gate stats --since 7d` (pipeline health) · `aphrollo gate gc` (dry run; `--apply` reclaims stale build dirs).\n")
	if f.Undercover {
		b.WriteString("- **Commit messages** say what the change does and nothing about how it was\n")
		b.WriteString("  written: no attribution trailers, tool names, or model names. The `commit-msg`\n")
		b.WriteString("  hook rejects one and quotes the offending line.\n")
	}
	b.WriteString("\n_This block is written by `aphrollo install`: edit the template in aphrollo, never the block, which the next install overwrites._\n")
	b.WriteString(claudeMDEnd + "\n")
	return b.String()
}

// shimToolsPhrase names the commands the queue shims intercept in this repo:
// `cargo` is a fact only where the repo builds with it.
func shimToolsPhrase(f BlockFlags) string {
	if f.Cargo {
		return "`git` and `cargo` resolve"
	}
	return "`git` resolves"
}

// iterateCommands is the compile-only command of each toolchain the repo
// carries, or the generic advice when it carries none the gate knows.
func iterateCommands(f BlockFlags) string {
	var cmds []string
	if f.Cargo {
		cmds = append(cmds, "`cargo check -p <crate> --tests`")
	}
	if f.Go {
		cmds = append(cmds, "`go vet ./...`")
	}
	if f.Npm {
		cmds = append(cmds, "`npx tsc --noEmit`")
	}
	if len(cmds) == 0 {
		return "a compile-only command"
	}
	return strings.Join(cmds, " or ")
}

// rootStages is the commit gate's per-root stage list for each toolchain the
// repo carries, in the order precommit_gateroot.go runs them.
func rootStages(f BlockFlags) string {
	var stages []string
	if f.Cargo {
		stages = append(stages, "a cargo root runs fmt→guards→clippy→check→fail-first")
	}
	if f.Go {
		stages = append(stages, "a Go root runs vet→lint→fail-first")
	}
	if f.Npm {
		stages = append(stages, "an npm root runs tsc→eslint→fail-first")
	}
	if len(stages) == 0 {
		return "the toolchain's own checks, then fail-first"
	}
	return strings.Join(stages, "; ")
}

// managedBlockFor renders the block install would write into repoRoot: the
// template above, with the flags this repo declares. Every caller that needs
// to know what the block SHOULD say goes through here — the writer and the
// check that judges an on-disk block against it — so the two can never
// disagree about what "current" means.
func managedBlockFor(repoRoot string) string {
	return ClaudeMDBlock(blockFlagsFor(repoRoot))
}

// blockFlagsFor reads the flags from wherever this repo declares them:
// `[workspace.metadata.aphrollo]` in its Cargo workspace, or `[aphrollo]` in
// aphrollo.toml — the same two places the gates that enforce them read.
func blockFlagsFor(repoRoot string) BlockFlags {
	// cargoWorkspaceRoot answers repoRoot itself when no workspace encloses
	// it, so ws is never empty.
	ws := cargoWorkspaceRoot(repoRoot)
	cfg, _ := ReadMutantsConfig(repoRoot)
	f := BlockFlags{
		Undercover:      cargoAphrolloFlag(ws, "undercover") || aphrolloTomlFlag(repoRoot, "undercover"),
		MutantsAtMerge:  cfg.AtMerge,
		MutantsBeforePR: cfg.BeforePR,

		MutantsAtMergeCI:  cfg.AtMergeCI,
		MutantsBeforePRCI: cfg.BeforePRCI,
	}
	f.Cargo, f.Go, f.Npm = repoToolchains(repoRoot)
	return f
}

// repoToolchains reports which toolchain manifests the repo carries at any
// depth, tracked or not yet committed (a fresh repo is installed into before
// its first commit). A dir git cannot list carries none.
func repoToolchains(repoRoot string) (cargo, goMod, npm bool) {
	out, err := gitRead(repoRoot, "ls-files", "--cached", "--others", "--exclude-standard", "--",
		":(glob)**/Cargo.toml", ":(glob)**/go.mod", ":(glob)**/package.json")
	if err != nil {
		return false, false, false
	}
	for _, f := range strings.Fields(out) {
		switch path.Base(f) {
		case "Cargo.toml":
			cargo = true
		case "go.mod":
			goMod = true
		case "package.json":
			npm = true
		}
	}
	return cargo, goMod, npm
}

// PatchClaudeMD returns existing with the managed block replaced in place, or
// appended at the end when there is none. It reports whether anything changed,
// so a second run over an unchanged file writes nothing at all.
func PatchClaudeMD(existing []byte, block string) ([]byte, bool) {
	text := string(existing)
	crlf := strings.Contains(text, "\r\n")
	if crlf {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}

	var out string
	start := strings.Index(text, claudeMDBegin)
	end := strings.Index(text, claudeMDEnd)
	switch {
	case start >= 0 && end > start:
		// Replace IN PLACE: the block may have been put somewhere deliberate,
		// and moving it to the end on every init would churn the file forever.
		// The +1 skips the newline after the end marker — but the file can
		// END exactly at the marker with no trailing newline (a truncated
		// CLAUDE.md, or an editor that strips the final newline), in which
		// case there is no such byte to skip: clamp to len(text) so the slice
		// never runs past it (issue #286).
		tail := min(len(text), end+len(claudeMDEnd)+1)
		out = text[:start] + block + text[tail:]
	case start >= 0 || end > 0:
		// One marker without the other: the file was hand-edited mid-block.
		// Drop the orphan and append a whole one rather than nesting markers.
		out = appendBlock(dropOrphanMarkers(text), block)
	default:
		out = appendBlock(text, block)
	}
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return []byte(out), out != string(existing)
}

// appendBlock puts the block at the end, separated by exactly one blank line.
func appendBlock(text, block string) string {
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return block
	}
	return trimmed + "\n\n" + block
}

// dropOrphanMarkers removes a lone begin or end marker line.
func dropOrphanMarkers(text string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == claudeMDBegin || t == claudeMDEnd {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// ErrManagedBlockInPrimary is returned by WriteClaudeMD when repoRoot is the
// primary checkout of a repo that has any linked worktree and sits on main:
// that checkout is merge-only (the git shim refuses a commit there at all),
// so writing the block there would leave it permanently dirty with no commit
// able to clear it, blocking workspace sync and leaving the installer to build
// off a stale tree. Land the block through a lane instead.
var ErrManagedBlockInPrimary = errors.New("managed CLAUDE.md block not written: merge-only primary checkout")

// WriteClaudeMD writes the managed block into repoRoot's CLAUDE.md. force
// creates the file when there is none; without it an absent CLAUDE.md is a
// no-op, so a plain `gate init` never invents a file in a repo that keeps none
// — unless the repo measures mutants, whose builders learn the mutation rules
// from this block and nowhere else. It reports whether the file changed.
func WriteClaudeMD(repoRoot string, force bool) (bool, error) {
	if repoRoot == "" {
		return false, nil
	}
	force = force || blockFlagsFor(repoRoot).measures()
	path := filepath.Join(repoRoot, "CLAUDE.md")
	existing, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err) && !force:
		return false, nil
	case os.IsNotExist(err):
		existing = nil
	case err != nil:
		return false, fmt.Errorf("reading %s: %w", path, err)
	}

	out, changed := PatchClaudeMD(existing, managedBlockFor(repoRoot))
	if !changed {
		return false, nil
	}
	// The sentinel means "a write would have changed this file and this is
	// the merge-only primary" — checked only once a write is actually due,
	// so a primary whose block is current, or that keeps no CLAUDE.md at
	// all, never gets told it is behind.
	if _, ok := PrimaryMergeOnly(repoRoot); ok {
		return false, ErrManagedBlockInPrimary
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}
