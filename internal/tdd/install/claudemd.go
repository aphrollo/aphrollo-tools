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
	// MutantsAtCommit is mutants-at-commit: the commit gate mutates the lines
	// a commit adds and refuses a survivor.
	MutantsAtCommit bool
	// MutantsAtCommitBlock and MutantsAtMergeBlock are the pins: the repo
	// declared mutants-at-commit = "block" or mutants-at-merge-level = "block",
	// so a survivor refuses the commit or the merge. Without a pin every
	// finding is a report and the mutation rules are guidance.
	MutantsAtCommitBlock, MutantsAtMergeBlock bool
	// Cargo, Go and Npm are the toolchains whose manifests the repo carries;
	// the block names only their commands and commit stages (#889). Whether
	// the queue shims are on the agent's PATH is a fact about the box, so the
	// block states the shims as a condition and never as a flag.
	Cargo, Go, Npm bool
}

// measures reports whether the repo measures mutants at all, which is what
// brings the mutation rules into the block.
func (f BlockFlags) measures() bool {
	return f.MutantsAtMerge || f.MutantsBeforePR || f.MutantsAtCommit
}

// blocks reports whether a finding of the mutation run refuses anything in
// this repo, which is what makes the mutation rules binding.
func (f BlockFlags) blocks() bool {
	return f.MutantsAtCommitBlock || f.MutantsAtMergeBlock
}

// ClaudeMDBlock renders the managed block for a repo declaring f. A session
// pays for it in tokens (measure.BriefCap), so a line says what a session
// cannot infer and leaves the rest to `aphrollo <verb> --help`.
func ClaudeMDBlock(f BlockFlags) string {
	var b strings.Builder
	b.WriteString(claudeMDBegin + "\n")
	b.WriteString("## aphrollo gate\n")
	b.WriteString("- **Hooks run the tests, not you.** Read each `gate:` and `gate: deferred` line after an edit; never re-run a suite they ran. Iterate with " + iterateCommands(f) + ". Usage: `aphrollo <verb> --help`.\n")
	b.WriteString("- **Verdicts:** `green (N passed)` · `red-missing-impl` (clean RED) · `red` · `red-bogus` · `TIMEOUT`/`SKIPPED`/`QUEUED-SKIPPED` = **not tested** · `BUILDING (deferred)`: `aphrollo gate status --wait <tree>`, never end the turn waiting. Run text: `aphrollo gate output`.\n")
	// A subagent never gets the session-start nudge but does get project
	// instructions, so this is where one with no Skill tool learns the path.
	b.WriteString("- **Read the `tdd` skill** (`~/.claude/skills/tdd/SKILL.md`) before changing code.\n")
	b.WriteString("- **Commit gate:** laws, docs, " + rootStages(f) + ", fail-first; the merge runs the suite (`NOT RUN` = untested).\n")
	b.WriteString("- **The primary checkout is merge-only:** work in a lane; `aphrollo gate allow primary` overrides. Refresh this block with `aphrollo install --managed-block-only --repo <lane>`.\n")
	switch {
	case f.MutantsAtMergeCI && f.MutantsAtMergeBlock:
		b.WriteString("- **Merge:** CI's `mutants-verdict` must pass; a survivor fails it.\n")
	case f.MutantsAtMergeCI:
		b.WriteString("- **Merge:** CI's `mutants-verdict` reports survivors, refuses none.\n")
	case f.MutantsAtMerge:
		b.WriteString("- **Merge:** the gate refuses an unaccepted mutant survivor.\n")
	}
	if f.MutantsAtCommit && f.MutantsAtCommitBlock {
		b.WriteString("- **Commit:** a survivor among the added lines' mutants refuses it.\n")
	}
	// The rules that exist only because this repo measures mutants. The
	// builder agent reaches every repo, so they live here, where they reach
	// only a repo that declared the measurement.
	if f.measures() && f.blocks() {
		b.WriteString("- **Mutation:** quote one `aphrollo gate mutants prove --file <f> --old <e> --new <e> --want-fail <Test>` KILLED line per new condition; never compute a loop or scan index (`i++` in a stepping loop, `i - n`): a timed-out mutant is refused.\n")
	}
	b.WriteString("- **Two modes, set by the request, never by habit.** *Ad hoc* (default: questions, checks, analysis, fixes, small features): you do it, with no subagents, reviewer or plan. An answer needs no lane; an edit goes in a lane (`aphrollo workspace create . lane/<name>`) you merge yourself (`aphrollo workspace merge --wait`). *Planned*: only on `/sdd` or an asked-for plan: spec, lanes, one builder each, a cold reviewer. State the mode when work starts; go from ad hoc to planned only if the user agrees.\n")
	if f.Undercover {
		b.WriteString("- **Commit messages:** no attribution trailers or tool/model names (`commit-msg` rejects them).\n")
	}
	b.WriteString("_Managed by `aphrollo install`; do not edit._\n")
	b.WriteString(claudeMDEnd + "\n")
	return b.String()
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
		stages = append(stages, "cargo fmt→guards→clippy→check")
	}
	if f.Go {
		stages = append(stages, "Go vet→lint")
	}
	if f.Npm {
		stages = append(stages, "npm tsc→eslint")
	}
	if len(stages) == 0 {
		return "the toolchain's own checks"
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
		MutantsAtCommit: cfg.AtCommit,

		MutantsAtCommitBlock: cfg.AtCommitBlock,
		MutantsAtMergeBlock:  cfg.AtMergeBlock,

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
