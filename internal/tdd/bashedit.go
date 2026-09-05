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

// bashInput is the slice of a Bash hook payload these two need.
type bashInput struct {
	SessionID string `json:"session_id"`
	ToolUseID string `json:"tool_use_id"`
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
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
	At    time.Time         `json:"at"`
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
	snap := takeBashSnapshot(in.Cwd)
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

	changed := changedSince(before)
	if len(changed) == 0 {
		return ""
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
			if skipped := otherRootsAmong(changed[i+1:], before.Root, seenRoot); len(skipped) > 0 {
				line := fmt.Sprintf("gate: deferred %s skipped, %d other root(s) changed by this command: %s",
					root, len(skipped), strings.Join(skipped, ", "))
				appendGateLog("postedit", before.Root, rel, fmt.Sprintf("bash-roots-skipped:%d", len(skipped)), 0)
				notes = append(notes, line)
			}
			break
		}
	}
	return strings.Join(notes, "\n")
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

// changedSince names every source path whose dirty stamp moved, entered the
// dirty set, or left it. An identical status hash AND identical stamps is the
// fast no-op: the command read something and wrote nothing.
func changedSince(before *bashSnapshot) []string {
	now := takeBashSnapshot(before.Root)
	if now == nil {
		return nil
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
	return out
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
