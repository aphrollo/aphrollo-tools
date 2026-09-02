package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// The edit-time half of the law engine. A law that can only speak under
// `cargo test` speaks after the write, after the build, after the commit — so
// the offence is already in the tree and the fix is a second edit. Here the
// same law judges the content the tool is ABOUT to write: a deny law with a
// new hit denies the write and says which law, where, and what its escape
// comment is; a warn law says it once and lets the write through.
//
// Everything here fails OPEN. A malformed payload, an unreadable file, a
// broken law file — none of them may wedge a session over a rule that is
// itself broken.

// ratchetEditInput is the slice of the PreToolUse payload needed to reconstruct
// the content an edit would leave on disk.
type ratchetEditInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath   string `json:"file_path"`
		Content    string `json:"content"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
		Edits      []struct {
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all"`
		} `json:"edits"`
	} `json:"tool_input"`
}

// RatchetAdvisory evaluates the consuming repo's laws against the content an
// edit would produce. Allow when there is no repo, no law, nothing in scope,
// or nothing new.
func RatchetAdvisory(raw []byte) Decision {
	var in ratchetEditInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return Decision{}
	}
	switch in.ToolName {
	case "Edit", "Write", "MultiEdit":
	default:
		return Decision{}
	}
	path := in.ToolInput.FilePath
	if path == "" {
		return Decision{}
	}
	root := RepoRoot(filepath.Dir(path))
	if root == "" || !ratchet.HasLaws(root) {
		return Decision{}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return Decision{}
	}
	relSlash := filepath.ToSlash(rel)
	if strings.HasPrefix(relSlash, "../") {
		return Decision{}
	}
	content, ok := proposedContent(in, path)
	if !ok {
		return Decision{}
	}

	res, err := ratchet.Check(ratchet.Options{
		Root:     root,
		Proposed: map[string]string{relSlash: content},
		Files:    []string{relSlash},
	})
	if err != nil || len(res.Findings) == 0 {
		return Decision{}
	}
	action := Warn
	if res.Blocked() {
		action = Block
	}
	return Decision{
		Action: action,
		// One hit per line: each carries its own remedy at the end, and
		// running them together on one line is where that remedy scrolls off.
		Reason: "ratchet: " + strings.Join(res.Lines(), "\nratchet: "),
		Policy: "ratchet:" + res.Findings[0].Law,
	}
}

// proposedContent reconstructs what the file would hold after the edit. A
// Write carries it outright; an Edit or MultiEdit is applied to what is on
// disk, in order, exactly as the tool would.
func proposedContent(in ratchetEditInput, path string) (string, bool) {
	if in.ToolName == "Write" {
		return in.ToolInput.Content, true
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", false
	}
	content := string(data)
	if in.ToolName == "Edit" {
		return applyEdit(content, in.ToolInput.OldString, in.ToolInput.NewString, in.ToolInput.ReplaceAll), true
	}
	for _, e := range in.ToolInput.Edits {
		content = applyEdit(content, e.OldString, e.NewString, e.ReplaceAll)
	}
	return content, true
}

// applyEdit is the Edit tool's own substitution: the first occurrence, or every
// one under replace_all. An empty old string means the edit creates the file.
func applyEdit(content, old, replacement string, all bool) string {
	if old == "" {
		return replacement
	}
	if all {
		return strings.ReplaceAll(content, old, replacement)
	}
	return strings.Replace(content, old, replacement, 1)
}

// ratchetStage judges the whole tree against its declared laws before any
// suite compiles: it is milliseconds warm, and a law is exactly the kind of
// rule that must be answered before the expensive stages, not after them. It
// never TIGHTENS a baseline — a commit gate that rewrote a file mid-commit
// would leave the lowered ceiling unstaged and the next run confused; lowering
// is `aphrollo ratchet check`'s job, run deliberately.
func ratchetStage(gateName, repoRoot string) GateResult {
	if repoRoot == "" || !ratchet.HasLaws(repoRoot) {
		return GateResult{}
	}
	started := time.Now()
	res, err := ratchet.Check(ratchet.Options{
		Root:     repoRoot,
		Proposed: indexOverlay(repoRoot),
		Tracked:  trackedFiles(repoRoot),
		CacheDir: stateDir(),
	})
	if err != nil {
		// A broken law file is a defect in the rule, not in the commit: say so
		// loudly and let the commit through rather than wedging every commit
		// in the repo behind a typo.
		line := fmt.Sprintf("gate %s: ratchet → skipped (%v)", gateName, err)
		fmt.Fprintln(os.Stderr, line)
		appendGateLog(gateName, repoRoot, "ratchet check", "ratchet-skipped", time.Since(started))
		return GateResult{Message: line}
	}
	noteNewerLaws(gateName, repoRoot, res.NewerLaws)
	if res.Blocked() {
		msg := fmt.Sprintf("gate %s: ratchet → REJECTED\n  %s",
			gateName, strings.Join(res.Lines(), "\n  "))
		appendGateLog(gateName, repoRoot, "ratchet check", "ratchet-rejected", time.Since(started))
		return GateResult{Blocked: true, Message: msg}
	}
	fmt.Fprintf(os.Stderr, "gate %s: ratchet → clean (%d law(s), %d file(s))\n", gateName, res.Laws, res.FilesScanned)
	appendGateLog(gateName, repoRoot, "ratchet check", "ratchet-clean", time.Since(started))

	if !stagedTouchesLaws(repoRoot) {
		return GateResult{}
	}
	return ratchetFixtureStage(gateName, repoRoot)
}

// noteNewerLaws reports every law whose declared schema this binary is too
// old to read in full. It warns once per law on stderr and leaves one
// `ratchet-law-newer:<law>` line in gate.log: a rule read with half its keys
// skipped reports clean exactly like a rule that is being obeyed, so the
// difference has to be stated somewhere a tally can see it.
func noteNewerLaws(gateName, repoRoot string, laws []ratchet.NewerLaw) {
	for _, l := range laws {
		fmt.Fprintf(os.Stderr, "gate %s: ratchet law %q declares schema %d; this binary supports %d — unknown keys skipped\n",
			gateName, l.Name, l.Schema, ratchet.SchemaVersion)
		appendGateLog(gateName, repoRoot, "ratchet check", "ratchet-law-newer:"+logToken(l.Name), 0)
	}
}

// trackedFiles is every path in the index — the exact set a commit can
// contain, newly staged files included. A shared checkout carries another
// session's scaffolding and a generator's leftovers beside the code, and a
// merge refused over a file nobody is committing cannot be cleared by
// changing anything in the merge. nil when git cannot answer, which falls
// back to walking the disk: a gate whose own tooling tripped judges more,
// never less.
func trackedFiles(repoRoot string) []string {
	out, err := gitRead(repoRoot, "ls-files")
	if err != nil {
		return nil
	}
	var files []string
	for line := range strings.SplitSeq(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if rel := strings.TrimSpace(line); rel != "" {
			files = append(files, filepath.ToSlash(rel))
		}
	}
	return files
}

// indexOverlay is what the laws must judge at commit time: the INDEX, the
// content this commit will actually contain. Only files whose worktree copy
// differs from the index need an entry — everything else already reads the
// same either way — so the overlay is exactly the dirty set, each keyed by
// repo-relative slash path and carrying its staged blob. Without it the gate
// read the worktree, which rejects an unstaged edit nobody is committing and
// lets a staged regression through whenever a later unstaged edit tidies the
// disk copy.
func indexOverlay(repoRoot string) map[string]string {
	out, err := git(repoRoot, "diff", "--name-only", "-M")
	if err != nil {
		return nil
	}
	overlay := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		rel := strings.TrimSpace(line)
		if rel == "" {
			continue
		}
		// No index entry means the commit deletes it; the scan reading the
		// leftover file is the lesser wrong, and inventing content is worse.
		if blob, ok := gitBlob(repoRoot, ":"+rel); ok {
			overlay[filepath.ToSlash(rel)] = blob
		}
	}
	if len(overlay) == 0 {
		return nil
	}
	return overlay
}

// stagedTouchesLaws reports whether this commit changes the laws themselves —
// the one time their own fixtures have to be re-proved.
func stagedTouchesLaws(repoRoot string) bool {
	for _, f := range stagedFiles(repoRoot) {
		if strings.HasPrefix(filepath.ToSlash(f), ".ratchet/") {
			return true
		}
	}
	return false
}

func ratchetFixtureStage(gateName, repoRoot string) GateResult {
	started := time.Now()
	results, err := ratchet.RunFixtures(repoRoot)
	if err != nil {
		line := fmt.Sprintf("gate %s: ratchet fixtures → skipped (%v)", gateName, err)
		fmt.Fprintln(os.Stderr, line)
		return GateResult{Message: line}
	}
	var failures []string
	for _, r := range results {
		for _, f := range r.Failures {
			failures = append(failures, r.Law+": "+f)
		}
	}
	if len(failures) > 0 {
		appendGateLog(gateName, repoRoot, "ratchet test", "ratchet-rejected", time.Since(started))
		return GateResult{Blocked: true, Message: fmt.Sprintf(
			"gate %s: ratchet fixtures → REJECTED\n  %s", gateName, strings.Join(failures, "\n  "))}
	}
	fmt.Fprintf(os.Stderr, "gate %s: ratchet fixtures → green (%d law(s))\n", gateName, len(results))
	appendGateLog(gateName, repoRoot, "ratchet test", "green", time.Since(started))
	return GateResult{}
}
