package tdd

import (
	"errors"
	"fmt"
	"os"
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

// ClaudeMDBlock renders the managed block. shimDir is the queue-shim
// directory a session prepends to PATH; undercover adds the commit-message
// rule for a workspace that asked for it.
func ClaudeMDBlock(shimDir string, undercover bool) string {
	dir := shellPath(shimDir)
	var b strings.Builder
	b.WriteString(claudeMDBegin + "\n")
	b.WriteString("## Working with the aphrollo gate\n\n")
	fmt.Fprintf(&b, "- **`cargo` and `git` resolve to the queue shim** (`which cargo` prints a path under\n")
	fmt.Fprintf(&b, "  `%s`); the user PATH and the shell profiles put it first, so a session never exports\n", dir)
	b.WriteString("  PATH by hand. A run through the shim QUEUES visibly behind another build instead of\n")
	b.WriteString("  hanging on a silent lock; if `which` prints the raw toolchain, the profile is broken: say so.\n")
	b.WriteString("- **The hooks run the tests, not you.** After every Edit/Write, PostToolUse prints\n")
	b.WriteString("  exactly ONE `gate:` line. Read it; never re-run a suite it just ran. Iterate with\n")
	b.WriteString("  `cargo check -p <crate> --tests`, which runs nothing.\n")
	b.WriteString("- **What the line means:** `green (N passed)` · `red-missing-impl` (a clean RED) · `red` ·\n")
	b.WriteString("  `red-bogus` (broken test setup, not a real RED) · `TIMEOUT` / `SKIPPED` / `QUEUED-SKIPPED`\n")
	b.WriteString("  (**inconclusive — the code was NOT tested**) · `BUILDING (deferred)` (the build outran the\n")
	b.WriteString("  budget and continues; its result arrives at the next hook). The only sanctioned manual runs: a mutation proof, a deliberate soak, or ONE targeted `-p <crate> <filter>` after a TIMEOUT.\n")
	b.WriteString("- **Commit gate, cheapest first:** staged-baseline guard → ratchet laws → docs check →\n")
	b.WriteString("  suppression check → per root: cargo sequential (fmt→guards→clippy→check→fail-first→\n")
	b.WriteString("  suites); a Go root also runs vet/lint first; every non-cargo root then runs fail-first\n")
	b.WriteString("  and the mechanical suite CONCURRENTLY.\n")
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
	b.WriteString("  `main` takes merges and nothing else: the Edit/Write/Bash/PowerShell hooks are a GUARDRAIL, the\n")
	b.WriteString("  git shim (refusing `checkout -b`/`switch -c`, a move off main, a non-merge commit) is the WALL.\n")
	b.WriteString("  Work in a lane: `git worktree add -b lane/<name> <parent>/.worktrees/<repo>/<name> main`; override\n")
	b.WriteString("  with `aphrollo gate allow primary` (works from inside a turn; `aphrollo gate revoke primary` restores it).\n")
	b.WriteString("- **A mutation receipt is earned by the COMMIT:** `gate postcommit` starts the lane's run; `aphrollo gate mutants run` (no arguments, in the lane) runs one in the FOREGROUND. Never a repo's own producer script — the box-wide lock wraps the CALL, so a hand-run script is outside it.\n")
	b.WriteString("- **Housekeeping:** `aphrollo gate stats --since 7d` (pipeline health) · `aphrollo gate gc` (dry run; `--apply` reclaims stale build dirs).\n")
	if undercover {
		b.WriteString("- **Commit messages** say what the change does and nothing about how it was\n")
		b.WriteString("  written: no attribution trailers, tool names, or model names. The `commit-msg`\n")
		b.WriteString("  hook rejects one and quotes the offending line.\n")
	}
	b.WriteString("\n_This block is written by `aphrollo install`. Edit the template in aphrollo, not\n")
	b.WriteString("the block — the next init overwrites whatever is between the markers._\n")
	b.WriteString(claudeMDEnd + "\n")
	return b.String()
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
// able to clear it, blocking workspace sync and leaving self-install to build
// off a stale tree. Land the block through a lane instead.
var ErrManagedBlockInPrimary = errors.New("managed CLAUDE.md block not written: merge-only primary checkout")

// WriteClaudeMD writes the managed block into repoRoot's CLAUDE.md. force
// creates the file when there is none; without it an absent CLAUDE.md is a
// no-op, so a plain `gate init` never invents a file in a repo that keeps none.
// It reports whether the file changed.
func WriteClaudeMD(repoRoot, shimDir string, force bool) (bool, error) {
	if repoRoot == "" {
		return false, nil
	}
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

	ws := cargoWorkspaceRoot(repoRoot)
	if ws == "" {
		ws = repoRoot
	}
	out, changed := PatchClaudeMD(existing, ClaudeMDBlock(shimDir, cargoAphrolloFlag(ws, "undercover")))
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
