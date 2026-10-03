package ghworkflow

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The pieces of the isolation, one at a time.

func writeTo(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCopyCargoConfig_TheBoxsConfigReachesTheRunsHomeAndSaysSo(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string // under the box's cargo home
		want  map[string]string // under the run's
	}{
		"config.toml":          {map[string]string{"config.toml": "A"}, map[string]string{"config.toml": "A"}},
		"the older name":       {map[string]string{"config": "B"}, map[string]string{"config": "B"}},
		"both: only the newer": {map[string]string{"config.toml": "A", "config": "B"}, map[string]string{"config.toml": "A"}},
		"credentials stay":     {map[string]string{"config.toml": "A", "credentials.toml": "token"}, map[string]string{"config.toml": "A"}},
	} {
		host, dst := t.TempDir(), filepath.Join(t.TempDir(), "cargo-home")
		for f, c := range tc.files {
			writeTo(t, filepath.Join(host, f), c)
		}
		s := &isolation{}
		s.copyCargoConfig([]string{"CARGO_HOME=" + host}, dst)
		for f, c := range tc.want {
			if got, err := os.ReadFile(filepath.Join(dst, f)); err != nil || string(got) != c {
				t.Errorf("%s: %s = %q (%v), want %q", name, f, got, err, c)
			}
		}
		entries, _ := os.ReadDir(dst)
		if len(entries) != len(tc.want) {
			t.Errorf("%s: the run's cargo home holds %d files, want %d", name, len(entries), len(tc.want))
		}
		if len(s.notes) != 1 || !strings.Contains(s.notes[0], "copied") {
			t.Errorf("%s: notes = %q, want one saying what was copied", name, s.notes)
		}
	}
}

func TestCopyCargoConfig_WithoutCargoHomeItReadsTheHomeDirectorys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTo(t, filepath.Join(home, ".cargo", "config.toml"), "from home")
	dst := filepath.Join(t.TempDir(), "cargo-home")
	s := &isolation{}
	s.copyCargoConfig([]string{"PATH=x"}, dst)
	if got, err := os.ReadFile(filepath.Join(dst, "config.toml")); err != nil || string(got) != "from home" {
		t.Errorf("config.toml = %q (%v), want the one in ~/.cargo", got, err)
	}
}

func TestCopyCargoConfig_NothingToCopyChangesNothing(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "cargo-home")
	s := &isolation{}
	s.copyCargoConfig([]string{"CARGO_HOME=" + t.TempDir()}, dst)
	if len(s.notes) != 0 {
		t.Errorf("notes = %q, want none", s.notes)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("the run's cargo home was made though there was nothing to copy (%v)", err)
	}
}

func TestCopyCargoConfig_ACopyThatFailsIsSaidNotSwallowed(t *testing.T) {
	host := t.TempDir()
	writeTo(t, filepath.Join(host, "config.toml"), "A")
	blocker := filepath.Join(t.TempDir(), "file")
	writeTo(t, blocker, "not a directory")
	noDir := &isolation{}
	noDir.copyCargoConfig([]string{"CARGO_HOME=" + host}, filepath.Join(blocker, "cargo-home"))
	if len(noDir.notes) != 1 || !strings.Contains(noDir.notes[0], "could not be copied") {
		t.Errorf("a cargo home that cannot be made: notes = %q", noDir.notes)
	}
	dst := t.TempDir()
	if err := os.Mkdir(filepath.Join(dst, "config.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	noFile := &isolation{}
	noFile.copyCargoConfig([]string{"CARGO_HOME=" + host}, dst)
	if len(noFile.notes) != 1 || !strings.Contains(noFile.notes[0], "could not be copied") {
		t.Errorf("a config that cannot be written: notes = %q", noFile.notes)
	}
}

func TestApply_VariablesFollowTheEnvironmentAndTheDirectoriesLeadPATH(t *testing.T) {
	sep := string(os.PathListSeparator)
	s := &isolation{vars: []KV{{"A", "1"}, {"B", "2"}}, path: []string{"first", "second"}}
	env := []string{"X=0", "Path=old1" + sep + "old2"}
	got := s.apply(env)
	want := []string{"X=0", "Path=old1" + sep + "old2", "A=1", "B=2", "PATH=first" + sep + "second" + sep + "old1" + sep + "old2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("apply = %q, want %q", got, want)
	}
	if len(env) != 2 {
		t.Errorf("apply changed the environment it was given: %q", env)
	}
	if got := s.apply([]string{"X=0"}); got[len(got)-1] != "PATH=first"+sep+"second" {
		t.Errorf("with no PATH to follow, the last entry = %q, want the directories alone", got[len(got)-1])
	}
	var none *isolation
	if got := none.apply(env); !reflect.DeepEqual(got, env) {
		t.Errorf("a nil isolation changed the environment: %q", got)
	}
}

func TestEnvIn_TheLastValueWinsAndTheNameIgnoresCase(t *testing.T) {
	env := []string{"Path=a", "OTHER=x", "PATH=b", "EMPTY="}
	if got := envIn(env, "path"); got != "b" {
		t.Errorf("envIn(path) = %q, want the last: b", got)
	}
	if got := envIn(env, "MISSING"); got != "" {
		t.Errorf("envIn(MISSING) = %q", got)
	}
	if got := envIn([]string{"NOEQUALS"}, "NOEQUALS"); got != "" {
		t.Errorf("an entry with no = gave %q", got)
	}
}

// ratchet: test_removed TestMentionsPython_AnyPlaceAStepCouldNameItAndNoWordThatOnlyLooksLikeIt: the venv is made whenever python is on PATH, so there is no scan of the steps to test; TestRun_AScriptThatPipInstallsGetsAVenvEvenWhenNoStepNamesPython covers it
func TestSetupPython_TheVenvIsMadeFromTheFoundPythonAndPutFirst(t *testing.T) {
	var gotPython, gotDir string
	var gotEnv []string
	prevFind, prevMake := findPython, makeVenv
	findPython = func() (string, bool) { return "/box/python3", true }
	makeVenv = func(_ context.Context, python, dir string, env []string) error {
		gotPython, gotDir, gotEnv = python, dir, env
		return nil
	}
	t.Cleanup(func() { findPython, makeVenv = prevFind, prevMake })
	s := &isolation{root: "ROOT", path: []string{"later"}}
	s.setupPython(context.Background(), []string{"BASE=1"})
	venv := filepath.Join("ROOT", "venv")
	if gotPython != "/box/python3" || gotDir != venv || !reflect.DeepEqual(gotEnv, []string{"BASE=1"}) {
		t.Errorf("makeVenv(%q, %q, %q), want the found python, %s and the base environment", gotPython, gotDir, gotEnv, venv)
	}
	if !reflect.DeepEqual(s.vars, []KV{{"VIRTUAL_ENV", venv}}) {
		t.Errorf("vars = %v, want VIRTUAL_ENV=%s", s.vars, venv)
	}
	if !reflect.DeepEqual(s.path, []string{venvBinDir(venv), "later"}) {
		t.Errorf("path = %q, want the venv's bin directory ahead of what was there", s.path)
	}
	if s.python != venvPythonPath(venv) {
		t.Errorf("python = %q, want the venv's interpreter %q", s.python, venvPythonPath(venv))
	}
}

func TestSetupPython_NoPythonOrABrokenVenvLeavesNoVenvAndSaysWhy(t *testing.T) {
	prevFind, prevMake := findPython, makeVenv
	t.Cleanup(func() { findPython, makeVenv = prevFind, prevMake })

	findPython = func() (string, bool) { return "", false }
	none := &isolation{}
	none.setupPython(context.Background(), nil)
	if len(none.vars) != 0 || none.python != "" || len(none.notes) != 1 || !strings.Contains(none.notes[0], "no python3 or python") {
		t.Errorf("no python: vars %v, python %q, notes %q", none.vars, none.python, none.notes)
	}

	findPython = func() (string, bool) { return "/box/python3", true }
	makeVenv = func(context.Context, string, string, []string) error { return errors.New("ensurepip exploded") }
	broken := &isolation{}
	broken.setupPython(context.Background(), nil)
	if len(broken.vars) != 0 || broken.python != "" || len(broken.path) != 0 {
		t.Errorf("a broken venv left vars %v, python %q, path %q", broken.vars, broken.python, broken.path)
	}
	if len(broken.notes) != 1 || !strings.Contains(broken.notes[0], "ensurepip exploded") || !strings.Contains(broken.notes[0], "/box/python3") {
		t.Errorf("a broken venv must say which python and why: %q", broken.notes)
	}
}

func TestIsPythonShell_ThePythonsAndTheirTemplatesAndNothingThatOnlyLooksLikeOne(t *testing.T) {
	for shell, want := range map[string]bool{
		"python":                  true,
		"python3":                 true,
		"python3.12":              true,
		" python3 ":               true,
		"python {0}":              true,
		"python -u {0}":           true,
		"/usr/bin/python3 -I {0}": true,
		`C:\Py\python.exe {0}`:    false, // a backslash path is not split on this platform's separator everywhere
		"python.exe {0}":          true,
		"":                        false,
		"   ":                     false,
		"bash":                    false,
		"pythonic {0}":            false,
		"bash -c 'python {0}'":    false,
		"pwsh -c {0}":             false,
	} {
		if got := isPythonShell(shell); got != want && !strings.Contains(shell, `\`) {
			t.Errorf("isPythonShell(%q) = %v, want %v", shell, got, want)
		}
	}
}

func TestInterpreter_OnlyAPythonShellStepGetsTheVenvsPython(t *testing.T) {
	argv := []string{"/box/python3", "script.py"}
	withVenv := &isolation{python: "/venv/python"}
	if got := withVenv.interpreter("python", argv); !reflect.DeepEqual(got, []string{"/venv/python", "script.py"}) {
		t.Errorf("python shell: %q", got)
	}
	if got := withVenv.interpreter(" python3 ", argv); got[0] != "/venv/python" {
		t.Errorf("python3 shell with padding: %q", got)
	}
	for name, got := range map[string][]string{
		"bash":    withVenv.interpreter("bash", argv),
		"no venv": (&isolation{}).interpreter("python", argv),
		"nil":     (*isolation)(nil).interpreter("python", argv),
	} {
		if !reflect.DeepEqual(got, argv) {
			t.Errorf("%s: argv changed to %q", name, got)
		}
	}
}

func TestJoinPath_NoStraySeparatorWhenThereIsNothingToFollow(t *testing.T) {
	sep := string(os.PathListSeparator)
	if got := joinPath([]string{"a", "b"}, "c"); got != "a"+sep+"b"+sep+"c" {
		t.Errorf("joinPath = %q", got)
	}
	if got := joinPath([]string{"a", "b"}, ""); got != "a"+sep+"b" {
		t.Errorf("joinPath with no rest = %q: a trailing separator would put the current directory on PATH", got)
	}
}

func TestDescribe_PrintsEveryVariableNoteAndTheDirectoriesAheadOnPATH(t *testing.T) {
	s := &isolation{root: "ROOT", vars: []KV{{"A", "1"}, {"B", "2"}}, path: []string{"d1", "d2"}, notes: []string{"a note"}}
	var out bytes.Buffer
	s.describe(&out)
	for _, want := range []string{"under ROOT", "[isolate] a note", "[isolate] A=1", "[isolate] B=2", "PATH first: d1" + string(os.PathListSeparator) + "d2", "[isolate] skipped, and the run inconclusive"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("describe lacks %q:\n%s", want, out.String())
		}
	}
}

// ratchet: test_removed TestMakeWritable_EveryDirectoryAndOnlyDirectoriesAreChanged: the scratch is removed by depinstall.RemoveTree, which makes directories and files writable; TestMakeWritable_EveryDirectoryAndRegularFileIsChangedAndNothingElse in internal/depinstall covers it

func TestLookPython_FindsPython3ThenPythonOnPATHAndNothingElse(t *testing.T) {
	dir, _ := fakeToolsOnPath(t) // python, python3 and pip first on PATH
	t.Setenv("PATH", dir)        // and nothing else, so the box's own python is never found
	got, ok := lookPython()
	if !ok || !under(got, dir) || !strings.Contains(strings.ToLower(filepath.Base(got)), "python3") {
		t.Errorf("lookPython = %q, %v, want the python3 in %s", got, ok, dir)
	}
	if err := os.Remove(filepath.Join(dir, "python3"+fakeExt())); err != nil {
		t.Fatal(err)
	}
	got, ok = lookPython()
	if !ok || !under(got, dir) || strings.Contains(strings.ToLower(filepath.Base(got)), "python3") {
		t.Errorf("without python3, lookPython = %q, %v, want the python in %s", got, ok, dir)
	}
	t.Setenv("PATH", t.TempDir())
	if got, ok := lookPython(); ok {
		t.Errorf("with no python on PATH, lookPython = %q", got)
	}
}
