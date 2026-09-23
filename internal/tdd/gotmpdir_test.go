package tdd

import (
	"os"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

var tempEnvKeys = tddtest.TempEnvKeys

// lastEnvValue returns the LAST binding for key in env, mirroring how
// os/exec actually resolves a duplicate-key Env slice ("only the last value
// in the slice for each duplicate key is used"). envValue (buildslots_test.go)
// returns the FIRST match instead, which is the wrong half of the slice for a
// key suiteEnv deliberately overrides: cleanGitEnv() inherits the process's
// own TMP/TEMP first, and goTmpEnv's override is appended after it.
func lastEnvValue(env []string, key string) string {
	prefix := key + "="
	val := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, prefix); ok {
			val = v
		}
	}
	return val
}

// TestSuiteEnv_GoRunnerGetsRepoLocalGotmpdir pins the fix for issue #520: a
// `go` runner must not stage its compiled test binary in the OS temp dir,
// where 133 go-build* survivors and a Defender quarantine of tdd.test.exe
// were measured. GOTMPDIR (what the go tool itself reads) and TMPDIR/TMP/TEMP
// (what os.TempDir() reads, for any test that shells out or calls
// t.TempDir() itself) must all point at the same directory, resolved beside
// the worktrees rather than inside dir (issue #532 — see GoTmpRootDir), and
// that directory must actually exist once suiteEnv returns.
func TestSuiteEnv_GoRunnerGetsRepoLocalGotmpdir(t *testing.T) {
	dir := makeGoRepo(t)
	env := suiteEnv(Runner{Cmd: "go", Args: []string{"test", "./..."}}, dir)

	want := GoTmpRootDir(dir)
	if want == "" {
		t.Fatalf("GoTmpRootDir(%q) = \"\", want a resolved path for a real git checkout", dir)
	}
	for _, key := range tempEnvKeys {
		if got := lastEnvValue(env, key); got != want {
			t.Errorf("suiteEnv %s = %q, want %q", key, got, want)
		}
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("suiteEnv did not create %q: %v", want, err)
	}
}

// TestSuiteEnv_NonGoRunnerLeavesTempVarsAlone guards the other half: a cargo
// (or pytest, or vitest) runner must keep passing straight through exactly as
// before the GOTMPDIR fix — cargo's own target dir already lands inside the
// project, so redirecting TMPDIR for it would be an unrequested behavior
// change with no bug behind it.
//
// The assertion is a DELTA against the ambient environment, never the
// absolute presence of a variable. suiteEnv builds on cleanGitEnv(), which
// starts from os.Environ(), so every variable named here may ALREADY be bound
// in the process running this test — and routinely is: the commit gate runs
// this package's own suite as a `go` runner, which is exactly the case that
// sets GOTMPDIR. Checking absolute presence reported that inherited binding
// as though suiteEnv had added it, so the test passed standalone and failed
// only when run through the gate it exists to guard (issue #539).
//
// dir is a real checkout rather than a bare t.TempDir() because goTmpEnv
// resolves its paths through primaryCheckoutRoot and yields NOTHING outside a
// git repository: with a fixture that had no repo, a suiteEnv that redirected
// every runner would still add nothing and this test would still pass.
func TestSuiteEnv_NonGoRunnerLeavesTempVarsAlone(t *testing.T) {
	dir := makeGoRepo(t)
	if GoTmpRootDir(dir) == "" {
		t.Fatalf("setup: GoTmpRootDir(%q) = \"\", so a redirect would be invisible to this test", dir)
	}

	inherited := make(map[string]string, len(tempEnvKeys))
	for _, key := range tempEnvKeys {
		inherited[key] = lastEnvValue(os.Environ(), key)
	}

	env := suiteEnv(Runner{Cmd: "cargo", Args: []string{"test"}}, dir)

	for _, key := range tempEnvKeys {
		if got := lastEnvValue(env, key); got != inherited[key] {
			t.Errorf("suiteEnv changed %s for a non-go runner: got %q, want the inherited %q", key, got, inherited[key])
		}
	}
}
