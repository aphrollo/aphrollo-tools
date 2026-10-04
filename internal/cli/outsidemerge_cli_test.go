package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// outsideMergeFixture is an aphrollo repo (`work`, on main, with an aphrollo.toml)
// whose bare origin gained one squash-merged commit, "Add thing (#7)", made in a
// second clone the way a GitHub-side merge lands one. The repo has fetched it
// and not yet merged it. seed is the commit both started from.
func outsideMergeFixture(t *testing.T) (work, seed string) {
	t.Helper()
	gateConfigDir(t)
	work = gitInit(t, map[string]string{"aphrollo.toml": "[aphrollo]\n"})
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := fixtureGit(append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(work, "branch", "-M", "main")
	seed = git(work, "rev-parse", "HEAD")
	origin := t.TempDir()
	git(origin, "init", "-q", "--bare", "-b", "main")
	git(work, "remote", "add", "origin", origin)
	git(work, "push", "-q", "origin", "main")

	other := t.TempDir()
	git(other, "clone", "-q", origin, other)
	git(other, "config", "user.email", "t@example.com")
	git(other, "config", "user.name", "t")
	writeFile(t, filepath.Join(other, "thing.txt"), "thing\n")
	git(other, "add", ".")
	git(other, "commit", "-q", "-m", "Add thing (#7)")
	git(other, "push", "-q", "origin", "main")
	git(work, "fetch", "-q", "origin")
	return work, seed
}

func eventLog(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	for _, e := range tdd.ReadEvents(wd) {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		log.Write(line)
		log.WriteByte(0x0a)
	}
	return log.String()
}

// A pull that takes in a merge made on GitHub fires `gate postmerge`; the hook
// records that merge, once, and says so on stderr.
func TestRun_GatePostMerge_RecordsAMergeMadeOutsideTheVerb(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	work, _ := outsideMergeFixture(t)
	if out, err := fixtureGit("-C", work, "merge", "-q", "--ff-only", "origin/main").CombinedOutput(); err != nil {
		t.Fatalf("git merge: %v\n%s", err, out)
	}
	inDir(t, work)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "postmerge"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — a post-merge hook can never block; stderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "#7") {
		t.Errorf("stderr = %q, want the recorded PR #7 named", errb.String())
	}
	if n := strings.Count(eventLog(t), `"by":"outside"`); n != 2 {
		t.Errorf("events carrying by=outside = %d, want 2 (the merge and its escape)\n%s", n, eventLog(t))
	}
}

func TestRun_WorkspaceSyncSince_DryRunNamesTheMergeAndWritesNothing(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	work, seed := outsideMergeFixture(t)
	inDir(t, work)

	var out, errb bytes.Buffer
	code := Run([]string{"workspace", "sync", "--since", seed, "--dry"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "would record 1 merge(s)") || !strings.Contains(out.String(), "#7") {
		t.Errorf("stdout = %q, want the dry run to name PR #7", out.String())
	}
	if log := eventLog(t); log != "" {
		t.Errorf("events = %q, want none from --dry", log)
	}
}

func TestRun_WorkspaceSyncSince_RecordsOnceAndRefusesABadRef(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	work, seed := outsideMergeFixture(t)
	inDir(t, work)

	for range 2 {
		var out, errb bytes.Buffer
		if code := Run([]string{"workspace", "sync", "--since", seed}, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
		}
	}
	if n := strings.Count(eventLog(t), `"by":"outside"`); n != 2 {
		t.Errorf("events carrying by=outside = %d, want 2 after two runs\n%s", n, eventLog(t))
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"workspace", "sync", "--since", "no-such-ref"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Errorf("exit = %d, want 1 for a ref that does not resolve", code)
	}
	if !strings.Contains(errb.String(), "no-such-ref") {
		t.Errorf("stderr = %q, want the bad ref named", errb.String())
	}
}
