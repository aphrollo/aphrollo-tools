package tdd

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// A receipt is keyed by the tree it measured, so a commit that changes
// NOTHING a mutant could live in used to throw the proof away: a docs-only
// fix on top of a measured lane cost a second multi-hour run for a tree whose
// every Source and Test file was byte-identical. Two rules fix that, both
// decided from git's own diff rather than from anybody's memory of what they
// edited:
//
//	not-required — the lane's whole diff against the merge base is
//	               Ignore-kind. There is no mutant to generate, so there is
//	               nothing a receipt could say (issue #102: a workflow-only
//	               lane was refused while a Markdown-only lane passed).
//	carried      — a receipt exists for an EARLIER tree of the same lane, and
//	               every path that differs between that tree and this one is
//	               Ignore-kind. The measured answer still describes the code
//	               being merged, so it is re-stamped under the new tree.
//
// Neither rule ever widens what counts as proof: a single differing .rs blob
// refuses both.

// laneHasNothingToMutate reports whether the lane's diff against its merge
// base contains no Source or Test file. A diff the gate cannot compute (no
// repo root, no base, a git that failed) is never waived — the gate's own
// blind spot must not become a way past it.
func laneHasNothingToMutate(ctx receiptContext) bool {
	if ctx.RepoRoot == "" || ctx.BaseSHA == "" {
		return false
	}
	changed, ok := changedPaths(ctx.RepoRoot, ctx.BaseSHA, ctx.TipTree)
	if !ok {
		return false
	}
	return onlyIgnoreKind(ctx.RepoRoot, changed)
}

// carryReceiptForward looks for a receipt of an earlier tree that still
// describes this one, re-stamps it under the tip tree and returns its bytes.
// The re-stamped copy is written to disk so the carry is auditable and the
// next lookup is a plain hit.
func carryReceiptForward(ctx receiptContext) ([]byte, bool) {
	if ctx.RepoRoot == "" || ctx.BaseSHA == "" {
		return nil, false
	}
	for _, r := range carryCandidates(ctx) {
		changed, ok := changedPaths(ctx.RepoRoot, r.TipTree, ctx.TipTree)
		if !ok || !onlyIgnoreKind(ctx.RepoRoot, changed) {
			continue
		}
		from := r.TipTree
		r.CarriedFrom, r.TipTree = from, ctx.TipTree
		// Re-signed, because the body just changed: the gate is a trusted
		// writer re-stamping a proof it has this moment verified, and a
		// carried receipt still carrying the measured tree's MAC would read
		// as forged at the next lookup.
		signReceipt(&r)
		data, err := json.Marshal(r)
		if err != nil {
			continue
		}
		if path := MutationReceiptPathFor(ctx.TipTree); path != "" {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
				_ = writeFileAtomic(path, data)
			}
		}
		appendGateLog("premergecommit", logToken(ctx.Repo), "mutation-receipt",
			"receipt-carried:"+short(from)+"->"+short(ctx.TipTree), 0)
		return data, true
	}
	return nil, false
}

// carryCandidates are the receipts that could possibly carry onto this tip:
// same repo, same merge base, a passing verdict over a clean worktree, newest
// first. A receipt with no base_sha is never a candidate — a carry is only
// sound because the two trees are known to be points on the SAME diff.
func carryCandidates(ctx receiptContext) []MutationReceipt {
	dir := stateDir()
	if dir == "" || ctx.BaseSHA == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []MutationReceipt
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "mutation-receipt.") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var r MutationReceipt
		if err := json.Unmarshal(data, &r); err != nil {
			continue
		}
		if r.TipTree == "" || r.TipTree == ctx.TipTree || r.WorktreeDirty {
			continue
		}
		if r.Verdict != receiptVerdictPass || len(r.Unaccepted) > 0 {
			continue
		}
		if !strings.EqualFold(r.BaseSHA, ctx.BaseSHA) {
			continue
		}
		if r.Repo != "" && ctx.Repo != "" && !sameRepo(r.Repo, ctx.Repo) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FinishedAt.After(out[j].FinishedAt) })
	return out
}

// onlyIgnoreKind reports whether every changed path is one no mutant can live
// in. An empty list means the two trees are identical, which qualifies.
func onlyIgnoreKind(repoRoot string, changed []string) bool {
	for _, p := range changed {
		if classifyRepoPath(repoRoot, p) != Ignore {
			return false
		}
	}
	return true
}

// classifyRepoPath classifies a repo-RELATIVE path. A .ron resolves its
// owning crate from the filesystem, so that one extension is re-asked with
// the path made absolute; everything else is decided from the path text, and
// deliberately so — an absolute path drags the checkout's own directory names
// (`build/`, `dist/`) through ClassifyFile's ignored-segment scan.
func classifyRepoPath(repoRoot, p string) Kind {
	// A .ron's owning crate is resolved by walking the FILESYSTEM
	// (ronHasOwningCrate), so the bare, CWD-relative ClassifyFile(p) below is
	// not trustworthy for it in EITHER direction: a process whose working
	// directory sits under some unrelated crate can have that bare walk
	// climb straight into it and answer Source for a .ron that belongs to
	// nothing in repoRoot at all, not just fail to notice one that does.
	// repoRoot is the one anchor every caller means, so a known repoRoot
	// always wins for this extension, before the cheap check ever runs.
	if repoRoot != "" && strings.ToLower(path.Ext(p)) == ".ron" {
		return ClassifyFile(filepath.Join(repoRoot, filepath.FromSlash(p)))
	}
	return ClassifyFile(p)
}

// changedPaths lists the repo-relative paths that differ between two
// tree-ish revisions. false means git could not say, which every caller
// treats as "judge nothing".
func changedPaths(repoRoot, from, to string) ([]string, bool) {
	if repoRoot == "" || from == "" || to == "" {
		return nil, false
	}
	out, err := git(repoRoot, "diff", "--name-only", from, to)
	if err != nil {
		return nil, false
	}
	var paths []string
	for line := range strings.SplitSeq(out, "\n") {
		if p := strings.TrimSpace(line); p != "" {
			paths = append(paths, p)
		}
	}
	return paths, true
}

// readReceiptFile decodes one receipt from disk.
func readReceiptFile(path string) (MutationReceipt, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return MutationReceipt{}, false
	}
	var r MutationReceipt
	if err := json.Unmarshal(data, &r); err != nil {
		return MutationReceipt{}, false
	}
	return r, true
}

// writeReceiptFile publishes a receipt, atomically: a merge reading a
// half-written one would report it unreadable and block a lane for a race.
func writeReceiptFile(path string, r MutationReceipt) {
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = writeFileAtomic(path, data)
}
