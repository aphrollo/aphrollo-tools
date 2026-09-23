package ratchet

import (
	"fmt"
	"path/filepath"
)

// RegressedBaseline names one law with at least one Finding a run produced,
// and the baseline file responsible for it — Check's own record of "which
// file would a caller ask about" (see Result.RegressedBaselines), built
// without any git I/O so it is exactly as free of the hook-environment
// hazard as the rest of this package's pure logic.
type RegressedBaseline struct {
	// Law is the offending law's name.
	Law string
	// Path is the baseline file, repo-relative slash path — Law.Baseline's
	// own value, so a caller can join it against Root or address it at a
	// git ref without re-deriving it.
	Path string
	// Form is the shape Path parses under (see ParseBaseline): a caller
	// comparing Path's content at two points in time must parse both sides
	// the same way this law's own baseline is parsed.
	Form Form
}

// DeletedBaselineRows counts every literal row headText records that nowText
// no longer carries AT ALL — a key that vanished, never a key whose count
// merely dropped. Baseline.LiteralKeyCounts is the identity this compares:
// tightening a ceiling down (the sanctioned direction, see Baseline.Tighten)
// rewrites a row's COUNT, which changes the line's text but keeps its key,
// so a plain line-by-line text diff would misread every ordinary tightening
// as a deletion. Only a row whose key is fully absent counts here.
func DeletedBaselineRows(headText, nowText string, form Form) (int, error) {
	head, err := ParseBaseline(headText, form)
	if err != nil {
		return 0, fmt.Errorf("HEAD baseline: %w", err)
	}
	now, err := ParseBaseline(nowText, form)
	if err != nil {
		return 0, fmt.Errorf("current baseline: %w", err)
	}
	nowKeys := now.LiteralKeyCounts()
	deleted := 0
	for key := range head.LiteralKeyCounts() {
		if _, ok := nowKeys[key]; !ok {
			deleted++
		}
	}
	return deleted, nil
}

// BaselineHeadRegressionNotes answers one advisory line per RegressedBaseline
// whose file has lost a row relative to HEAD that THIS run did not remove —
// a run reporting any regression at all never writes any baseline (Check
// defers every write to commitTightened, called only when res.Findings is
// empty), so a row missing here was already missing before this run started:
// the aftermath of an interrupted or otherwise stale write (#497), a hand
// edit, or a rebase that dropped it. The note names none of those causes —
// `git diff` on the path is what distinguishes them, and the remedy (restore
// from HEAD, re-run) is the same regardless.
//
// headContent answers every regressed baseline's committed HEAD text in ONE
// round trip, the same "batch, one subprocess" shape RepathCountedKeys's own
// callers already use; an entry missing from its result reads as "content
// unknown at HEAD" (a law adopted in this very commit has no HEAD copy at
// all, and that is not a finding), never eligible to compare. This function
// does no git I/O itself: a caller nested inside a hook (a commit-gate
// process that must not let a git subprocess inherit the hook's own
// GIT_DIR/GIT_INDEX_FILE) builds headContent with its own scrubbed
// exec.Command, exactly the discipline RepathCountedKeys already asks of its
// callers — see internal/tdd/lawgate/baselineguard_repath.go.
func BaselineHeadRegressionNotes(root string, regs []RegressedBaseline, headContent func(rels []string) map[string]string) []string {
	if len(regs) == 0 {
		return nil
	}
	rels := make([]string, len(regs))
	for i, r := range regs {
		rels[i] = r.Path
	}
	head := headContent(rels)
	var notes []string
	for _, r := range regs {
		headText, ok := head[r.Path]
		if !ok {
			continue // absence-ok: no HEAD copy, or a failed read -- never eligible to compare
		}
		nowBytes, err := readFile(filepath.Join(root, filepath.FromSlash(r.Path)))
		if err != nil {
			continue // absence-ok: unreadable now has nothing to compare against
		}
		deleted, err := DeletedBaselineRows(headText, string(nowBytes), r.Form)
		if err != nil || deleted == 0 {
			continue
		}
		notes = append(notes, fmt.Sprintf(
			"%s has %d %s deleted relative to HEAD that this run did not delete\n"+
				"      (`git diff %s`) — an interrupted or refused earlier run may have\n"+
				"      dropped it; restoring the file from HEAD and re-running is the check",
			r.Path, deleted, plural(deleted, "row"), r.Path))
	}
	return notes
}
