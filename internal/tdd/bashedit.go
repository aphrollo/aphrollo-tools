package tdd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A `sed -i`, a heredoc, a `gofmt -w`, a generator — every one of them is an
// edit, and none of them fires the Edit hooks. So the gate stopped at the
// shell: the same change that is denied through Edit lands silently through
// Bash, and the suite never runs. That is not a hole a rule can close; it
// needs the hooks to see the shell too.
//
// PreToolUse asks git ONCE what is dirty (`git status --porcelain -uall -z`),
// keeps the hash of that answer plus a size+mtime stamp for the dirty SOURCE
// paths only, and PostToolUse asks again and diffs. The cost is O(dirty), not
// O(tracked): an earlier version listed every tracked file and stat'd it, so
// a bare `ls` paid two full index walks and a stat per source file in the
// tree, then wrote ~140 KB of state twice.
//
// The snapshot is keyed by the TOOL CALL, not the session. Claude batches
// tool calls, so two Bash calls interleave PreA, PreB, PostA, PostB — with
// one slot per session, B's Pre overwrote A's, A's Post consumed it, and B's
// shell edit reached nothing.
//
// Two things it deliberately does NOT do. It cannot deny a smell before the
// write: the content does not exist until the command has run, so the
// pre-edit half is a snapshot and nothing else — the commit gate is where a
// smell introduced through the shell is caught. And it runs a project's
// suite ONCE however many files the command rewrote: the suite answers for
// all of them at once, so a per-file loop would charge the same build twenty
// times and leave twenty detached ones behind it.

// maxBashSnapshots bounds what one session holds. A Pre whose Post never
// arrives (a cancelled call, a crashed hook) would otherwise accumulate in
// the session file forever; past this the oldest is dropped.
const maxBashSnapshots = 16

// bashInput is the slice of a Bash hook payload these two need. The command
// itself is read for ONE question — which tree this call is answerable for
// (bashSnapshotDir) — never to judge what the command does.
type bashInput struct {
	SessionID string `json:"session_id"`
	ToolUseID string `json:"tool_use_id"`
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// bashSnapshot is the tree as it stood before one shell command ran.
type bashSnapshot struct {
	Root string `json:"root"`
	// StatusHash fingerprints `git status --porcelain -uall -z`: unchanged
	// means no file entered or left the dirty set, which is the cheap
	// no-op answer for the commands that read rather than write.
	StatusHash string `json:"status_hash"`
	// Dirty stamps each dirty SOURCE path "<size>:<mtime-unix-nanos>". The
	// hash alone cannot see a second edit to a file that was already
	// modified — the status line is identical before and after — and that is
	// the common case, so the stamps carry it.
	// bound: one entry per dirty source path, not per tracked file.
	Dirty map[string]string `json:"dirty"`
	// MergeHead is the commit MERGE_HEAD named when the snapshot was taken,
	// "" when no merge was in progress. It is what lets the harvest tell a
	// command that CONCLUDED a merge from one that edited the merged paths.
	MergeHead string    `json:"merge_head,omitempty"`
	At        time.Time `json:"at"`
}

// IsBashHook reports whether a hook payload describes a Bash call, so the
// dispatcher can send it to the snapshot/diff pair instead of the edit path.
func IsBashHook(raw []byte) bool {
	var in bashInput
	return json.Unmarshal(raw, &in) == nil && in.ToolName == "Bash"
}

// bashKey names the slot one call's snapshot lives in. A payload with no
// tool_use_id (an older harness) falls back to one shared slot, which is the
// pre-existing behaviour rather than no behaviour.
func bashKey(in bashInput) string {
	if in.ToolUseID != "" {
		return in.ToolUseID
	}
	return "-"
}

// PreBash records what the repo looked like before a Bash command ran. It is
// silent and best-effort: a directory outside any repo, an unreadable index
// or a missing session id all mean "no snapshot", and PostBash then says
// nothing rather than guessing at a diff it cannot compute.
func PreBash(raw []byte) {
	var in bashInput
	if err := json.Unmarshal(raw, &in); err != nil || in.ToolName != "Bash" {
		return
	}
	s, path := loadSession(in.SessionID)
	if s == nil || path == "" {
		return
	}
	snap := takeBashSnapshot(bashSnapshotDir(in.Cwd, in.ToolInput.Command))
	if snap == nil {
		return
	}
	if s.Bash == nil {
		s.Bash = map[string]*bashSnapshot{}
	}
	s.Bash[bashKey(in)] = snap
	pruneBashSnapshots(s.Bash)
	_ = s.save(path)
}

// pruneBashSnapshots drops the oldest entries past the bound.
func pruneBashSnapshots(snaps map[string]*bashSnapshot) {
	for len(snaps) > maxBashSnapshots {
		oldestKey, oldest := "", time.Time{}
		for k, v := range snaps {
			if oldestKey == "" || v.At.Before(oldest) {
				oldestKey, oldest = k, v.At
			}
		}
		delete(snaps, oldestKey)
	}
}

// bashSnapshotDir names the directory whose repo this command is answerable
// for, which is not always the one it was typed in. The harness resets a
// session's shell cwd between calls — to the primary checkout on one box, to
// HOME on another — so a lane session's command arrives as `cd <lane> && …`
// with a cwd it never meant: the directory the command runs in is where the
// command cds to, not the cwd it was handed (issue #733).
//
// A primary checkout is shared: several sessions stand in it at once, and a
// snapshot taken there is diffed against whatever ANY of them did in
// between, so a command whose only writes land in a lane worktree was
// reported as having changed the primary and ran a suite in a tree it never
// touched (issue #593). When the command runs in a merge-only primary, or in
// no repo at all, and every write it can be seen to make lands in one OTHER
// repo, that repo is the one to snapshot. Every other case — an ordinary
// checkout, a write into the primary itself, writes spread over two repos, a
// command whose writes this scanner cannot see — keeps the directory the
// command runs in.
func bashSnapshotDir(cwd, cmd string) string {
	dir := commandRunDir(cwd, cmd)
	primary, ok := PrimaryMergeOnly(dir)
	if !ok && RepoRoot(dir) != "" {
		return dir
	}
	if wt := soleRepoWrittenTo(primary, bashWriteTargets(cmd, cwd)); wt != "" {
		return wt
	}
	return dir
}

// commandRunDir is the directory a command line's commands run in, following
// its `cd` segments from cwd. Every non-cd segment has to land in the same
// repo for that repo to be the answer; a command line spread over two trees,
// or one that cds somewhere this scanner cannot resolve, falls back to cwd.
func commandRunDir(cwd, cmd string) string {
	cur := cwd
	answer, answerRoot := "", ""
	for _, seg := range shellSegments(stripHeredocBodies(cmd)) {
		words := dropLeadingEnvAssignments(seg)
		if target, isCd := cdTarget(words); isCd {
			cur = resolveAgainst(cur, target)
			continue
		}
		if cur == "" {
			return cwd
		}
		root := RepoRoot(existingAncestorDir(cur))
		if answer == "" {
			answer, answerRoot = cur, root
			continue
		}
		if !samePath(root, answerRoot) {
			return cwd
		}
	}
	if answer == "" {
		return cwd
	}
	return answer
}

// soleRepoWrittenTo returns the one repo root, other than primary, that every
// write target lands in. A target inside no repo at all (a scratch file, a
// log outside the tree) cannot move any repo's status and is skipped rather
// than treated as a second repo; a target in the primary, or in a second
// repo, answers "" — the command is not attributable to a single other tree.
func soleRepoWrittenTo(primary string, targets []string) string {
	found := ""
	for _, p := range targets {
		root := repoRootNear(filepath.Dir(p))
		if root == "" {
			continue
		}
		if samePath(root, primary) {
			return ""
		}
		if found != "" && !samePath(found, root) {
			return ""
		}
		found = root
	}
	return found
}

// takeBashSnapshot asks git once what is dirty and stamps the source paths in
// that answer. nil when there is no repo to snapshot.
func takeBashSnapshot(cwd string) *bashSnapshot {
	if cwd == "" {
		return nil
	}
	root := RepoRoot(cwd)
	if root == "" {
		return nil
	}
	status, err := gitRead(root, "status", "--porcelain", "-uall", "-z")
	if err != nil {
		return nil
	}
	sum := sha256.Sum256([]byte(status))
	snap := &bashSnapshot{
		Root:       root,
		StatusHash: hex.EncodeToString(sum[:]),
		Dirty:      map[string]string{},
		MergeHead:  mergeHeadCommit(root),
		At:         time.Now().UTC(),
	}
	for _, rel := range porcelainPaths(status) {
		if ClassifyFile(rel) == Ignore {
			continue
		}
		snap.Dirty[rel] = fileStamp(filepath.Join(root, filepath.FromSlash(rel)))
	}
	return snap
}

// fileStamp is the cheap identity of a file's content: its size and mtime.
// A file that is gone stamps "gone", so a deletion still reads as a change.
func fileStamp(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "gone"
	}
	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
}

// PostBash puts every source file a Bash command changed through the same
// post-edit path an Edit takes, and returns the advisory to surface. It is
// silent when there is no snapshot, no repo, or nothing source-shaped moved.
func PostBash(raw []byte, run SuiteRunner) string {
	var in bashInput
	if err := json.Unmarshal(raw, &in); err != nil || in.ToolName != "Bash" {
		return ""
	}
	s, path := loadSession(in.SessionID)
	if s == nil {
		return ""
	}
	key := bashKey(in)
	before := s.Bash[key]
	if before == nil {
		return ""
	}
	// This call's snapshot is consumed, and only this call's: a batched
	// sibling still has its own, and a stale one must not outlive the command
	// it was taken for.
	delete(s.Bash, key)
	if path != "" {
		_ = s.save(path)
	}

	changed, now := changedSince(before)
	changed, concluded := withoutConcludedMergePaths(before, now, changed)
	if len(changed) == 0 {
		if concluded > 0 {
			appendGateLog("postedit", before.Root, "-", fmt.Sprintf("merge-concluded-standdown:%d", concluded), 0)
			return mergeConcludedLine(before.Root, concluded)
		}
		return ""
	}
	changed, fromTrunk := trunkSyncOwnPaths(before.Root, changed)
	if len(changed) == 0 {
		appendGateLog("postedit", before.Root, "-", fmt.Sprintf("trunk-sync-standdown:%d", fromTrunk), 0)
		return trunkSyncStandDownLine(before.Root, fromTrunk)
	}
	if line := foreignStagedLine(before.Root, changed); line != "" {
		return line
	}

	var notes []string
	seenRoot := map[string]bool{}
	for i, rel := range changed {
		appendGateLog("postedit", before.Root, rel, "bash-edit:"+logToken(rel), 0)
		target := filepath.Join(before.Root, filepath.FromSlash(rel))
		root := FindProjectRoot(target)
		if root == "" || seenRoot[root] {
			continue
		}
		seenRoot[root] = true
		text, deferred := postEditFile(in.SessionID, target, run)
		if text != "" {
			notes = append(notes, text)
		}
		if deferred {
			// One detached build per Bash call: a second project's cold build
			// started here would run unwatched beside the first, and neither
			// result would describe the tree by the time they land. The other
			// roots are not silently dropped, though: they stay unexercised
			// until a later Edit or commit touches them, so say so here —
			// otherwise the only trace was a per-file "bash-edit:" log line
			// nothing reads as "this root never got a run" (issue #306).
			// The count is the fact; the names are a courtesy, and
			// skippedRootsPhrase caps them (issue #583).
			if skipped := otherRootsAmong(changed[i+1:], before.Root, seenRoot); len(skipped) > 0 {
				line := fmt.Sprintf("gate: deferred %s skipped, %d other root(s) changed by this command: %s",
					root, len(skipped), skippedRootsPhrase(skipped))
				appendGateLog("postedit", before.Root, rel, fmt.Sprintf("bash-roots-skipped:%d", len(skipped)), 0)
				notes = append(notes, line)
			}
			break
		}
	}
	return strings.Join(notes, "\n")
}

// foreignStagedLine is the refusal for a harvest that found another session's
// work rather than this command's. A merge-only primary checkout takes merges
// and nothing else, so a source path STAGED in its index belongs to whoever
// is working there, not to the shell call this hook is closing — running a
// suite over it reports a red for code this session never wrote, on a tree
// that is halfway through somebody else's edit (issue #593). "" whenever the
// question does not arise: an ordinary checkout, or a primary whose changed
// paths are all unstaged.
//
// A `git merge --no-ff` INTO that primary is the one command whose own
// staged paths are not foreign at all — a conflicted (or auto-merged) merge
// leaves exactly its own touched files staged, as MERGE_HEAD, and reading
// those as "another session's work" mislabelled the merge's own tree on
// every `git merge --no-ff` this box ran (issue #713, foreign-staged-skipped
// counted 19 hits in one session, every one the merge's own files). When
// MERGE_HEAD (or a cherry-pick/revert in progress) names this as the merge,
// mergeInProgressLine reports that truthfully instead.
func foreignStagedLine(root string, changed []string) string {
	if _, ok := PrimaryMergeOnly(root); !ok {
		return ""
	}
	staged := map[string]bool{}
	for _, rel := range stagedFiles(root) {
		staged[filepath.ToSlash(rel)] = true
	}
	var hits []string
	for _, rel := range changed {
		if staged[filepath.ToSlash(rel)] {
			hits = append(hits, rel)
		}
	}
	if len(hits) == 0 {
		return ""
	}
	if ref := mergeInProgressRef(root); ref != "" {
		appendGateLog("postedit", root, logToken(hits[0]), "merge-in-progress-standdown", 0)
		return mergeInProgressLine(root, hits)
	}
	appendGateLog("postedit", root, logToken(hits[0]), "foreign-staged-skipped", 0)
	return fmt.Sprintf("gate: → skipped in %s (%d changed path(s) are staged in this merge-only primary's index — "+
		"another session's work, not this command's: %s; the code was NOT tested)",
		root, len(hits), strings.Join(hits, ", "))
}

// mergeInProgressLine is what a merge's own staged paths get instead of the
// foreign-session refusal: a true statement of what is actually happening —
// this command IS the merge, and the premerge/commit gate is what judges it,
// not this shell-edit harvest.
func mergeInProgressLine(root string, hits []string) string {
	return fmt.Sprintf("gate: merge in progress (%d path(s) staged in %s) — premerge runs at commit", len(hits), root)
}

// otherRootsAmong finds the distinct project roots among rest (the changed
// paths not yet visited) that are not already in seen, in the order they
// first appear. It is what lets PostBash name every root a Bash command
// touched but never ran a gate for, once the first root's phase deferred and
// stopped the loop.
func otherRootsAmong(rest []string, base string, seen map[string]bool) []string {
	local := make(map[string]bool, len(seen))
	for k := range seen {
		local[k] = true
	}
	var out []string
	for _, rel := range rest {
		target := filepath.Join(base, filepath.FromSlash(rel))
		root := FindProjectRoot(target)
		if root == "" || local[root] {
			continue
		}
		local[root] = true
		out = append(out, root)
	}
	return out
}

// maxNamedSkippedRoots bounds how many unexercised roots the skip line
// spells out. Three is what a reader can act on in one line; past that the
// list stops describing this command and starts describing the size of the
// tree (issue #583: a `git merge --no-ff` printed all 25 roots the merge
// touched).
const maxNamedSkippedRoots = 3

// skippedRootsPhrase renders the roots a deferred phase left unexercised:
// the first few by name, then a count of the rest. The COUNT is never
// dropped — the line's whole job is to say how much of this command never
// got a gate, and a truncation that hid that would be suppressing the fact
// instead of shortening the sentence. The caller prints the exact total
// alongside this phrase, so the two halves cannot disagree; `aphrollo gate
// status` and gate.log's own bash-roots-skipped entry hold the full list.
func skippedRootsPhrase(roots []string) string {
	if len(roots) <= maxNamedSkippedRoots {
		return strings.Join(roots, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(roots[:maxNamedSkippedRoots], ", "), len(roots)-maxNamedSkippedRoots)
}

// changedSince names every source path whose dirty stamp moved, entered the
// dirty set, or left it. An identical status hash AND identical stamps is the
// fast no-op: the command read something and wrote nothing.
func changedSince(before *bashSnapshot) ([]string, *bashSnapshot) {
	now := takeBashSnapshot(before.Root)
	if now == nil {
		return nil, nil
	}
	changed := map[string]bool{}
	for rel, stamp := range before.Dirty {
		if now.Dirty[rel] != stamp {
			changed[rel] = true
		}
	}
	for rel, stamp := range now.Dirty {
		if before.Dirty[rel] != stamp {
			changed[rel] = true
		}
	}
	out := make([]string, 0, len(changed))
	for rel := range changed {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out, now
}

// porcelainPaths reads the paths out of `git status --porcelain -z` records.
// `-z` is what makes a non-ASCII path readable at all: without it git quotes
// and escapes such a path, and the quoted spelling matches nothing on disk. A
// rename record carries the destination first and the source in its own
// following record, and both are paths this cares about.
func porcelainPaths(status string) []string {
	var out []string
	fields := strings.Split(status, "\x00")
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if len(rec) < 4 {
			continue
		}
		xy, rel := rec[:2], rec[3:]
		out = append(out, rel)
		// A rename/copy record is followed by a NUL-terminated field holding
		// the ORIGIN path, which is not a status record of its own.
		if strings.ContainsAny(xy, "RC") && i+1 < len(fields) {
			i++
			if origin := fields[i]; origin != "" {
				out = append(out, origin)
			}
		}
	}
	return out
}
