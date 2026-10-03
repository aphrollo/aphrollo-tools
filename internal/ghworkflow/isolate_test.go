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

func TestRun_AShellPythonTemplateStepIsPythonNotShellAndRunsTheVenvsInterpreter(t *testing.T) {
	boxBin, log := fakeToolsOnPath(t)
	useFakePython(t, boxBin)
	sum, out, _ := runFlow(t, `
on: pull_request
jobs:
  script:
    steps:
      - shell: python -u {0}
        run: sudo apt-get install libfoo
`)
	if sum.Failed() || len(sum.Refused()) != 0 {
		t.Fatalf("a python script is not shell text to scan; refused %q:\n%s", sum.Refused(), out)
	}
	venv, _ := isolateValue(out, "VIRTUAL_ENV")
	calls := fakeLogLines(t, log)
	if len(calls) != 1 || venv == "" || !under(calls[0][0], venv) {
		t.Errorf("a python {0} step ran %v, want the interpreter inside the venv %q", calls, venv)
	}
}

// ratchet: test_removed TestRun_NoVenvIsMadeWhenNoStepMentionsPython: a script such as ./ci.sh can pip install without any step naming python, so the venv is made whenever python is on PATH; TestRun_AScriptThatPipInstallsGetsAVenvEvenWhenNoStepNamesPython covers it
func TestRun_AScriptThatPipInstallsGetsAVenvEvenWhenNoStepNamesPython(t *testing.T) {
	boxBin, _ := fakeToolsOnPath(t)
	useFakePython(t, boxBin)
	sum, out, _ := runFlow(t, `
on: pull_request
jobs:
  build:
    steps:
      - run: echo the repo script installs its own tools, this step does not say how
`)
	if sum.Failed() {
		t.Fatalf("run failed:\n%s", out)
	}
	venv, ok := isolateValue(out, "VIRTUAL_ENV")
	if !ok {
		t.Fatalf("no venv was made for a script that may pip install:\n%s", out)
	}
	if got, _ := isolateValue(out, "PIP_REQUIRE_VIRTUALENV"); got != "true" {
		t.Errorf("PIP_REQUIRE_VIRTUALENV = %q, want true", got)
	}
	if _, err := os.Stat(venv); !os.IsNotExist(err) {
		t.Errorf("the venv %s outlived the run (stat err %v)", venv, err)
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

// ratchet: test_removed TestRun_AGlobalInstallIsRefusedNamingItsStepAndNothingAfterItRuns: a refused step is now listed as skipped, like a uses: step, not failed; TestRun_AGlobalInstallIsListedAsSkippedNamingItsStepAndTheRestRuns covers it
func TestRun_AGlobalInstallIsListedAsSkippedNamingItsStepAndTheRestRuns(t *testing.T) {
	sum, out, dir := runFlow(t, `
on: pull_request
jobs:
  sys:
    steps:
      - run: echo before >> log.txt
      - name: Install system libs
        run: sudo -n apt-get --version
      - name: Tolerated install
        continue-on-error: true
        run: sudo -n apt-get --version
      - run: echo after >> log.txt
`)
	r := result(t, sum, "sys")
	if r.Result != ResultSuccess || len(r.Refused) != 2 || r.Refused[0] != "Install system libs" || r.Refused[1] != "Tolerated install" {
		t.Errorf("sys = %+v, want success with both refused steps named, never a failure and never tolerated into a pass", r)
	}
	if got := sum.Refused(); len(got) != 2 || !strings.HasPrefix(got[0], "ci.yml: sys: Install system libs") {
		t.Errorf("Summary.Refused = %q, want each refusal with its workflow and job", got)
	}
	if sum.Failed() {
		t.Errorf("a refusal is not a failure:\n%s", out)
	}
	if !strings.Contains(out, "[skip] Install system libs") || !strings.Contains(out, "refused") || !strings.Contains(out, "sudo") {
		t.Errorf("the skip must name the step, say it was refused and why:\n%s", out)
	}
	if got := readFile(t, filepath.Join(dir, "log.txt")); got != "before\nafter\n" {
		t.Errorf("log = %q, want the steps around the refused ones to run", got)
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
          for v in GOPATH GOMODCACHE GOBIN CARGO_HOME npm_config_prefix npm_config_cache PIP_CACHE_DIR PIPX_HOME PIPX_BIN_DIR UV_TOOL_DIR UV_TOOL_BIN_DIR UV_PYTHON_INSTALL_DIR UV_CACHE_DIR RUSTUP_HOME; do
            eval "echo $v=\$$v" >> seen.txt
          done
          echo "PATH=$PATH" >> seen.txt
`, func(o *Options) {
		o.Env = append(os.Environ(), "GOPATH="+host, "CARGO_HOME="+host, "npm_config_cache="+host, "PIPX_HOME="+host, "UV_CACHE_DIR="+host, "RUSTUP_HOME="+host)
	})
	if sum.Failed() {
		t.Fatalf("run failed:\n%s", out)
	}
	seen := map[string]string{}
	for _, l := range strings.Split(readFile(t, filepath.Join(dir, "seen.txt")), "\n") {
		k, v, _ := strings.Cut(l, "=")
		seen[k] = v
	}
	for _, key := range []string{"GOPATH", "GOMODCACHE", "GOBIN", "CARGO_HOME", "npm_config_prefix", "npm_config_cache", "PIP_CACHE_DIR", "PIPX_HOME", "PIPX_BIN_DIR", "UV_TOOL_DIR", "UV_TOOL_BIN_DIR", "UV_PYTHON_INSTALL_DIR", "UV_CACHE_DIR", "RUSTUP_HOME"} {
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
	for _, bin := range []string{"/isolation/npm-prefix", "/isolation/gopath/bin", "/isolation/cargo-home/bin", "/isolation/pipx-bin", "/isolation/uv-tool-bin"} {
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

func TestRun_RustupGetsTheBoxsToolchainsLinkedAndSettingsCopiedButNewOnesLandInTheScratch(t *testing.T) {
	host := t.TempDir()
	toolchain := filepath.Join(host, "toolchains", "stable-host")
	writeTo(t, filepath.Join(toolchain, "bin", "rustc"), "compiler")
	writeTo(t, filepath.Join(host, "settings.toml"), "default_toolchain = \"stable-host\"\n")
	sum, out, dir := runFlow(t, `
on: pull_request
jobs:
  r:
    steps:
      - run: |
          cat "$RUSTUP_HOME/toolchains/stable-host/bin/rustc" >> seen.txt
          cat "$RUSTUP_HOME/settings.toml" >> seen.txt
          mkdir -p "$RUSTUP_HOME/toolchains/nightly-new/bin"
          echo downloaded > "$RUSTUP_HOME/toolchains/nightly-new/bin/rustc"
`, func(o *Options) { o.Env = append(os.Environ(), "RUSTUP_HOME="+host) })
	if sum.Failed() {
		t.Fatalf("run failed:\n%s", out)
	}
	if got := readFile(t, filepath.Join(dir, "seen.txt")); got != "compilerdefault_toolchain = \"stable-host\"\n" {
		t.Errorf("the step saw %q, want the box's toolchain through the link and its settings copied", got)
	}
	if home, _ := isolateValue(out, "RUSTUP_HOME"); under(home, host) || !strings.Contains(filepath.ToSlash(home), "/isolation/") {
		t.Errorf("RUSTUP_HOME = %s, want a directory of the run's own, not the box's %s", home, host)
	}
	if _, err := os.Stat(filepath.Join(host, "toolchains", "nightly-new")); !os.IsNotExist(err) {
		t.Errorf("a toolchain the run installed landed in the box's rustup home (stat err %v)", err)
	}
	if got := readFile(t, filepath.Join(toolchain, "bin", "rustc")); got != "compiler" {
		t.Errorf("the box's toolchain changed: %q", got)
	}
	if !strings.Contains(out, "rustup:") {
		t.Errorf("what was linked must be said:\n%s", out)
	}
}
