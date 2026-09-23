package tdd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
		CacheDir:       StateDir(),
		// A diff-scoped law (symbol-removed, co-change, hunk-regex) needs the
		// commit it is about to land on top of — HEAD, both for a plain
		// commit and a merge commit — plus, for a `[scope] changed =
		// "staged"` law, the files THIS commit actually stages: without it
		// co-change/hunk-regex answer nothing on every real commit (see
		// changedInput), which is the "law that silently does nothing"
		// shape #320 is about, not a rejection anyone would ever see.
		Base:        "HEAD",
		StagedFiles: stagedFiles(repoRoot),
	})
	if err != nil {
		return ratchetCheckErrorResult(gateName, repoRoot, err, started)
	}
	// On the refusing path only (#497): a stale baseline row a PRIOR,
	// interrupted or otherwise buggy run left deleted on disk without ever
	// committing it reads today as an ordinary "baseline 0" regression, with
	// nothing in the finding pointing at .ratchet/baselines/ as the actual
	// place to look. gitBatchBlobs already runs every git subprocess this
	// hook spawns through cleanGitEnv, the same discipline
	// baselineguard_repath.go uses for the identical hazard (a nested git
	// call must not inherit this process's own GIT_DIR/GIT_INDEX_FILE).
	if res.Blocked() {
		res.Notes = append(res.Notes, ratchet.BaselineHeadRegressionNotes(repoRoot, res.RegressedBaselines,
			func(rels []string) map[string]string { return gitBatchBlobs(repoRoot, "HEAD", rels) })...)
	}
	noteNewerLaws(gateName, repoRoot, res.NewerLaws)
	noteSkippedLaws(gateName, repoRoot, res.SkippedLaws)
	for _, note := range res.Notes {
		fmt.Fprintf(os.Stderr, "gate %s: ratchet: %s\n", gateName, note)
	}
	if res.Blocked() {
		msg := fmt.Sprintf("gate %s: ratchet → REJECTED\n  %s",
			gateName, strings.Join(res.Lines(), "\n  "))
		AppendGateLog(gateName, repoRoot, "ratchet check", "ratchet-rejected", time.Since(started))
		return GateResult{Blocked: true, Message: msg}
	}
	fmt.Fprintf(os.Stderr, "gate %s: ratchet → clean (%d law(s), %d file(s))\n", gateName, res.Laws, res.FilesScanned)
	AppendGateLog(gateName, repoRoot, "ratchet check", "ratchet-clean", time.Since(started))

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
//     TOML — a broken regex, a typo'd key, a name that disagrees with its
//     file). Skipping used to read as "clean" to every stage after it, and
//     it does not just excuse ONE law — every OTHER law in the repo goes
//     unjudged with it until the box catches up (#158: one law a binary
//     could not even parse disarmed the whole ratchet stage, and `docs
//     check` alongside it, until reinstall). A gate that cannot read its own
//     laws is not a gate, so this blocks too, with a remedy: fix the law.
//     An unknown MATCHER KIND is deliberately not this shape (#440): it
//     reaches Check as a per-law skip in Result.SkippedLaws instead of an
//     error, precisely so the one law a binary predates cannot take every
//     other law down with it — see noteSkippedLaws.
//
// Both block; only the message differs, because the two need different
// fixes and a session reading gate.log should not have to guess which one
// it hit. Classification goes through errors.As against ratchet's own typed
// error, never string-matching — the design contract's "fail loud with a
// fix suggestion" applies to the classification itself, not just the block.
// Both this and ratchetFixtureStage classify a ratchet-tooling failure into
// the same REJECTED shape; a new failure class recognized here needs the
// same recognition there. The one place they now legitimately differ is
// WHICH BINARY was judging: the fixtures stage hands the laws a commit
// changes to a build of the checkout under judgement, so a failure there can
// also mean that build itself, while check has no such build and its whole
// answer comes from the running binary. That is why only the fixtures stage
// carries a lane-build outcome, and why both remedies name `aphrollo update`
// rather than any path that rebuilds the box binary from an unmerged tree.
// twin-diverges-ok: stageOutcome field rename; the twin uses none of these fields
// twin: internal/tdd/ratchetgate.go#ratchetFixtureStage
func ratchetCheckErrorResult(gateName, repoRoot string, err error, started time.Time) GateResult {
	var readErr *ratchet.ScanReadError
	if errors.As(err, &readErr) {
		msg := fmt.Sprintf(
			"gate %s: ratchet → REJECTED (a scoped file could not be read: %v)\n  the commit cannot be judged against %s — retry once the lock or permission clears",
			gateName, err, readErr.Path)
		fmt.Fprintln(os.Stderr, msg)
		AppendGateLog(gateName, repoRoot, "ratchet check", "ratchet-rejected", time.Since(started))
		return GateResult{Blocked: true, Message: msg}
	}
	msg := fmt.Sprintf(
		"gate %s: ratchet → REJECTED (the law tooling could not run: %v)\n  fix the law file named above, or run `aphrollo update` if this binary predates a schema a law declares",
		gateName, err)
	fmt.Fprintln(os.Stderr, msg)
	AppendGateLog(gateName, repoRoot, "ratchet check", "ratchet-rejected", time.Since(started))
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
		AppendGateLog(gateName, repoRoot, "ratchet check", "ratchet-law-newer:"+LogToken(l.Name), 0)
	}
}

// noteSkippedLaws reports every law whose [matcher].kind this binary does
// not compile in at all — the #440 bootstrap hazard: a lane lands a new
// matcher kind before every checkout on the box rebuilds from it, and the
// old fix (reject the commit) took every OTHER law down with the one this
// binary cannot read. It warns once per law on stderr, loudly enough to name
// the law and the unknown kind, and leaves a `standdown-unknown-matcher-
// kind:<law>` line in gate.log — counted the same way every other stand-down
// is (see denyVerdictPrefixes), because a law that silently stops enforcing
// is the exact defect #320 is about.
func noteSkippedLaws(gateName, repoRoot string, laws []ratchet.SkippedLaw) {
	for _, l := range laws {
		fmt.Fprintf(os.Stderr, "gate %s: ratchet law %q declares matcher kind %q, unknown to this binary — SKIPPED, not judged; rebuild aphrollo\n",
			gateName, l.Name, l.Kind)
		AppendGateLog(gateName, repoRoot, "ratchet check", "standdown-unknown-matcher-kind:"+LogToken(l.Name), 0)
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

// stagedTouchesLaws reports whether this commit changes something that can
// actually change a fixture's verdict: a law itself, or a fixture. Anything
// else under `.ratchet/` — the generated README, the dev-instrument
// registry, notes — cannot move a fixture result, so it must not fire this
// stage. `.ratchet/baselines/**` is deliberately excluded from this list: a
// staged baseline is judged by baselineStage, which runs ahead of this one
// in precommitDecide, so it needs no second pass here.
func stagedTouchesLaws(repoRoot string) bool {
	for _, f := range stagedFiles(repoRoot) {
		rel := filepath.ToSlash(f)
		if strings.HasPrefix(rel, ".ratchet/laws/") || strings.HasPrefix(rel, ".ratchet/fixtures/") {
			return true
		}
	}
	return false
}

func ratchetFixtureStage(gateName, repoRoot string) GateResult {
	started := time.Now()
	// Laws this commit changes are judged by a build of the checkout under
	// judgement, everything else by the installed binary — the exact split
	// ratchetgate_lanebuild.go's header describes, and the reason a matcher
	// correction can now carry the fixture rows that prove it (#659, #673).
	lane := laneJudgedLaws(repoRoot)
	results, err := ratchet.RunFixturesWith(repoRoot, ratchet.FixtureOptions{Except: lane})
	if err != nil {
		// The same shape #158 fixed for ratchet check: a law this binary's
		// schema cannot even parse must not disarm the fixture proof for
		// every OTHER law alongside it.
		return verdictFor(gateName, "ratchet-fixtures", repoRoot, "ratchet test", stageOutcome{
			Kind: outcomeCheckError,
			Err:  err,
			Message: fmt.Sprintf(
				"gate %s: ratchet fixtures → REJECTED (the law tooling could not run: %v)\n  fix the law file named above, or run `aphrollo update` if this binary predates a schema a law declares",
				gateName, err),
		})
	}
	if len(lane) > 0 {
		laneResults, laneErr := laneJudgedFixtures(gateName, repoRoot, lane)
		if laneErr != nil {
			return verdictFor(gateName, "ratchet-fixtures", repoRoot, "ratchet test", stageOutcome{
				Kind: outcomeCheckError,
				Err:  laneErr,
				Message: fmt.Sprintf(
					"gate %s: ratchet fixtures → REJECTED (this checkout's own build could not judge the law(s) it changes: %v)\n  %s",
					gateName, laneErr, laneBuildFixHint),
			})
		}
		results = append(results, laneResults...)
		sort.Slice(results, func(i, j int) bool { return results[i].Law < results[j].Law })
	}
	var failures []string
	tested := 0
	for _, r := range results {
		// Already warned and counted by noteSkippedLaws, which ran ahead of
		// this stage in ratchetStage: a law this binary cannot even build a
		// Matcher for has no fixture verdict to report, ok or failed.
		if r.Skipped {
			continue
		}
		tested++
		for _, f := range r.Failures {
			failures = append(failures, r.Law+": "+f)
		}
	}
	if len(failures) > 0 {
		AppendGateLog(gateName, repoRoot, "ratchet test", "ratchet-rejected", time.Since(started))
		return GateResult{Blocked: true, Message: fmt.Sprintf(
			"gate %s: ratchet fixtures → REJECTED\n  %s", gateName, strings.Join(failures, "\n  "))}
	}
	fmt.Fprintf(os.Stderr, "gate %s: ratchet fixtures → green (%d law(s))\n", gateName, tested)
	AppendGateLog(gateName, repoRoot, "ratchet test", "green", time.Since(started))
	return GateResult{}
}
