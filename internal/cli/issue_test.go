package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// The gh stub is a compiled binary, not a script: Windows cannot exec a .bat
// through CreateProcess, so a script stub would never run and every assertion
// about gh would pass vacuously.
const ghStubSource = `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	if log := os.Getenv("GH_STUB_LOG"); log != "" {
		if f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
			f.Close()
		}
	}
	if out := os.Getenv("GH_STUB_OUT"); out != "" {
		fmt.Println(out)
	}
}
`

var ghStubDir = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "aphrollo-cli-gh-stub")
	if err != nil {
		return "", err
	}
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(ghStubSource), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module ghstub\n\ngo 1.26\n"), 0o644); err != nil {
		return "", err
	}
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.exe"
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, name), ".")
	cmd.Dir = src
	if _, err := cmd.CombinedOutput(); err != nil {
		return "", err
	}
	return dir, nil
})

// stubIssueRepo is a committed repo with a GitHub origin and a fake gh on
// PATH, plus the argv log gh writes.
func stubIssueRepo(t *testing.T, stdout string) (repo, argvLog string) {
	t.Helper()
	dir, err := ghStubDir()
	if err != nil {
		t.Fatal(err)
	}
	gateConfigDir(t)
	isolateGit(t)
	repo = t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"remote", "add", "origin", "https://github.com/o/r.git"},
	} {
		cmd := fixtureGit(args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	argvLog = filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("GH_STUB_LOG", argvLog)
	t.Setenv("GH_STUB_OUT", stdout)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return repo, argvLog
}

// The command exists to be piped: whatever else it says goes to stderr, so
// stdout is the URL and nothing else.
func TestGateIssuePrintsTheURLAsItsOnlyStdoutLine(t *testing.T) {
	repo, _ := stubIssueRepo(t, "https://github.com/o/r/issues/12")
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "issue", "the rig drifts at 60 Hz", "--label", "physics", "--repo", repo},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if got := out.String(); got != "https://github.com/o/r/issues/12\n" {
		t.Fatalf("stdout = %q, want the URL alone", got)
	}
}

// A typo'd label opens a theme nobody filters on. The refusal has to carry
// the list, or the author is guessing at the spelling.
func TestGateIssueRefusesAnUndeclaredLabelAndNamesTheList(t *testing.T) {
	repo, log := stubIssueRepo(t, "https://github.com/o/r/issues/12")
	if err := os.WriteFile(filepath.Join(repo, "aphrollo.toml"),
		[]byte("[aphrollo]\nissue-labels = [\"netcode\", \"physics\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "issue", "t", "--label", "phsyics", "--repo", repo},
		strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatal("an undeclared label must be refused")
	}
	for _, want := range []string{"phsyics", "netcode", "physics", "--new-label"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the refusal must name %q; stderr was %q", want, errb.String())
		}
	}
	if data, _ := os.ReadFile(log); strings.Contains(string(data), "issue create") {
		t.Errorf("a refused label must never reach gh:\n%s", data)
	}
	if out.Len() != 0 {
		t.Errorf("a refusal prints no URL, got %q", out.String())
	}
}

// A title is the one thing an issue cannot be opened without.
func TestGateIssueRefusesAnEmptyTitle(t *testing.T) {
	repo, _ := stubIssueRepo(t, "https://github.com/o/r/issues/12")
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "issue", "--repo", repo}, strings.NewReader(""), &out, &errb); code == 0 {
		t.Fatal("an issue with no title must be refused")
	}
}

// `gate issue --label physics "the title"` is how a hand types it as often as
// title-first. Discarding the trailing positionals left the command refusing
// a title that was right there on the line.
func TestGateIssueAcceptsATitleAfterItsFlags(t *testing.T) {
	repo, log := stubIssueRepo(t, "https://github.com/o/r/issues/21")
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "issue", "--repo", repo, "--label", "physics", "the rig drifts"},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(argv), "--title the rig drifts") {
		t.Errorf("the trailing title must reach gh:\n%s", argv)
	}
}
