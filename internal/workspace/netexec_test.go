package workspace

import (
	"errors"
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

// networkTimeoutErr must leave a real subprocess error untouched when the
// deadline was NOT what killed it — only a deadline hit gets rewritten.
func TestNetworkTimeoutErr_LeavesARealFailureUntouchedWhenTheDeadlineDidNotFire(t *testing.T) {
	real := errors.New("exit status 128")
	got := networkTimeoutErr(false, 5*time.Second, "git", []string{"fetch", "origin"}, real)
	if !errors.Is(got, real) {
		t.Fatalf("networkTimeoutErr(deadlineHit=false, ...) = %v, want the original error unwrapped", got)
	}
}

// A nil error stays nil regardless of the deadline flag — there is nothing to
// name as stalled.
func TestNetworkTimeoutErr_NilErrorStaysNil(t *testing.T) {
	if got := networkTimeoutErr(true, 5*time.Second, "git", []string{"fetch"}, nil); got != nil {
		t.Fatalf("networkTimeoutErr(nil) = %v, want nil", got)
	}
}

// When the deadline DID fire, the rewritten message names the command and
// args that stalled and the configured deadline — an operator reading it
// knows what to retry, not just "exit status -1" or "signal: killed".
func TestNetworkTimeoutErr_NamesTheStalledCommandAndDeadline(t *testing.T) {
	got := networkTimeoutErr(true, 200*time.Millisecond, "git", []string{"push", "origin", "feat/y"}, context_DeadlineExceededStandin())
	if got == nil {
		t.Fatal("expected a non-nil timeout error")
	}
	msg := got.Error()
	for _, want := range []string{"git push origin feat/y", "200ms", "timed out"} {
		if !strings.Contains(msg, want) {
			t.Errorf("timeout message %q missing %q", msg, want)
		}
	}
}

// context_DeadlineExceededStandin stands in for whatever error
// exec.Cmd.Run/Output/CombinedOutput returns when its context's deadline
// fires (typically "signal: killed" or similar) — networkTimeoutErr's
// rewrite is driven by the deadlineHit flag, not by inspecting this error's
// text, so any non-nil error works here.
func context_DeadlineExceededStandin() error { return errors.New("signal: killed") }

// slowStubSource is a tiny compiled binary standing in for "git"/"gh": it
// sleeps for SLOWSTUB_SLEEP_MS (if set), prints SLOWSTUB_STDOUT/STDERR (if
// set), then exits with SLOWSTUB_EXIT (default 0). A compiled binary, not a
// shell script, because Windows cannot exec a shell script through
// CreateProcess. It never touches the network — the "network" subprocess
// under test is this local stub, so a deadline or an error-text distinction
// is proven without any real fetch/push/gh call.
const slowStubSource = `package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	if ms := os.Getenv("SLOWSTUB_SLEEP_MS"); ms != "" {
		if n, err := strconv.Atoi(ms); err == nil {
			time.Sleep(time.Duration(n) * time.Millisecond)
		}
	}
	if s := os.Getenv("SLOWSTUB_STDOUT"); s != "" {
		fmt.Fprint(os.Stdout, s)
	}
	if s := os.Getenv("SLOWSTUB_STDERR"); s != "" {
		fmt.Fprint(os.Stderr, s)
	}
	code := 0
	if c := os.Getenv("SLOWSTUB_EXIT"); c != "" {
		if n, err := strconv.Atoi(c); err == nil {
			code = n
		}
	}
	os.Exit(code)
}
`

// slowStubDir builds the stub binary once for the whole package, under both
// the "git" and "gh" names so either seam can be pointed at it.
var slowStubDir = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "aphrollo-netexec-stub")
	if err != nil {
		return "", err
	}
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(slowStubSource), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module slowstub\n\ngo 1.26\n"), 0o644); err != nil {
		return "", err
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	for _, name := range []string{"git", "gh"} {
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, name+ext), ".")
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("building the slow stub as %s: %v\n%s", name, err, out)
		}
	}
	return dir, nil
})

// putSlowStubOnPath prepends the stub dir to PATH for the duration of the
// test, restored by t.Setenv's own cleanup.
func putSlowStubOnPath(t *testing.T) {
	t.Helper()
	dir, err := slowStubDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// gitNetworkOutput must give up on a stalled git subprocess at
// gitNetworkTimeout rather than waiting it out, and say so in the error.
func TestGitNetworkOutput_GivesUpOnAStalledFetchAtTheDeadline(t *testing.T) {
	putSlowStubOnPath(t)
	t.Setenv("SLOWSTUB_SLEEP_MS", "3000")
	defer func(d time.Duration) { gitNetworkTimeout = d }(gitNetworkTimeout)
	gitNetworkTimeout = 200 * time.Millisecond

	started := time.Now()
	_, err := gitNetworkOutput(t.TempDir(), "fetch", "origin", "--quiet")
	elapsed := time.Since(started)

	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout error, got %v", err)
	}
	// Generous headroom over the 200ms deadline: the assertion is that the
	// call did not wait out the stub's full three seconds.
	if elapsed > 2*time.Second {
		t.Fatalf("gitNetworkOutput waited %s — the deadline did not fire", elapsed)
	}
}

// ghOutput must give up on a stalled gh subprocess at ghTimeout the same way.
func TestGhOutput_GivesUpOnAStalledCallAtTheDeadline(t *testing.T) {
	putSlowStubOnPath(t)
	t.Setenv("SLOWSTUB_SLEEP_MS", "3000")
	defer func(d time.Duration) { ghTimeout = d }(ghTimeout)
	ghTimeout = 200 * time.Millisecond

	started := time.Now()
	_, err := ghOutput(t.TempDir(), "pr", "view")
	elapsed := time.Since(started)

	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout error, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("ghOutput waited %s — the deadline did not fire", elapsed)
	}
}
