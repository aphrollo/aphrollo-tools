// Package commitrecord keeps a record of every commit made through the real
// commit path: the post-commit hook the gate installs appends the new HEAD's
// sha to a per-repository file in the gate state dir. A test process runs under
// the isolation harness with the hooks neutralised, so a commit it leaks never
// reaches the hook and has no record, whatever identity it commits under. The
// mutation canary reads the record to tell the lane owner's commits from a leak.
package commitrecord

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	core "github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// fileFor is the record file of the repository dir belongs to, keyed by the
// shared git dir so every worktree of one repository reads and writes one file.
// head is the sha of dir's HEAD, "" when it has none.
func fileFor(dir string) (path, head string) {
	cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output() // stderr-ok: any failure means no record, and the hook fails open
	if err != nil {
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
