package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitLaneNoKey is a git repository with a Go module that declares nothing
// about the commit-time run.
func gitLaneNoKey(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestGateMutantsCommit_OutsideARepositorySaysWhy(t *testing.T) {
	gateConfigDir(t)
	inDir(t, t.TempDir())
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "commit"}, strings.NewReader(""), &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "git repository") {
		t.Errorf("exit %d stderr %q, want 1 and the reason", code, errb.String())
	}
}

func TestGateMutantsCommit_ARepoThatDeclaresNothingSaysSo(t *testing.T) {
	gateConfigDir(t)
	inDir(t, gitLaneNoKey(t))
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "commit"}, strings.NewReader(""), &out, &errb)
	if code != 0 || !strings.Contains(errb.String(), "mutants-at-commit") {
		t.Errorf("exit %d stderr %q, want 0 and the missing key named", code, errb.String())
	}
}

// Outside a repository the verbs do nothing even where a config that declares
// the key happens to lie in the directory: what makes them inert is that
// there is no repository, not that nothing declared the key.
func TestGateMutantsVerbs_OutsideARepositoryIgnoreAStrayDeclaration(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	gateConfigDir(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aphrollo.toml"), []byte("[aphrollo]\nmutants-at-commit = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inDir(t, dir)
	var out, errb bytes.Buffer
	done := filepath.Join(t.TempDir(), "done")
	if code := Run([]string{"gate", "mutants", "edit", "--file", "x.go", "--done", done}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Errorf("edit: exit %d, want 0: %s", code, errb.String())
	}
	if data, err := os.ReadFile(done); err != nil || strings.TrimSpace(string(data)) != "ok" {
		t.Errorf("edit result = %q (%v), want ok", data, err)
	}
	if log, err := gateLogBytes(t); err == nil && strings.Contains(string(log), "mutants") {
		t.Errorf("a run happened outside a repository:\n%s", log)
	}
}

// `edit` is what the edit hook starts detached: it needs the file and where to
// record the result, and records "ok" where there is nothing to measure.
func TestGateMutantsEdit_NeedsItsFlagsAndRecordsWhereItIsPointed(t *testing.T) {
	gateConfigDir(t)
	root := gitLaneNoKey(t)
	inDir(t, root)
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "mutants", "edit"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Errorf("no flags: exit %d, want 2", code)
	}
	done := filepath.Join(t.TempDir(), "done")
	if code := Run([]string{"gate", "mutants", "edit", "--file", "x.go"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Errorf("no --done: exit %d, want 2", code)
	}
	if code := Run([]string{"gate", "mutants", "edit", "--done", done}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Errorf("no --file: exit %d, want 2", code)
	}
	errb.Reset()
	if code := Run([]string{"gate", "mutants", "edit", "--file", "x.go", "--done", done}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("in a repo declaring nothing: exit %d, want 0: %s", code, errb.String())
	}
	if data, err := os.ReadFile(done); err != nil || strings.TrimSpace(string(data)) != "ok" {
		t.Errorf("result = %q (%v), want ok", data, err)
	}
}

func TestGateMutantsEdit_OutsideARepositoryRecordsOK(t *testing.T) {
	gateConfigDir(t)
	inDir(t, t.TempDir())
	done := filepath.Join(t.TempDir(), "done")
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "mutants", "edit", "--file", "x.go", "--done", done}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, errb.String())
	}
	if data, err := os.ReadFile(done); err != nil || strings.TrimSpace(string(data)) != "ok" {
		t.Errorf("result = %q (%v), want ok so the hook that started it is not left waiting", data, err)
	}
}

func TestGateMutants_HelpListsTheCommitTimeVerbs(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "mutants", "--help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"commit ", "edit --file"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("help never lists %q:\n%s", want, errb.String())
		}
	}
}

// No verb builds a test map by hand or for a hook: the commit stage measures
// the coverage it needs itself. A caller still running the old verb is told it
// is unknown, not answered with silence.
func TestGateMutants_NoTestmapVerbExists(t *testing.T) {
	gateConfigDir(t)
	inDir(t, gitLaneNoKey(t))
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "testmap"}, strings.NewReader(""), &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), `unknown verb "testmap"`) {
		t.Errorf("exit %d stderr %q, want 2 and the verb named unknown", code, errb.String())
	}
	errb.Reset()
	if code := Run([]string{"gate", "mutants", "--help"}, strings.NewReader(""), &out, &errb); code != 0 || strings.Contains(errb.String(), "testmap") {
		t.Errorf("help (exit %d) still lists testmap:\n%s", code, errb.String())
	}
}

// ratchet: test_removed TestGateMutantsTestmap_IsSilentWhereItHasNothingToDo: the testmap verb is gone
// ratchet: test_removed TestGateMutantsTestmap_RefusesAFlagItDoesNotHave: the testmap verb is gone
