package handoff

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

// install makes a fake user-space install whose current is ver.
func install(t *testing.T, ver string) (root, bin string) {
	t.Helper()
	root = t.TempDir()
	bin = userbin.BinaryPath(root, ver)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := userbin.SetCurrent(root, ver); err != nil {
		t.Fatal(err)
	}
	return root, bin
}

type call struct {
	path string
	args []string
	env  []string
}

func run(t *testing.T, args []string, self, root string, env map[string]string) (code int, handed bool, calls []call, stderr string) {
	t.Helper()
	var buf bytes.Buffer
	launch := func(path string, a, e []string, announce func()) (int, error) {
		calls = append(calls, call{path, a, e})
		announce()
		return 42, nil
	}
	getenv := func(k string) string { return env[k] }
	code, handed = Maybe(args, self, root, getenv, &buf, launch, func(string) bool { return true })
	return code, handed, calls, buf.String()
}

func TestMaybe_NewerInstallTakesTheVerb(t *testing.T) {
	root, bin := install(t, "1.39.0")
	code, handed, calls, stderr := run(t, []string{"workspace", "merge", "--wait"}, "1.23.1", root, nil)
	if !handed || code != 42 {
		t.Fatalf("handed=%v code=%d, want true 42 (the child's exit code)", handed, code)
	}
	if len(calls) != 1 || calls[0].path != bin || strings.Join(calls[0].args, " ") != "workspace merge --wait" {
		t.Fatalf("launch = %+v, want one call to %s with the same argv", calls, bin)
	}
	if !slicesContains(calls[0].env, GuardEnv+"=1.39.0") {
		t.Fatalf("child env lacks %s=1.39.0", GuardEnv)
	}
	want := "aphrollo 1.23.1 -> 1.39.0 (" + bin + "): a newer install runs this\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

func TestMaybe_GuardPreventsALoop(t *testing.T) {
	root, _ := install(t, "1.39.0")
	_, handed, calls, stderr := run(t, []string{"workspace", "list"}, "1.23.1", root, map[string]string{GuardEnv: "1.39.0"})
	if handed || len(calls) != 0 || stderr != "" {
		t.Fatalf("handed=%v calls=%d stderr=%q, want none once the guard is set", handed, len(calls), stderr)
	}
}

func TestMaybe_OptOutDisablesHandoff(t *testing.T) {
	root, _ := install(t, "1.39.0")
	_, handed, _, _ := run(t, []string{"ratchet", "check"}, "1.23.1", root, map[string]string{OptOutEnv: "1"})
	if handed {
		t.Fatal("handed off despite " + OptOutEnv)
	}
}

func TestMaybe_EqualOrOlderStaysSelf(t *testing.T) {
	for _, cur := range []string{"1.23.1", "1.2.0", "0.9.9"} {
		root, _ := install(t, cur)
		_, handed, calls, stderr := run(t, []string{"workspace", "list"}, "1.23.1", root, nil)
		if handed || len(calls) != 0 || stderr != "" {
			t.Fatalf("current %s: handed=%v stderr=%q, want self silently", cur, handed, stderr)
		}
	}
}

func TestMaybe_NumericNotLexicalCompare(t *testing.T) {
	root, _ := install(t, "1.9.0")
	_, handed, _, _ := run(t, []string{"ci", "why"}, "1.10.0", root, nil)
	if handed {
		t.Fatal("1.9.0 treated as newer than 1.10.0")
	}
}

func TestMaybe_FailOpen(t *testing.T) {
	t.Run("unreadable pointer", func(t *testing.T) {
		root := t.TempDir()
		_, handed, _, stderr := run(t, []string{"workspace", "list"}, "1.23.1", root, nil)
		if handed || stderr != "" {
			t.Fatalf("handed=%v stderr=%q", handed, stderr)
		}
	})
	t.Run("non-semver pointer", func(t *testing.T) {
		root, _ := install(t, "1.39.0")
		if err := userbin.SetCurrent(root, "latest"); err != nil {
			t.Fatal(err)
		}
		_, handed, _, _ := run(t, []string{"workspace", "list"}, "1.23.1", root, nil)
		if handed {
			t.Fatal("handed off to a non-semver version")
		}
	})
	t.Run("missing binary", func(t *testing.T) {
		root, bin := install(t, "1.39.0")
		if err := os.Remove(bin); err != nil {
			t.Fatal(err)
		}
		_, handed, _, _ := run(t, []string{"workspace", "list"}, "1.23.1", root, nil)
		if handed {
			t.Fatal("handed off to a missing binary")
		}
	})
	t.Run("self is a dev build", func(t *testing.T) {
		root, _ := install(t, "1.39.0")
		_, handed, _, _ := run(t, []string{"workspace", "list"}, "0.0.0-dev+abc1234", root, nil)
		if handed {
			t.Fatal("a dev build handed off")
		}
	})
}

func TestMaybe_LaunchErrorRunsSelf(t *testing.T) {
	root, _ := install(t, "1.39.0")
	var buf bytes.Buffer
	_, handed := Maybe([]string{"workspace", "list"}, "1.23.1", root, func(string) string { return "" }, &buf,
		func(string, []string, []string, func()) (int, error) { return 0, os.ErrPermission }, func(string) bool { return true })
	if handed {
		t.Fatal("a failed launch must fall back to self")
	}
}

func TestMaybe_VerbList(t *testing.T) {
	root, _ := install(t, "1.39.0")
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"workspace", "merge"}, true},
		{[]string{"gate", "pretooluse"}, true},
		{[]string{"gate", "premerge"}, true},
		{[]string{"ratchet", "check"}, true},
		{[]string{"ci", "run"}, true},
		{[]string{"version"}, false},
		{[]string{"update"}, false},
		{[]string{"install"}, false},
		{[]string{"gate", "init"}, false},
		{[]string{"gate", "allow"}, false},
		{[]string{"workspace", "--help"}, false},
		{[]string{"ratchet", "-h"}, false},
		{[]string{"help"}, false},
		{nil, false},
	}
	for _, c := range cases {
		_, handed, _, _ := run(t, c.args, "1.23.1", root, nil)
		if handed != c.want {
			t.Errorf("%v: handed=%v, want %v", c.args, handed, c.want)
		}
	}
}

func TestNewer_ReportsVersionAndPath(t *testing.T) {
	root, bin := install(t, "1.39.0")
	v, p, ok := Newer("1.23.1", root, func(string) bool { return true })
	if !ok || v != "1.39.0" || p != bin {
		t.Fatalf("Newer = %q %q %v, want 1.39.0 %s true", v, p, ok, bin)
	}
	if _, _, ok := Newer("1.39.0", root, func(string) bool { return true }); ok {
		t.Fatal("equal version reported newer")
	}
}

// The real launcher passes the exit code, stdout and the env through.
func TestLaunch_PassesExitCodeThrough(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "aphrollo"+userbin.ExeSuffix)
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), "HANDOFF_TEST_MODE=launch", "HANDOFF_TEST_TARGET="+target)
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 7 {
		t.Fatalf("err = %v, want exit code 7 from the newer binary", err)
	}
	if out.String() != "fake:1" {
		t.Fatalf("stdout = %q, want fake:1 (guard set in the child)", out.String())
	}
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestMaybe_StaleGuardNamingAnOlderVersionStillHandsOff(t *testing.T) {
	root, _ := install(t, "1.39.0")
	_, handed, _, _ := run(t, []string{"workspace", "list"}, "1.23.1", root, map[string]string{GuardEnv: "1.30.0"})
	if !handed {
		t.Fatal("a guard naming another version suppressed the handoff")
	}
}

func TestMaybe_LaunchErrorPrintsNoLine(t *testing.T) {
	root, _ := install(t, "1.39.0")
	var buf bytes.Buffer
	_, handed := Maybe([]string{"workspace", "list"}, "1.23.1", root, func(string) string { return "" }, &buf,
		func(string, []string, []string, func()) (int, error) { return 0, os.ErrPermission },
		func(string) bool { return true })
	if handed || buf.Len() != 0 {
		t.Fatalf("handed=%v stderr=%q, want nothing when the launch fails before it starts", handed, buf.String())
	}
}

func TestMaybe_GateHooksHandOffWithoutALine(t *testing.T) {
	root, _ := install(t, "1.39.0")
	_, handed, calls, stderr := run(t, []string{"gate", "pretooluse"}, "1.23.1", root, nil)
	if !handed || len(calls) != 1 || stderr != "" {
		t.Fatalf("handed=%v calls=%d stderr=%q, want a silent handoff", handed, len(calls), stderr)
	}
}

func TestMaybe_AnotherUsersBinaryDoesNotHandOff(t *testing.T) {
	root, _ := install(t, "1.39.0")
	var buf bytes.Buffer
	_, handed := Maybe([]string{"workspace", "list"}, "1.23.1", root, func(string) string { return "" }, &buf,
		func(string, []string, []string, func()) (int, error) { t.Fatal("launched"); return 0, nil },
		func(string) bool { return false })
	if handed || buf.Len() != 0 {
		t.Fatalf("handed=%v stderr=%q, want self silently", handed, buf.String())
	}
}

func TestTakes_IsTheVerbListAlone(t *testing.T) {
	if !Takes([]string{"workspace", "list"}) || Takes([]string{"version"}) {
		t.Fatal("Takes disagrees with the verb list")
	}
}
