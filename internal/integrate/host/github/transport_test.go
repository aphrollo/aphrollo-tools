package github

import (
	"context"
	"errors"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"os"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/shfake"
)

// gh's stdout and stderr used to be folded together, so a warning line (an
// update notice, a deprecation line, a proxy or auth note) landed inside the
// JSON the verbs parse as data (#883). ExecRunner returns stdout alone.
func TestExecRunner_ReturnsStdoutOnlyEvenWithAStderrWarning(t *testing.T) {
	dir := t.TempDir()
	shfake.Install(t, dir, "gh", "#!/bin/sh\nprintf '%s\n' '{\"number\":7}'\nprintf '%s\n' 'warning: a new release of gh is available' 1>&2\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := ExecRunner(t.TempDir(), 0, "pr", "view")
	if err != nil {
		t.Fatalf("ExecRunner: %v", err)
	}
	if want := `{"number":7}` + "\n"; string(got) != want {
		t.Fatalf("ExecRunner = %q, want %q (stdout only)", got, want)
	}
	if strings.Contains(string(got), "release") {
		t.Fatalf("stderr leaked into the data return: %q", got)
	}
}

// slowGH puts a gh on PATH that outlives any deadline a test sets.
func slowGH(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	shfake.Install(t, dir, "gh", "#!/bin/sh\nsleep 20\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestExecRunner_ADeadlineEndsAStalledGHAndNamesTheCall(t *testing.T) {
	slowGH(t)
	started := time.Now()

	_, err := ExecRunner(t.TempDir(), 300*time.Millisecond, "api", "user")

	if err == nil || !strings.Contains(err.Error(), "timed out after 300ms") || !strings.Contains(err.Error(), "api user") {
		t.Fatalf("err = %v, want the stalled call and its deadline named", err)
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("waited %s: the deadline did not end the child", elapsed)
	}
}

func TestGitHub_AnInterruptEndsACallInFlight(t *testing.T) {
	slowGH(t)
	ctx, cancel := context.WithCancel(context.Background())
	g := New(Options{Dir: t.TempDir(), Context: ctx})
	time.AfterFunc(300*time.Millisecond, cancel)
	started := time.Now()

	_, err := g.jobSteps(1)

	if err == nil {
		t.Fatal("a cancelled context left the call to finish")
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("waited %s: the interrupt did not end the child", elapsed)
	}
}

func TestGitHub_TheDeadlineCutsEachCallToWhatIsLeftAndRefusesOnceSpent(t *testing.T) {
	var got []time.Duration
	g := New(Options{Dir: "/lane", Timeout: time.Hour, Deadline: time.Now().Add(time.Minute),
		Runner: func(_ string, d time.Duration, _ ...string) ([]byte, error) {
			got = append(got, d)
			return []byte("{}"), nil
		}})

	g.gh("api", "x")

	if len(got) != 1 || got[0] > time.Minute || got[0] < 50*time.Second {
		t.Fatalf("per-call timeout = %v, want what is left of the minute", got)
	}
	spent := New(Options{Dir: "/lane", Deadline: time.Now().Add(-time.Second), Runner: func(string, time.Duration, ...string) ([]byte, error) {
		t.Error("a call ran after the deadline")
		return nil, nil
	}})
	if _, err := spent.gh("api", "x"); err == nil {
		t.Error("a call after the deadline was not refused")
	}
}

// A hook exports GIT_DIR; gh would then resolve the hook's repository instead
// of the directory it was run in and file into the wrong tracker.
func TestExecRunner_NoGitVariableReachesTheGHChild(t *testing.T) {
	dir := t.TempDir()
	shfake.Install(t, dir, "gh", "#!/bin/sh\nprintf 'dir=%s work=%s other=%s' \"$GIT_DIR\" \"$GIT_WORK_TREE\" \"$KEEP_ME\"\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_DIR", "/the/hooks/repo/.git")
	t.Setenv("GIT_WORK_TREE", "/the/hooks/repo")
	t.Setenv("KEEP_ME", "yes")

	for name, got := range map[string]func() ([]byte, error){
		"default":  func() ([]byte, error) { return ExecRunner(t.TempDir(), 30*time.Second, "x") },
		"New runs": func() ([]byte, error) { return New(Options{Dir: t.TempDir()}).gh("x") },
	} {
		out, err := got()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(out) != "dir= work= other=yes" {
			t.Errorf("%s: child saw %q, want no GIT_* and the rest of the environment", name, out)
		}
	}
}

func TestExecRunner_AnEnvOptionIsTheChildsWholeEnvironment(t *testing.T) {
	dir := t.TempDir()
	shfake.Install(t, dir, "gh", "#!/bin/sh\nprintf '%s' \"$ONLY_THIS\"\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, err := New(Options{Dir: t.TempDir(), Env: append(os.Environ(), "ONLY_THIS=given")}).gh("x")

	if err != nil || string(out) != "given" {
		t.Fatalf("out = %q, %v; want the environment the caller gave", out, err)
	}
}

func TestOpenIssue_AFailureCarriesAtMostFourHundredRunesOfGHsWords(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) {
		return []byte(strings.Repeat("x", 3000)), errors.New("exit status 1")
	}

	_, err := s.host(originURL).OpenIssue(host.IssueRequest{Title: "t", Body: "b"})

	if err == nil || len([]rune(err.Error())) > 460 {
		t.Fatalf("error is %d runes, want gh's words cut to 400", len([]rune(err.Error())))
	}
}
