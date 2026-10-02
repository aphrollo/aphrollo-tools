package ghworkflow

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A GitHub runner is thrown away after a job; this box is not. A workflow's
// `pip install -r requirements.txt` that ran against the box's global python
// downgraded cryptography on a production host (#1102). So a run installs only
// into a directory of its own, removed when it ends: a venv first on PATH for
// python, and a prefix, cache or home for npm, go and cargo, each set last on
// every step so no env: block of a workflow points an install back out. What
// no variable can redirect (sudo, a system package manager) is refused before
// the step runs (isolate_refuse.go). Everything applied is printed.

// findPython is the interpreter a run's venv is made from: the first python3
// or python on PATH. A variable so a test never reaches the box's own.
var findPython = lookPython

// lookPython is findPython's real answer.
func lookPython() (string, bool) {
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, true
		}
	}
	return "", false
}

// makeVenv makes dir a venv of python. A variable so a test can watch it.
var makeVenv = func(ctx context.Context, python, dir string, env []string) error {
	ctx, cancel := context.WithTimeout(ctx, venvTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-m", "venv", dir)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// venvTimeout bounds making the venv: a stuck interpreter must not hold the run.
const venvTimeout = 2 * time.Minute

// isolation is what keeps one run's installs inside its own scratch.
type isolation struct {
	root   string
	vars   []KV     // set on every step, after the workflow's own env
	path   []string // directories put first on every step's PATH
	python string   // the venv's interpreter, "" when the run has no venv
	notes  []string // what was done that is not a variable, one line each
}

// newIsolation builds the run's isolation under scratch. base is the
// environment steps start from, so the venv is made the way a step would see
// it.
func newIsolation(ctx context.Context, scratch string, base []string) *isolation {
	s := &isolation{root: filepath.Join(scratch, "isolation")}
	s.setupPython(ctx, base)
	s.set("PIP_REQUIRE_VIRTUALENV", "true")
	s.dir("PIP_CACHE_DIR", "pip-cache")
	s.path = append(s.path, npmBinDir(s.dir("npm_config_prefix", "npm-prefix")))
	s.dir("npm_config_cache", "npm-cache")
	gopath := s.dir("GOPATH", "gopath")
	s.set("GOMODCACHE", filepath.Join(gopath, "pkg", "mod"))
	gobin := filepath.Join(gopath, "bin")
	s.set("GOBIN", gobin)
	s.path = append(s.path, gobin)
	cargo := s.dir("CARGO_HOME", "cargo-home")
	s.path = append(s.path, filepath.Join(cargo, "bin"))
	s.copyCargoConfig(base, cargo)
	return s
}

func (s *isolation) set(key, val string) { s.vars = append(s.vars, KV{key, val}) }

// dir sets key to a directory of the run's own and returns it.
func (s *isolation) dir(key, name string) string {
	d := filepath.Join(s.root, name)
	s.set(key, d)
	return d
}

func (s *isolation) note(format string, args ...any) {
	s.notes = append(s.notes, fmt.Sprintf(format, args...))
}

// setupPython makes the run's venv whenever python is on PATH: a script a step
// runs (./ci.sh) can pip install without any step naming python, so no scan of
// the steps decides it. Without a venv pip still cannot reach the box's python:
// PIP_REQUIRE_VIRTUALENV makes it refuse.
func (s *isolation) setupPython(ctx context.Context, base []string) {
	py, ok := findPython()
	if !ok {
		s.note("python: no python3 or python on PATH, so no venv was made")
		return
	}
	venv := filepath.Join(s.root, "venv")
	if err := makeVenv(ctx, py, venv, base); err != nil {
		s.note("python: a venv could not be made with %s (%v)", py, err)
		return
	}
	s.set("VIRTUAL_ENV", venv)
	s.path = append([]string{venvBinDir(venv)}, s.path...)
	s.python = venvPythonPath(venv)
	s.note("python: venv made with %s", py)
}

// copyCargoConfig gives the run's cargo home the box's cargo config, so a
// registry mirror, a linker or a proxy it declares still applies. Credentials
// stay behind.
func (s *isolation) copyCargoConfig(base []string, cargoHome string) {
	host := envIn(base, "CARGO_HOME")
	if host == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return // absence-ok: no home directory means no box config to copy
		}
		host = filepath.Join(home, ".cargo")
	}
	for _, name := range []string{"config.toml", "config"} {
		data, err := os.ReadFile(filepath.Join(host, name))
		if err != nil {
			continue
		}
		if err := os.MkdirAll(cargoHome, 0o755); err != nil {
			s.note("cargo: %s could not be copied (%v)", filepath.Join(host, name), err)
			return
		}
		if err := os.WriteFile(filepath.Join(cargoHome, name), data, 0o600); err != nil {
			s.note("cargo: %s could not be copied (%v)", filepath.Join(host, name), err)
			return
		}
		s.note("cargo: copied %s into CARGO_HOME", filepath.Join(host, name))
		return
	}
}

// apply is the environment a step runs with: env, then the isolation's
// variables and its directories first on PATH. A nil isolation changes nothing.
func (s *isolation) apply(env []string) []string {
	if s == nil {
		return env
	}
	out := append([]string{}, env...)
	for _, kv := range s.vars {
		out = append(out, kv.Key+"="+kv.Val)
	}
	return append(out, "PATH="+joinPath(s.path, pathIn(env)))
}

// describe prints everything the isolation did, so none of it is a surprise.
func (s *isolation) describe(out io.Writer) {
	fmt.Fprintf(out, "ci run: isolation: installs this run makes land under %s, which is removed when it ends\n", s.root)
	for _, n := range s.notes {
		fmt.Fprintf(out, "  [isolate] %s\n", n)
	}
	for _, kv := range s.vars {
		fmt.Fprintf(out, "  [isolate] %s=%s\n", kv.Key, kv.Val)
	}
	fmt.Fprintf(out, "  [isolate] PATH first: %s\n", strings.Join(s.path, string(os.PathListSeparator)))
	fmt.Fprintf(out, "  [isolate] refused before the step runs: sudo and other privilege changes, system package managers, pip --user and --break-system-packages, uv --system, yarn global, pnpm -g, gem install, corepack enable\n")
}

// joinPath is dirs followed by the rest of a PATH, with no stray separator
// when there is no rest (an empty entry means the current directory).
func joinPath(dirs []string, rest string) string {
	joined := strings.Join(dirs, string(os.PathListSeparator))
	if rest == "" {
		return joined
	}
	return joined + string(os.PathListSeparator) + rest
}

// envIn is the last value of a variable in an environment, "" when unset.
func envIn(env []string, key string) string {
	val := ""
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok && strings.EqualFold(k, key) {
			val = v
		}
	}
	return val
}

// pathIn is the PATH an environment holds.
func pathIn(env []string) string { return envIn(env, "PATH") }

// shellOf is the shell a step runs under: its own, its job's, its workflow's.
func (r *jobRun) shellOf(st *Step) string {
	return firstNonEmpty(st.Shell, r.job.Shell, r.wf.Shell)
}

// pythonCommand is the name of a python interpreter: python, python3, python3.12.
var pythonCommand = regexp.MustCompile(`^python[0-9.]*$`)

// isPythonShell reports whether a step's script is python, not shell: the
// shell is python, or a template such as `python -u {0}` that starts with it.
func isPythonShell(shell string) bool {
	fields := strings.Fields(shell)
	if len(fields) == 0 {
		return false
	}
	return pythonCommand.MatchString(strings.TrimSuffix(filepath.Base(fields[0]), ".exe"))
}

// refusal is why a step is not run, "" when it may run. A python script is not
// shell text, so the scan does not read it.
func (r *jobRun) refusal(st *Step, script string) string {
	if isPythonShell(r.shellOf(st)) {
		return ""
	}
	return refuseGlobal(script)
}

// interpreter is the argv of a step with its python replaced by the run's
// venv python, so a python script installs into the venv too.
func (s *isolation) interpreter(shell string, argv []string) []string {
	if s == nil || s.python == "" || !isPythonShell(shell) {
		return argv
	}
	return append([]string{s.python}, argv[1:]...)
}
