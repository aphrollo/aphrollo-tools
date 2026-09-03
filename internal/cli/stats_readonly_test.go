package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The stub is a compiled binary, not a shell script: Windows cannot exec a
// .bat through CreateProcess, so a script stub would never run and every
// assertion about gh would pass vacuously.
const cliGhStubSource = `package main

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

var cliGhStubDir = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "aphrollo-cli-gh-stub")
	if err != nil {
		return "", err
	}
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(cliGhStubSource), 0o644); err != nil {
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
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the gh stub: %v\n%s", err, out)
	}
	return dir, nil
})

// stubGhForCLI puts the fake gh first on PATH and returns the file every call
// it receives is logged to.
func stubGhForCLI(t *testing.T, stdout string) string {
	t.Helper()
	dir, err := cliGhStubDir()
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("GH_STUB_LOG", log)
	t.Setenv("GH_STUB_OUT", stdout)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// gitHubRepoCwd makes the working directory a committed repo whose origin is
// on GitHub, so the escape loop believes there is somewhere to open an issue.
// Both tests below depend on that being TRUE: without it the loop stops before
// it reaches gh, and the assertion that gh was never called passes vacuously
// on any box whose checkout has no GitHub remote.
func gitHubRepoCwd(t *testing.T) {
	t.Helper()
	dir := gitInit(t, map[string]string{"a.txt": "x\n"})
	cmd := exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/o/r.git")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	t.Chdir(dir)
}

func ghCalls(t *testing.T, log string) string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		return ""
	}
	return string(data)
}

// risingDenials writes a gate.log whose refusals of one check rise in each of
// the last three weeks — the shape DemoteCandidates looks for.
func risingDenials(t *testing.T, cfg string) {
	t.Helper()
	dir := filepath.Join(cfg, "gate-state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var b strings.Builder
	for week, n := range map[int]int{2: 1, 1: 4, 0: 9} {
		for range n {
			at := now.Add(-time.Duration(week)*7*24*time.Hour - time.Hour)
			fmt.Fprintf(&b, "%s precommit D:/repo cargo test -p server pretooluse-denied:test-sleep 5s\n",
				at.Format(time.RFC3339))
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "gate.log"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// `gate stats` is a REPORT. Opening GitHub issues from it means running the
// weekly health command twice opens the same debt twice, and that anyone who
// reads the pipeline's numbers has silently written to the tracker.
func TestGateStatsOpensNoIssues(t *testing.T) {
	cfg := gateConfigDir(t)
	risingDenials(t, cfg)
	gitHubRepoCwd(t)
	log := stubGhForCLI(t, "https://github.com/o/r/issues/42")

	var out, errBuf bytes.Buffer
	if code := runGate([]string{"stats"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errBuf.String())
	}
	if calls := ghCalls(t, log); calls != "" {
		t.Fatalf("a read-only report must never reach GitHub, it ran:\n%s", calls)
	}
	if !strings.Contains(out.String(), "test-sleep") {
		t.Fatalf("the report must still NAME the demotion candidate:\n%s", out.String())
	}
}

// Taking the write out of the report must not lose it: `escape sync` is the
// verb that already reaches GitHub, so it is where a demotion candidate turns
// into the false-positive issue somebody can answer.
func TestEscapeSyncOpensTheDemoteCandidateIssues(t *testing.T) {
	cfg := gateConfigDir(t)
	risingDenials(t, cfg)
	gitHubRepoCwd(t)
	log := stubGhForCLI(t, "https://github.com/o/r/issues/42")

	var out, errBuf bytes.Buffer
	if code := runGate([]string{"escape", "sync"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errBuf.String())
	}
	calls := ghCalls(t, log)
	if !strings.Contains(calls, "issue create") || !strings.Contains(calls, "test-sleep") {
		t.Fatalf("sync must open the false-positive issue for the candidate, it ran:\n%s", calls)
	}
}
