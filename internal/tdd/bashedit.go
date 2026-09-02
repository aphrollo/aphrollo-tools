package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A `sed -i`, a heredoc, a `gofmt -w`, a generator — every one of them is an
// edit, and none of them fires the Edit hooks. So the gate stopped at the
// shell: the same change that is denied through Edit lands silently through
// Bash, and the suite never runs. That is not a hole a rule can close; it
// needs the hooks to see the shell too.
//
// PreToolUse takes a cheap snapshot of the repo the command runs in — the
// porcelain status plus the size and mtime of every tracked SOURCE file —
// and PostToolUse diffs it. Every changed source or test file goes through
// the identical post-edit path an Edit would take.
//
// Two things it deliberately does NOT do. It cannot deny a smell before the
// write: the content does not exist until the command has run, so the
// pre-edit half is a snapshot and nothing else — the commit gate is where a
// smell introduced through the shell is caught. And it runs a project's
// suite ONCE however many files the command rewrote: the suite answers for
// all of them at once, so a per-file loop would charge the same build twenty
// times and leave twenty detached ones behind it.

// bashInput is the slice of a Bash hook payload these two need.
type bashInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
}

// bashSnapshot is the tree as it stood before a shell command ran.
type bashSnapshot struct {
	Root   string `json:"root"`
	Status string `json:"status"`
	// Files stamps each tracked source file "<size>:<mtime-unix-nanos>".
	// bound: one entry per tracked source file in the repo the command runs in.
	Files map[string]string `json:"files"`
}

// IsBashHook reports whether a hook payload describes a Bash call, so the
// dispatcher can send it to the snapshot/diff pair instead of the edit path.
func IsBashHook(raw []byte) bool {
	var in bashInput
	return json.Unmarshal(raw, &in) == nil && in.ToolName == "Bash"
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
	s.Bash = takeBashSnapshot(in.Cwd)
	_ = s.save(path)
}

// takeBashSnapshot stats every tracked source file under cwd's repo. nil when
// there is no repo to snapshot.
func takeBashSnapshot(cwd string) *bashSnapshot {
	if cwd == "" {
		return nil
	}
	root := RepoRoot(cwd)
	if root == "" {
		return nil
	}
	snap := &bashSnapshot{Root: root, Files: map[string]string{}}
	snap.Status, _ = git(root, "status", "--porcelain")
	tracked, err := gitRead(root, "ls-files")
	if err != nil {
		return snap
	}
	for line := range strings.SplitSeq(strings.ReplaceAll(tracked, "\r\n", "\n"), "\n") {
		rel := strings.TrimSpace(line)
		if rel == "" || ClassifyFile(rel) == Ignore {
			continue
		}
		snap.Files[rel] = fileStamp(filepath.Join(root, filepath.FromSlash(rel)))
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
	if s == nil || s.Bash == nil {
		return ""
	}
	before := s.Bash
	// The snapshot is consumed: two PostToolUse calls for one command must not
	// each report the same change, and a stale snapshot must not outlive the
	// call it was taken for.
	s.Bash = nil
	if path != "" {
		_ = s.save(path)
	}

	changed := changedSince(before)
	if len(changed) == 0 {
		return ""
	}

	var notes []string
	seenRoot := map[string]bool{}
	for _, rel := range changed {
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
			// result would describe the tree by the time they land.
			break
		}
	}
	return strings.Join(notes, "\n")
}

// changedSince names every tracked SOURCE file whose stamp moved, plus every
// source file that appeared (a new file is not in the snapshot at all, and
// `git status --porcelain` is what sees it). Sorted, so a multi-file command
// reports in a stable order.
func changedSince(before *bashSnapshot) []string {
	now := takeBashSnapshot(before.Root)
	if now == nil {
		return nil
	}
	changed := map[string]bool{}
	for rel, stamp := range before.Files {
		if now.Files[rel] != stamp {
			changed[rel] = true
		}
	}
	for rel := range now.Files {
		if _, known := before.Files[rel]; !known {
			changed[rel] = true
		}
	}
	if now.Status != before.Status {
		for _, rel := range porcelainPaths(now.Status) {
			if ClassifyFile(rel) != Ignore && before.Files[rel] != fileStamp(filepath.Join(before.Root, filepath.FromSlash(rel))) {
				changed[rel] = true
			}
		}
	}
	out := make([]string, 0, len(changed))
	for rel := range changed {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// porcelainPaths reads the paths out of `git status --porcelain` lines. A
// rename line (`R  old -> new`) yields the destination, which is the file
// that now holds the content.
func porcelainPaths(status string) []string {
	var out []string
	for line := range strings.SplitSeq(strings.ReplaceAll(status, "\r\n", "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		rel := strings.TrimSpace(line[3:])
		if _, dst, ok := strings.Cut(rel, " -> "); ok {
			rel = dst
		}
		out = append(out, strings.Trim(rel, `"`))
	}
	return out
}
