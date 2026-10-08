package precommit

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/rootseam"
)

// A declared command can run for minutes (a regen and the DB suites). Its
// success line names it, and once it has run past a short threshold one line
// says it is running, so a reader can tell a running command from a hung gate.

// noticeSink is a root's stderr that tells when a line naming a running
// command has been written, and keeps what it was given.
type noticeSink struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	running chan struct{}
	once    sync.Once
}

func newNoticeSink() *noticeSink { return &noticeSink{running: make(chan struct{})} }

func (s *noticeSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.Contains(string(p), "running") {
		s.once.Do(func() { close(s.running) })
	}
	return s.buf.Write(p)
}

func (s *noticeSink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestDeclaredPrecommit_EachSuccessLineNamesItsCommand(t *testing.T) {
	repo, frontend := makeFrontendRepo(t, declaredFrontend)
	sink := newNoticeSink()
	t.Cleanup(rootseam.SetStderr(repo, sink))
	t.Cleanup(rootseam.SetStderr(frontend, sink))

	var seen []Runner
	if res := Precommit(repo, runsAt(&seen, frontend)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}

	got := sink.text()
	for _, want := range []string{
		"declared in " + frontend + " → clean (npx tsc -p tsconfig.app.json --noEmit",
		"declared in " + frontend + " → clean (npx eslint src",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "running") {
		t.Errorf("a command that finished at once printed a start line:\n%s", got)
	}
}

// The runner of the slow command does not return until the start line has
// been written, so the line is what lets it finish, never a timing guess.
func TestDeclaredPrecommit_ACommandRunningPastTheThresholdPrintsOneStartLine(t *testing.T) {
	repo, frontend := makeFrontendRepo(t, declaredFrontend)
	sink := newNoticeSink()
	t.Cleanup(rootseam.SetStderr(repo, sink))
	t.Cleanup(rootseam.SetStderr(frontend, sink))
	t.Cleanup(setStartNoticeAfter(10 * time.Millisecond))
	slow := func(r Runner, dir string) SuiteResult {
		if dir == frontend && cmdLine(r) == "npx eslint src" {
			select {
			case <-sink.running:
			case <-time.After(30 * time.Second):
				t.Error("no start line was printed for a command that outran the threshold")
			}
		}
		return SuiteResult{Passed: true}
	}

	if res := Precommit(repo, slow); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}

	got := sink.text()
	if want := "declared in " + frontend + " → running npx eslint src"; strings.Count(got, want) != 1 {
		t.Errorf("stderr holds %d copies of %q, want one:\n%s", strings.Count(got, want), want, got)
	}
	if strings.Contains(got, "running npx tsc") {
		t.Errorf("the fast command printed a start line:\n%s", got)
	}
}

// The line is stamped when it is printed, which is the threshold after the
// command began; it says so, so a reader does not take the stamp for the start.
func TestDeclaredPrecommit_TheStartLineSaysHowLongAgoTheCommandStarted(t *testing.T) {
	repo, frontend := makeFrontendRepo(t, declaredFrontend)
	sink := newNoticeSink()
	t.Cleanup(rootseam.SetStderr(repo, sink))
	t.Cleanup(rootseam.SetStderr(frontend, sink))
	t.Cleanup(setStartNoticeAfter(2 * time.Second))
	slow := func(r Runner, dir string) SuiteResult {
		if dir == frontend && cmdLine(r) == "npx eslint src" {
			select {
			case <-sink.running:
			case <-time.After(30 * time.Second):
				t.Error("no start line was printed for a command that outran the threshold")
			}
		}
		return SuiteResult{Passed: true}
	}
	if res := Precommit(repo, slow); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if want := "→ running npx eslint src (started 2s ago) …\n"; !strings.Contains(sink.text(), want) {
		t.Errorf("stderr lacks %q:\n%s", want, sink.text())
	}
}
