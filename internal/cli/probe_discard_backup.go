package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// probeBackupDirName is the gate-state subdirectory discard backups live in.
// The state dir is outside every checkout, so a backup survives the discard,
// the lane's prune and a `git clean`, and can never be staged or committed.
// No gc category reaches into it: backups are listed, never swept.
const probeBackupDirName = "probe-discard"

// probeBackupFileHeader opens every backup; the gc listing reads the
// `# file:` lines under it. git apply skips everything before the first
// `diff --git`, so the file stays a patch the operator can apply as is.
const probeBackupFileHeader = "# aphrollo gate probe discard backup"

func probeBackupDir() (string, error) {
	state := tdd.StateDir()
	if state == "" {
		return "", fmt.Errorf("no gate state dir (CLAUDE_CONFIG_DIR unset and no resolvable home) to hold the backup")
	}
	return filepath.Join(state, probeBackupDirName), nil
}

// probeBackupPath names the backup for a discard in root at now: a UTC
// timestamp to the nanosecond, then the repo's directory name, so a listing
// sorts by time and still says which checkout each came from.
func probeBackupPath(root string, now time.Time) (string, error) {
	dir, err := probeBackupDir()
	if err != nil {
		return "", err
	}
	stamp := now.UTC().Format("20060102T150405.000000000Z")
	return filepath.Join(dir, stamp+"-"+filepath.Base(root)+".patch"), nil
}

// probeWriteBackup writes the full discarded diff to path and syncs it
// before returning: a tracked file's `git diff --binary HEAD`, and an
// untracked file's whole content as a new-file patch. It refuses to
// overwrite an existing file.
func probeWriteBackup(realGit, root, path string, doomed []probeFile) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n# repo: %s\n# created: %s\n# undo: git -C %s apply %s\n",
		probeBackupFileHeader, root, time.Now().UTC().Format(time.RFC3339), root, path)
	var tracked []string
	for _, f := range doomed {
		fmt.Fprintf(&b, "# file: %s\n", f.rel)
		if f.changed {
			tracked = append(tracked, f.rel)
		}
	}
	if len(tracked) > 0 {
		args := append([]string{"diff", "--binary", "--no-color", "--no-ext-diff", "--no-textconv", "HEAD", "--"}, tracked...)
		diff, err := probeGit(realGit, root, args...)
		if err != nil {
			return err
		}
		b.WriteString(diff)
	}
	for _, f := range doomed {
		if !f.untracked {
			continue
		}
		diff, err := untrackedPatch(realGit, root, f.rel)
		if err != nil {
			return err
		}
		b.WriteString(diff)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(file, b.String()); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// untrackedPatch renders an untracked file as a new-file patch against
// /dev/null, which git's --no-index mode reads as "no file" on every host.
// Exit status 1 is git's "the two differ", the expected answer here.
func untrackedPatch(realGit, root, rel string) (string, error) {
	out, err := runGitCapture(realGit, root, "diff", "--no-index", "--binary", "--no-color", "--no-ext-diff", "--no-textconv",
		"--", "/dev/null", rel)
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return out, nil
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("git diff --no-index found no content in untracked %s", rel)
}

// probeBackupListing is gc's report of the discard backups: path, age, size
// and the files each covers, oldest first. gc never deletes one; the list is
// there so the operator can remove them by hand.
func probeBackupListing(now time.Time) []string {
	dir, err := probeBackupDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var lines []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		lines = append(lines, fmt.Sprintf("  %s  %s old  %d bytes  %s",
			path, now.Sub(info.ModTime()).Truncate(time.Second), info.Size(), strings.Join(probeBackupFiles(path), ", ")))
	}
	if len(lines) == 0 {
		return nil
	}
	return append([]string{"probe discard backups (never swept; remove by hand once the arm is gone for good):"}, lines...)
}

// probeBackupFiles reads the `# file:` header lines of one backup.
func probeBackupFiles(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var files []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "#") {
			break
		}
		if rel, ok := strings.CutPrefix(line, "# file: "); ok {
			files = append(files, rel)
		}
	}
	return files
}
