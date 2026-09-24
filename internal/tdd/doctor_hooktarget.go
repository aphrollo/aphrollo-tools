package tdd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A managed hook is two things: a shim in the dir core.hooksPath names, and
// the binary that shim execs. doctorGitHooksPath judged the first half and
// nothing judged the second, so a box whose hooks all pointed at a path that
// was never there read as a healthy install (issue #681). The shim itself
// says the binary is missing — to stderr, at the moment git runs it, in the
// middle of whatever the operator was doing — and that is the only place it
// was ever said. A report nobody is looking at when it prints is not a way
// to LEARN the state; this is.

// doctorGitHookBinary checks that the managed shims in the hooks dir exec a
// binary this box can actually spawn. Silent when there is no managed hooks
// dir to read: unset, missing, or holding nothing of ours are all
// doctorGitHooksPath's finding, and a second failure about one fact reads
// like two problems.
func doctorGitHookBinary(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "git hook binary", OK: true}
	if in.GitHooksPath == "" {
		return c
	}
	var broken []string
	for _, bin := range hookTargets(in.GitHooksPath) {
		if err := BinIsRunnable(fromShellPath(bin.path)); err != nil {
			broken = append(broken, fmt.Sprintf("%s (run by %s): %v", bin.path, strings.Join(bin.hooks, ", "), err))
		}
	}
	if len(broken) == 0 {
		return c
	}
	c.OK = false
	c.Detail = fmt.Sprintf("%s — git runs the shim, the shim cannot run the binary, and every commit on this box is UNGATED with nothing else reporting it; "+
		"run `aphrollo install --bin <the installed aphrollo binary>`", strings.Join(broken, "; "))
	return c
}

// hookTarget is one binary path the managed shims name, with the hooks that
// name it. Grouped so a report about the usual case — every hook pointing at
// the same wrong path — is one finding rather than five.
type hookTarget struct {
	path  string
	hooks []string
}

// hookTargets reads the managed shims in dir and returns the binaries they
// exec, in the fixed order gitGateHooks declares so two runs read the same
// way. A hook that is absent, unreadable, or not ours is skipped:
// doctorGitHooksPath and doctorForeignHooks own those findings.
func hookTargets(dir string) []hookTarget {
	var out []hookTarget
	index := map[string]int{}
	for _, h := range gitGateHooks {
		data, err := os.ReadFile(filepath.Join(dir, h.Name))
		if err != nil {
			continue
		}
		bin := hookShimBin(string(data))
		if bin == "" {
			continue
		}
		if i, seen := index[bin]; seen {
			out[i].hooks = append(out[i].hooks, h.Name)
			continue
		}
		index[bin] = len(out)
		out = append(out, hookTarget{path: bin, hooks: []string{h.Name}})
	}
	return out
}

// hookShimBin returns the binary a managed shim execs, or "" when the text is
// not a shim this tool wrote. The LAST exec line is the answer: a queue shim
// carries an earlier `exec` for the real tool it falls back to when the
// binary is gone, and that fallback is not the path being judged here.
func hookShimBin(text string) string {
	if !strings.Contains(text, installMarker) {
		return ""
	}
	bin := ""
	for line := range strings.Lines(text) {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), `exec "`)
		if !ok {
			continue
		}
		if path, _, ok := strings.Cut(rest, `"`); ok {
			bin = path
		}
	}
	return bin
}

// BinIsRunnable reports whether path is something this box would spawn: a
// regular file, and one exec.LookPath accepts — the executable bit off
// Windows, a PATHEXT-listed extension on it. One definition, because the two
// callers must agree byte for byte on what "runnable" means: `gate init`
// asks it of --bin before it writes a hook, and doctor asks it of the path
// the installed hooks already carry. A box that installs on one answer and
// is judged on another can be told its hooks are fine while they are not,
// which is the whole of issue #681.
func BinIsRunnable(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if _, err := exec.LookPath(path); err != nil {
		return err
	}
	return nil
}
