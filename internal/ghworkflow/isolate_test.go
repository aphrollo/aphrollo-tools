package ghworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A run must never change the box it runs on: GitHub's runners are thrown away
// after a job, this box is not (#1102: pip install -r requirements.txt from a
// workflow downgraded cryptography 50 to 44 in a production host's global
// python). These tests pin where installs land, with fake pip and python on PATH.

// isolateValue is the value a run printed for one isolation variable.
func isolateValue(out, key string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "[isolate] "+key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// under reports whether path is dir or inside it, on either slash and without
// regard to case, as a Windows path may be spelled.
func under(path, dir string) bool {
	p := strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	d := strings.ToLower(filepath.ToSlash(filepath.Clean(dir)))
	return p == d || strings.HasPrefix(p, d+"/")
}

// fakeLogLines is what the fake tools recorded: the path of the copy that ran,
// and what it was asked.
func fakeLogLines(t *testing.T, log string) [][2]string {
	t.Helper()
	var lines [][2]string
	for _, l := range strings.Split(strings.TrimSpace(readFile(t, log)), "\n") {
		exe, call, _ := strings.Cut(l, "\t")
		lines = append(lines, [2]string{exe, call})
	}
	return lines
}

func TestRun_PipInstallsLandInTheRunsVenvNotTheBoxs(t *testing.T) {
	boxBin, log := fakeToolsOnPath(t)
	useFakePython(t, boxBin)
	sum, out, _ := runFlow(t, `
on: pull_request
jobs:
  deps:
    steps:
      - name: Install requirements
        run: pip install -r requirements.txt
      - name: Install through the interpreter
        run: python -m pip install other-package
`)
	if sum.Failed() {
		t.Fatalf("run failed:\n%s", out)
	}
	venv, ok := isolateValue(out, "VIRTUAL_ENV")
	if !ok {
		t.Fatalf("the run printed no VIRTUAL_ENV:\n%s", out)
	}
	calls := fakeLogLines(t, log)
	if len(calls) != 2 {
		t.Fatalf("fake pip/python recorded %d calls, want 2: %v", len(calls), calls)
	}
	for _, c := range calls {
		if under(c[0], boxBin) {
			t.Errorf("%q ran the box's own %s, not the venv's", c[1], c[0])
		}
		if !under(c[0], venv) {
			t.Errorf("%q ran %s, outside the run's venv %s", c[1], c[0], venv)
		}
	}
	if _, err := os.Stat(venv); !os.IsNotExist(err) {
		t.Errorf("the run's venv %s outlived the run (stat err %v)", venv, err)
	}
}

func TestRun_AShellPythonStepRunsTheVenvsInterpreter(t *testing.T) {
	boxBin, log := fakeToolsOnPath(t)
	useFakePython(t, boxBin)
	sum, out, _ := runFlow(t, `
on: pull_request
jobs:
  script:
    steps:
      - shell: python
        run: print("hello")
`)
	if sum.Failed() {
		t.Fatalf("run failed:\n%s", out)
	}
	venv, _ := isolateValue(out, "VIRTUAL_ENV")
	calls := fakeLogLines(t, log)
	if len(calls) != 1 || !under(calls[0][0], venv) || venv == "" {
		t.Errorf("a shell: python step ran %v, want the interpreter inside the venv %q", calls, venv)
	}
}

func TestRun_NoVenvIsMadeWhenNoStepMentionsPython(t *testing.T) {
	boxBin, log := fakeToolsOnPath(t)
	useFakePython(t, boxBin)
	sum, out, _ := runFlow(t, `
on: pull_request
jobs:
  build:
    steps:
      - run: echo building
`)
	if sum.Failed() {
		t.Fatalf("run failed:\n%s", out)
	}
	if v, ok := isolateValue(out, "VIRTUAL_ENV"); ok {
		t.Errorf("a venv %s was made for a run that never mentions python", v)
	}
	if got, ok := isolateValue(out, "PIP_REQUIRE_VIRTUALENV"); !ok || got != "true" {
		t.Errorf("without a venv pip must still refuse a global install: PIP_REQUIRE_VIRTUALENV=%q (printed %v)", got, ok)
	}
	if _, err := os.Stat(log); err == nil {
		t.Errorf("the box's python ran for a run that never mentions it:\n%s", readFile(t, log))
	}
}

func TestRun_AVenvThatCannotBeMadeIsSaidAndPipStaysRefused(t *testing.T) {
	prev := findPython
	findPython = func() (string, bool) { return filepath.Join(t.TempDir(), "no-such-python"), true }
	t.Cleanup(func() { findPython = prev })
	sum, out, _ := runFlow(t, `
on: pull_request
jobs:
  deps:
    steps:
      - run: echo python is mentioned here
`)
	if sum.Failed() {
		t.Fatalf("a venv that cannot be made is said, not fatal; the run failed:\n%s", out)
	}
	if _, ok := isolateValue(out, "VIRTUAL_ENV"); ok {
		t.Errorf("a VIRTUAL_ENV was printed though the venv could not be made:\n%s", out)
	}
	if got, ok := isolateValue(out, "PIP_REQUIRE_VIRTUALENV"); !ok || got != "true" {
		t.Errorf("PIP_REQUIRE_VIRTUALENV = %q (printed %v), want true so pip cannot touch the box", got, ok)
	}
	if !strings.Contains(out, "no-such-python") {
		t.Errorf("the output must name the python that failed:\n%s", out)
	}
}

func TestRun_AGlobalInstallIsRefusedNamingItsStepAndNothingAfterItRuns(t *testing.T) {
	sum, out, dir := runFlow(t, `
on: pull_request
jobs:
  sys:
    steps:
      - run: echo before >> log.txt
      - name: Install system libs
        run: sudo -n apt-get --version
      - run: echo after >> log.txt
`)
	r := result(t, sum, "sys")
	if r.Result != ResultFailure || !strings.Contains(r.Detail, "Install system libs") {
		t.Errorf("sys = %+v, want a failure naming the step", r)
	}
	if !strings.Contains(out, "[refuse] Install system libs") || !strings.Contains(out, "sudo") {
		t.Errorf("the refusal must name the step and the command:\n%s", out)
	}
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "before\n" {
		t.Errorf("log = %q, want only the step before the refused one", got)
	}
}

func TestRun_EveryInstallTargetIsUnderTheRunsScratchAndWinsOverTheEnvironment(t *testing.T) {
	host := t.TempDir()
	sum, out, dir := runFlow(t, `
on: pull_request
env:
  npm_config_prefix: /usr/local
jobs:
  vars:
    steps:
      - run: |
          for v in GOPATH GOMODCACHE GOBIN CARGO_HOME npm_config_prefix npm_config_cache PIP_CACHE_DIR; do
            eval "echo $v=\$$v" >> seen.txt
          done
          echo "PATH=$PATH" >> seen.txt
`, func(o *Options) {
		o.Env = append(os.Environ(), "GOPATH="+host, "CARGO_HOME="+host, "npm_config_cache="+host)
	})
	if sum.Failed() {
		t.Fatalf("run failed:\n%s", out)
	}
	seen := map[string]string{}
	for _, l := range strings.Split(readFile(t, filepath.Join(dir, "seen.txt")), "\n") {
		k, v, _ := strings.Cut(l, "=")
		seen[k] = v
	}
	for _, key := range []string{"GOPATH", "GOMODCACHE", "GOBIN", "CARGO_HOME", "npm_config_prefix", "npm_config_cache", "PIP_CACHE_DIR"} {
		printed, ok := isolateValue(out, key)
		if !ok {
			t.Errorf("%s was set for the steps but never printed:\n%s", key, out)
			continue
		}
		if !strings.Contains(filepath.ToSlash(printed), "/isolation/") {
			t.Errorf("%s = %s, want a directory inside the run's isolation", key, printed)
		}
		if got := filepath.ToSlash(seen[key]); !strings.Contains(got, "/isolation/") {
			t.Errorf("a step saw %s=%q, want the isolated directory the run printed (%s)", key, seen[key], printed)
		}
	}
	for _, bin := range []string{"/isolation/npm-prefix", "/isolation/gopath/bin", "/isolation/cargo-home/bin"} {
		if !strings.Contains(seen["PATH"], bin) {
			t.Errorf("PATH %q lacks %s, so what an install puts there would not be found", seen["PATH"], bin)
		}
	}
}

func TestRun_TheScratchIsRemovedEvenWhenItsModuleCacheIsReadOnly(t *testing.T) {
	sum, out, _ := runFlow(t, `
on: pull_request
jobs:
  mods:
    steps:
      - run: |
          case "$GOMODCACHE" in *isolation*) ;; *) echo "not isolated, touching nothing: $GOMODCACHE"; exit 1;; esac
          mkdir -p "$GOMODCACHE/example.com/m@v1/sub"
          echo x > "$GOMODCACHE/example.com/m@v1/sub/file.go"
          chmod -R a-w "$GOMODCACHE/example.com"
`)
	if sum.Failed() {
		t.Fatalf("run failed:\n%s", out)
	}
	cache, ok := isolateValue(out, "GOMODCACHE")
	if !ok {
		t.Fatalf("no GOMODCACHE printed:\n%s", out)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Errorf("the run's module cache %s outlived the run (stat err %v)", cache, err)
	}
	if strings.Contains(out, "was not removed") {
		t.Errorf("a scratch directory that was removed was reported as kept:\n%s", out)
	}
}

func TestRun_CargoGetsTheBoxsConfigButNotItsHome(t *testing.T) {
	host := t.TempDir()
	if err := os.WriteFile(filepath.Join(host, "config.toml"), []byte("[net]\nretry = 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, out, dir := runFlow(t, `
on: pull_request
jobs:
  c:
    steps:
      - run: cat "$CARGO_HOME/config.toml" >> got.txt
`, func(o *Options) { o.Env = append(os.Environ(), "CARGO_HOME="+host) })
	if sum.Failed() {
		t.Fatalf("run failed:\n%s", out)
	}
	if got := readFile(t, filepath.Join(dir, "got.txt")); got != "[net]\nretry = 7\n" {
		t.Errorf("the step's cargo config = %q, want the box's config.toml copied", got)
	}
	if home, _ := isolateValue(out, "CARGO_HOME"); under(home, host) {
		t.Errorf("CARGO_HOME = %s is the box's own %s", home, host)
	}
}
