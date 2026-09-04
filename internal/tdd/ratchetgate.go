package tdd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// ratchetCheckFn is ratchet.Check, indirected so a test can substitute a
// failure ratchetStage's own error classification must react to without
// constructing an OS-level unreadable file (not portably possible — see
// TestRatchetStage_BlocksWhenAScopedFileCannotBeRead).
var ratchetCheckFn = ratchet.Check

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
	// repoRootNear, not RepoRoot: a Write CREATES its parent directories, so
	// the directory this path names routinely does not exist yet, and git
	// asked from a missing directory answers "not a repository" — which read
	// as "no laws here" and let the first file of a new module through.
	root := repoRootNear(filepath.Dir(path))
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
	// Narrow the verdict to what this edit ADDS: an edit that lowers or
	// keeps a law's count in this file must not be refused for the hits it
	// leaves behind (see ratchetedit.go). The baseline comparison above is
	// still what selects a finding at all, so this only ever allows more.
	res.Findings = editRegressions(root, relSlash, onDiskContent(path), content, res)
	if len(res.Findings) == 0 {
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

// onDiskContent is the file as it stands right now, "" when it is not there
// yet — which is the correct zero for a Write that creates it.
func onDiskContent(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
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
	res, err := ratchetCheckFn(ratchet.Options{
		Root:           repoRoot,
		Proposed:       indexOverlay(repoRoot),
		Tracked:        trackedFiles(repoRoot),
		TrackedIgnored: trackedIgnoredFiles(repoRoot),
		CacheDir:       stateDir(),
	})
	if err != nil {
		return ratchetCheckErrorResult(gateName, repoRoot, err, started)
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

// ratchetCheckErrorResult classifies a ratchetCheckFn failure and decides the
// commit's fate. Two shapes reach here, and they are NOT the same offence —
// treating every Check() error as "tooling problem, skip it" was the defect
// (cold review against #164, this stage untouched by that fix):
//
//   - a *ratchet.ScanReadError: a file or directory IN SCOPE could not be
//     read (locked, permission-denied, any I/O error other than the path
//     vanishing mid-walk, which stays silent inside Check itself). The
//     commit cannot be judged against data nobody looked at, so this BLOCKS
//     and names the path — the concrete #164 scenario: a tracked .rs file
//     locked by an editor swap file or an AV scan while `git commit` runs.
//   - anything else: the law tooling could not even START (a malformed law
//     TOML, a matcher kind this binary's schema predates). Skipping used to
//     read as "clean" to every stage after it, and it does not just excuse
//     ONE law — every OTHER law in the repo goes unjudged with it until the
//     box catches up (#158: one law a binary is too old to parse disarmed
//     the whole ratchet stage, and `docs check` alongside it, until
//     reinstall). A gate that cannot read its own laws is not a gate, so
//     this blocks too, with a remedy: fix the law, or reinstall aphrollo.
//
// Both block; only the message differs, because the two need different
// fixes and a session reading gate.log should not have to guess which one
// it hit. Classification goes through errors.As against ratchet's own typed
// error, never string-matching — the design contract's "fail loud with a
// fix suggestion" applies to the classification itself, not just the block.
func ratchetCheckErrorResult(gateName, repoRoot string, err error, started time.Time) GateResult {
	var readErr *ratchet.ScanReadError
	if errors.As(err, &readErr) {
		msg := fmt.Sprintf(
			"gate %s: ratchet → REJECTED (a scoped file could not be read: %v)\n  the commit cannot be judged against %s — retry once the lock or permission clears",
			gateName, err, readErr.Path)
		fmt.Fprintln(os.Stderr, msg)
		appendGateLog(gateName, repoRoot, "ratchet check", "ratchet-rejected", time.Since(started))
		return GateResult{Blocked: true, Message: msg}
	}
	msg := fmt.Sprintf(
		"gate %s: ratchet → REJECTED (the law tooling could not run: %v)\n  fix the law file named above, or reinstall aphrollo if it predates a matcher kind or schema a law declares",
		gateName, err)
	fmt.Fprintln(os.Stderr, msg)
	appendGateLog(gateName, repoRoot, "ratchet check", "ratchet-rejected", time.Since(started))
	return GateResult{Blocked: true, Message: msg}
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
// `-z` is not optional: without it git QUOTES and escapes a path carrying a
// non-ASCII byte, and the quoted spelling matches nothing on disk — the file
// drops out of the tracked set and its offence is never measured.
func trackedFiles(repoRoot string) []string {
	out, err := gitRead(repoRoot, "ls-files", "-z")
	if err != nil {
		return nil
	}
	return nulPaths(out)
}

// trackedIgnoredFiles is the subset of the index that .gitignore ALSO matches
// — a repo that ignores a whole extension (borld ignores `*.md`) still tracks
// those files. The disk walk hands such a file only to a law that opted in
// with `ignore_gitignore`, so the tracked set has to carry the same flag or
// the commit gate would silently judge more than `ratchet check` does.
func trackedIgnoredFiles(repoRoot string) []string {
	out, err := gitRead(repoRoot, "ls-files", "-z", "-i", "-c", "--exclude-standard")
	if err != nil {
		return nil
	}
	return nulPaths(out)
}

// nulPaths splits a NUL-separated git path list into slash paths.
func nulPaths(out string) []string {
	var files []string
	for rec := range strings.SplitSeq(out, "\x00") {
		if rel := strings.TrimSpace(rec); rel != "" {
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
