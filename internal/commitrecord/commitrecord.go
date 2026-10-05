// Package commitrecord keeps a record of every commit made through the real
// commit path: the post-commit hook the gate installs appends the new HEAD's
// sha to a per-repository file in the gate state dir. The record is only as
// good as the wall around it: a test process must run a git that fires no real
// hook (gitiso.Isolate and gitenv.Sealed point core.hooksPath at an empty dir,
// which outranks the repository's own .git/hooks) and keeps the gate state dir
// (CLAUDE_CONFIG_DIR) out of the operator's, so a commit it leaks has no record
// in the operator's file whatever identity it commits under. A process that
// reaches the real hook and the real state dir anyway gets its commits
// recorded, and the canary then takes them for the owner's. The mutation canary
// reads the record to tell the lane owner's commits from a leak.
package commitrecord

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	childrun "github.com/aphrollo/aphrollo-tools/internal/run"
	core "github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// fileFor is the record file of the repository dir belongs to, keyed by the
// shared git dir so every worktree of one repository reads and writes one file.
// head is the sha of dir's HEAD, "" when it has none.
func fileFor(dir string) (path, head string) {
	out, err := childrun.LightOutput(childrun.Spec{Name: "git", Args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir", "HEAD"}, Dir: dir})
	if err != nil { // any failure means no record, and the hook fails open
		return "", ""
	}
	lines := strings.Fields(string(out))
	if len(lines) != 2 {
		return "", ""
	}
	state := core.StateDir()
	if state == "" {
		return "", ""
	}
	sum := sha1.Sum([]byte(filepath.ToSlash(lines[0])))
	return filepath.Join(state, "commits", hex.EncodeToString(sum[:8])+".log"), lines[1]
}

// Record appends dir's HEAD to the repository's record. Best-effort: the commit
// is already made, so any failure is dropped.
func Record(dir string) {
	path, head := fileFor(dir)
	if path == "" || head == "" {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(head + "\n")
	_ = f.Close()
}

// Recorded is the set of commit shas recorded for the repository dir belongs to.
func Recorded(dir string) map[string]bool {
	set := map[string]bool{}
	path, _ := fileFor(dir)
	if path == "" {
		return set
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return set
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			set[line] = true
		}
	}
	return set
}

// RecordSHAs appends each of shas, as given, to dir's repository's record: the
// new commits a rebase or an amend wrote, which fire post-rewrite and neither
// post-commit nor post-merge. Best-effort, like Record.
func RecordSHAs(dir string, shas []string) {
	path, _ := fileFor(dir)
	if path == "" || len(shas) == 0 {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(strings.Join(shas, "\n") + "\n")
	_ = f.Close()
}
