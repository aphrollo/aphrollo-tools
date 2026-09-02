package tdd

import (
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
	fmt.Fprintf(&b, "- **PATH, queue shim first** — bash `export PATH=\"%s:$PATH\"` · PowerShell\n", dir)
	fmt.Fprintf(&b, "  `$env:Path = \"%s;$env:Path\"`. A `cargo`/`git` run through the shim QUEUES\n", dir)
	b.WriteString("  visibly behind another build instead of hanging on a silent lock.\n")
	b.WriteString("- **The hooks run the tests, not you.** After every Edit/Write, PostToolUse prints\n")
	b.WriteString("  exactly ONE `gate:` line. Read it; never re-run a suite it just ran. Iterate with\n")
	b.WriteString("  `cargo check -p <crate> --tests`, which runs nothing.\n")
	b.WriteString("- **What the line means:** `green (N passed)` · `red-missing-impl` (a clean RED) ·\n")
	b.WriteString("  `red` · `red-bogus` (broken test setup, not a real RED) · `TIMEOUT` / `SKIPPED` /\n")
	b.WriteString("  `QUEUED-SKIPPED` (**inconclusive — the code was NOT tested**) · `BUILDING (deferred)`\n")
	b.WriteString("  (the build outran the budget and continues; its result arrives at the next hook).\n")
	b.WriteString("  The only sanctioned manual runs: a mutation proof, a deliberate soak, or ONE\n")
	b.WriteString("  targeted `-p <crate> <filter>` after the hook itself said TIMEOUT/SKIPPED.\n")
	b.WriteString("- **Commit gate, cheapest first:** staged-baseline guard → ratchet laws → `cargo fmt`\n")
	b.WriteString("  → always-run guards → clippy → workspace check → fail-first RED proof → the\n")
	b.WriteString("  touched crates' suites. It stops at the first rejection and names the stage.\n")
	b.WriteString("- **Laws are data:** `.ratchet/laws/*.toml` (scope + one matcher + severity), with\n")
	b.WriteString("  baselines under `.ratchet/baselines/` that only ever go DOWN. `aphrollo ratchet\n")
	b.WriteString("  check` judges the tree and tightens; `aphrollo ratchet test` proves each law against\n")
	b.WriteString("  its fixtures. A new hit is admitted by the law's escape comment, NEVER by editing a\n")
	b.WriteString("  baseline — the gate rejects a raised one.\n")
	b.WriteString("- **Housekeeping:** `aphrollo gate stats --since 7d` (pipeline health) ·\n")
	b.WriteString("  `aphrollo gate gc` (dry run; `--apply` reclaims stale build dirs).\n")
	if undercover {
		b.WriteString("- **Commit messages** say what the change does and nothing about how it was\n")
		b.WriteString("  written: no attribution trailers, tool names, or model names. The `commit-msg`\n")
		b.WriteString("  hook rejects one and quotes the offending line.\n")
	}
	b.WriteString("\n_This block is written by `aphrollo gate init`. Edit the template in aphrollo, not\n")
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
		out = text[:start] + block + text[end+len(claudeMDEnd)+1:]
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
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}
