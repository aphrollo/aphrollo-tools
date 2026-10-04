package workspace

import (
	"strings"
	"testing"
	"time"
)

// A call through the host keeps the verbs' per-call deadline (ghTimeout): the
// move onto the port neither loosens nor tightens it, including for the
// merge_group explanation, whose total of three minutes is a bound on top.
func TestHost_EveryCallKeepsTheGhTimeout(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	putSlowGHStubOnPath(t)
	t.Setenv("SLOWSTUB_SLEEP_MS", "20000")
	defer func(d time.Duration) { ghTimeout = d }(ghTimeout)
	ghTimeout = 300 * time.Millisecond

	for name, call := range map[string]func() error{
		"newHost": func() error { _, err := hostFor(repo).RunsOn("b", "pull_request", 1); return err },
		"hostUntil": func() error {
			_, err := hostUntil(repo, time.Now().Add(3*time.Minute)).RunsOn("b", "pull_request", 1)
			return err
		},
	} {
		started := time.Now()
		err := call()
		if err == nil || !strings.Contains(err.Error(), "timed out after 300ms") {
			t.Errorf("%s: err = %v, want a timeout after the 300ms ghTimeout", name, err)
		}
		if elapsed := time.Since(started); elapsed > 15*time.Second {
			t.Errorf("%s: waited %s, the per-call deadline did not fire", name, elapsed)
		}
	}
}

func TestHostUntil_ASpentTotalRefusesTheNextCall(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")

	_, err := hostUntil(repo, time.Now().Add(-time.Second)).RunsOn("b", "pull_request", 1)

	if err == nil || !strings.Contains(err.Error(), "spent") {
		t.Fatalf("err = %v, want the spent total named", err)
	}
}
