package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `aphrollo gate probe discard` is the sanctioned route back to HEAD for a
// refused probe arm (#836). Every test runs it against a REAL repo: the
// command's whole promise is what git and the filesystem look like after it.

// probeFixture is a one-commit repo holding a.txt (one line) beside the
// seed, with the gate's state dir isolated and the test chdir'd into it.
func probeFixture(t *testing.T) (repo, realGit, cfgDir string) {
	t.Helper()
	cfgDir = gateConfigDir(t)
	withDirectGitShim(t)
	repo, realGit = newDiscardFixture(t)
	writeFixtureFile(t, repo, "a.txt", []string{"a-orig-0"})
	runFixtureGit(t, realGit, repo, "add", ".")
	runFixtureGit(t, realGit, repo, "commit", "-qm", "base")
	t.Chdir(repo)
	return repo, realGit, cfgDir
}

func readFixture(t *testing.T, repo, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// backupPathFrom reads the path the command printed on its `backup:` line.
func backupPathFrom(t *testing.T, stdout string) string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		if rest, ok := strings.CutPrefix(line, "backup: "); ok {
			path, _, _ := strings.Cut(rest, " (")
			return path
		}
	}
	t.Fatalf("no backup: line in stdout:\n%s", stdout)
	return ""
}

func backupEntries(t *testing.T, cfgDir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(cfgDir, "gate-state", "probe-discard"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return entries
}

func TestProbeDiscard_RefusesAFileWithStagedContentAndTouchesNothing(t *testing.T) {
	repo, realGit, cfgDir := probeFixture(t)
	writeFixtureFile(t, repo, "a.txt", distinctLines("staged", 2))
	runFixtureGit(t, realGit, repo, "add", "a.txt")
	writeFixtureFile(t, repo, "seed.txt", distinctLines("arm", 2))

	var out, errb bytes.Buffer
	if code := probeDiscard(realGit, repo, []string{"--apply", "a.txt", "seed.txt"}, &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "a.txt") || !strings.Contains(errb.String(), "staged") {
		t.Fatalf("stderr = %q, want it to name a.txt and its staged content", errb.String())
	}
	if got := readFixture(t, repo, "seed.txt"); got != "arm-0\narm-1\n" {
		t.Fatalf("seed.txt = %q, want the arm left in place — a refusal touches nothing", got)
	}
	if n := len(backupEntries(t, cfgDir)); n != 0 {
		t.Fatalf("%d backup(s) written, want 0 on a refusal", n)
	}
}

// The backup is the full `git diff HEAD` of the arm, written before the file
// goes back to HEAD, and it is a patch: `git apply` of it brings the arm back.
func TestProbeDiscard_BacksUpTheExactDiffThenRestoresHEAD(t *testing.T) {
	repo, realGit, cfgDir := probeFixture(t)
	arm := distinctLines("arm", 3)
	writeFixtureFile(t, repo, "a.txt", arm)
	wantDiff := runFixtureGit(t, realGit, repo, "diff", "--binary", "HEAD", "--", "a.txt")

	var out, errb bytes.Buffer
	if code := probeDiscard(realGit, repo, []string{"--apply", "a.txt"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if got := readFixture(t, repo, "a.txt"); got != "a-orig-0\n" {
		t.Fatalf("a.txt = %q, want HEAD's content", got)
	}
	backup := backupPathFrom(t, out.String())
	if filepath.Dir(backup) != filepath.Join(cfgDir, "gate-state", "probe-discard") {
		t.Fatalf("backup %s is not under the gate state dir", backup)
	}
	body, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("backup not written: %v", err)
	}
	if !strings.Contains(string(body), wantDiff) {
		t.Fatalf("backup does not carry the exact diff\nwant:\n%s\ngot:\n%s", wantDiff, body)
	}
	runFixtureGit(t, realGit, repo, "apply", backup)
	if got := readFixture(t, repo, "a.txt"); got != strings.Join(arm, "\n")+"\n" {
		t.Fatalf("a.txt after git apply of the backup = %q, want the arm back", got)
	}
}

func TestProbeDiscard_BacksUpAnUntrackedFileThenRemovesIt(t *testing.T) {
	repo, realGit, _ := probeFixture(t)
	writeFixtureFile(t, repo, "new.txt", []string{"hello"})

	var out, errb bytes.Buffer
	if code := probeDiscard(realGit, repo, []string{"--apply", "new.txt"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new.txt still present (err=%v), want it removed", err)
	}
	backup := backupPathFrom(t, out.String())
	runFixtureGit(t, realGit, repo, "apply", backup)
	if got := readFixture(t, repo, "new.txt"); got != "hello\n" {
		t.Fatalf("new.txt after git apply of the backup = %q, want its full content back", got)
	}
}

func TestProbeDiscard_BackupFailureDiscardsNothing(t *testing.T) {
	repo, realGit, cfgDir := probeFixture(t)
	writeFixtureFile(t, repo, "a.txt", distinctLines("arm", 3))
	blocker := filepath.Join(cfgDir, "gate-state", "probe-discard")
	if err := os.MkdirAll(filepath.Dir(blocker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := probeDiscard(realGit, repo, []string{"--apply", "a.txt"}, &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1 when the backup cannot be written\nstdout: %s", code, out.String())
	}
	if got := readFixture(t, repo, "a.txt"); got != "arm-0\narm-1\narm-2\n" {
		t.Fatalf("a.txt = %q, want the arm untouched: no backup, no discard", got)
	}
}

// The dry run is the default: it prints the plan, what each file would lose
// and the backup path it would use, and changes nothing anywhere.
func TestProbeDiscard_DryRunPrintsThePlanAndChangesNothing(t *testing.T) {
	repo, realGit, cfgDir := probeFixture(t)
	writeFixtureFile(t, repo, "a.txt", distinctLines("arm", 3))
	writeFixtureFile(t, repo, "new.txt", []string{"hello"})

	var out, errb bytes.Buffer
	if code := probeDiscard(realGit, repo, []string{"a.txt", "new.txt", "seed.txt"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"a.txt  +3/-1 lines vs HEAD", "new.txt  untracked, 6 bytes", "seed.txt  no change vs HEAD", "--apply"} {
		if !strings.Contains(got, want) {
			t.Errorf("dry run output lacks %q:\n%s", want, got)
		}
	}
	if backupPathFrom(t, got) == "" {
		t.Fatal("dry run must name the backup path it would use")
	}
	if got := readFixture(t, repo, "a.txt"); got != "arm-0\narm-1\narm-2\n" {
		t.Fatalf("a.txt = %q, want it untouched by a dry run", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); err != nil {
		t.Fatalf("new.txt gone after a dry run: %v", err)
	}
	if n := len(backupEntries(t, cfgDir)); n != 0 {
		t.Fatalf("%d backup(s) written by a dry run, want 0", n)
	}
	if log := readGateLog(t, cfgDir); strings.Contains(log, "probe-discard") {
		t.Fatalf("a dry run logged a discard:\n%s", log)
	}
}

func TestProbeDiscard_ApplyLogsPathsSummaryAndBackup(t *testing.T) {
	repo, realGit, cfgDir := probeFixture(t)
	writeFixtureFile(t, repo, "a.txt", distinctLines("arm", 3))
	writeFixtureFile(t, repo, "new.txt", []string{"hello"})

	var out, errb bytes.Buffer
	if code := probeDiscard(realGit, repo, []string{"--apply", "a.txt", "new.txt"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "a.txt  +3/-1 lines vs HEAD") || !strings.Contains(out.String(), "new.txt  untracked, 6 bytes") {
		t.Fatalf("--apply must print what each file lost, got:\n%s", out.String())
	}
	log := readGateLog(t, cfgDir)
	for _, want := range []string{"probe-discard", "a.txt:+3/-1", "new.txt:untracked:6B", backupPathFrom(t, out.String())} {
		if !strings.Contains(log, want) {
			t.Errorf("gate.log lacks %q:\n%s", want, log)
		}
	}
}

func TestProbeDiscard_RefusesPathsItCannotNameExactly(t *testing.T) {
	repo, realGit, cfgDir := probeFixture(t)
	writeFixtureFile(t, repo, "a.txt", distinctLines("arm", 3))
	if err := os.Mkdir(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "x.txt")
	for _, bad := range []string{outside, "sub", "*.txt", "a?.txt"} {
		var out, errb bytes.Buffer
		if code := probeDiscard(realGit, repo, []string{"--apply", "a.txt", bad}, &out, &errb); code != 1 {
			t.Errorf("%s: exit = %d, want 1\nstdout: %s", bad, code, out.String())
		}
		if !strings.Contains(errb.String(), bad) {
			t.Errorf("%s: stderr = %q, want it to name the refused path", bad, errb.String())
		}
	}
	if got := readFixture(t, repo, "a.txt"); got != "arm-0\narm-1\narm-2\n" {
		t.Fatalf("a.txt = %q, want it untouched: one refused path refuses the whole call", got)
	}
	if n := len(backupEntries(t, cfgDir)); n != 0 {
		t.Fatalf("%d backup(s) written, want 0", n)
	}
}

// Nothing to discard is a report, not an error, and writes no backup.
func TestProbeDiscard_UnchangedPathIsReportedNotRefused(t *testing.T) {
	repo, realGit, cfgDir := probeFixture(t)

	var out, errb bytes.Buffer
	if code := probeDiscard(realGit, repo, []string{"--apply", "a.txt"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "a.txt  no change vs HEAD") {
		t.Fatalf("stdout = %q, want a.txt reported as unchanged", out.String())
	}
	if n := len(backupEntries(t, cfgDir)); n != 0 {
		t.Fatalf("%d backup(s) written with nothing to discard, want 0", n)
	}
}

func TestRun_GateProbeDiscardDispatches(t *testing.T) {
	repo, realGit, _ := probeFixture(t)
	t.Setenv("APHROLLO_REAL_GIT", realGit)
	writeFixtureFile(t, repo, "a.txt", distinctLines("arm", 3))

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "probe", "discard", "--apply", "a.txt"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if got := readFixture(t, repo, "a.txt"); got != "a-orig-0\n" {
		t.Fatalf("a.txt = %q, want HEAD's content", got)
	}
}

// A backup exists so a discard can always be undone: gc, dry run or
// --apply, never deletes one, and its report lists each so the operator can.
func TestGateGC_ListsDiscardBackupsAndNeverSweepsThem(t *testing.T) {
	repo, realGit, _ := probeFixture(t)
	writeFixtureFile(t, repo, "a.txt", distinctLines("arm", 3))
	var out, errb bytes.Buffer
	if code := probeDiscard(realGit, repo, []string{"--apply", "a.txt"}, &out, &errb); code != 0 {
		t.Fatalf("discard exit = %d\nstderr: %s", code, errb.String())
	}
	backup := backupPathFrom(t, out.String())

	for _, args := range [][]string{{"--repo", repo}, {"--repo", repo, "--apply"}} {
		var gcOut, gcErr bytes.Buffer
		if code := runGateGC(args, &gcOut, &gcErr); code != 0 {
			t.Fatalf("gc %v exit = %d\nstderr: %s", args, code, gcErr.String())
		}
		if _, err := os.Stat(backup); err != nil {
			t.Fatalf("gc %v removed the discard backup: %v", args, err)
		}
		var listed string
		for _, line := range strings.Split(gcOut.String(), "\n") {
			if strings.Contains(line, backup) {
				listed = line
			}
		}
		if listed == "" || !strings.Contains(listed, "a.txt") {
			t.Fatalf("gc %v must list the backup with the files it covers, got:\n%s", args, gcOut.String())
		}
	}
}
