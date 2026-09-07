package tdd

import (
	"os"
	"strings"
	"testing"
)

// envHasKey reports whether env carries a binding for key at all, for
// asserting a variable was NOT set — envValue (buildslots_test.go) returns ""
// both when a key is absent and when it is bound to the empty string, so it
// cannot answer that question.
func envHasKey(env []string, key string) bool {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}

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
	for _, key := range []string{"GOTMPDIR", "TMPDIR", "TMP", "TEMP"} {
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
// before this fix — cargo's own target dir already lands inside the project,
// so redirecting TMPDIR for it would be an unrequested behavior change with
// no bug behind it.
func TestSuiteEnv_NonGoRunnerLeavesTempVarsAlone(t *testing.T) {
	// t.TempDir() must be taken BEFORE the sentinel TMPDIR is set: on Unix the
	// helper resolves through TMPDIR, so pointing it at a path that does not
	// exist makes t.TempDir() itself fail ("TempDir: stat /somewhere/real: no
	// such file or directory") before this test asserts anything. Windows hid
	// that, because t.TempDir() reads TMP/TEMP there and never consults TMPDIR.
	dir := t.TempDir()
	t.Setenv("TMPDIR", "/somewhere/real")

	env := suiteEnv(Runner{Cmd: "cargo", Args: []string{"test"}}, dir)

	if envHasKey(env, "GOTMPDIR") {
		t.Error("suiteEnv set GOTMPDIR for a non-go runner")
	}
	if got := envValue(env, "TMPDIR"); got != "/somewhere/real" {
		t.Errorf("suiteEnv overrode TMPDIR for a non-go runner: got %q", got)
	}
}
