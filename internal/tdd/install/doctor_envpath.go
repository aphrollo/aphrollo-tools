package install

import (
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// doctorEnvPath checks that settings.json's env.PATH — the literal PATH
// snapshot the agent's own Bash tool actually resolves `git`/`cargo`
// through, since Claude Code writes an env block into the process
// environment rather than expanding a live "$PATH" — still starts with the
// queue shim dir. Unlike doctorShimPath, which reads THIS PROCESS's live
// PATH, env.PATH is a snapshot `aphrollo install` wrote once: a PATH change
// on the box afterward (a new shell-profile line, a machine-wide toolchain
// install) never touches it, so it can go stale with nothing else on this
// list noticing. WARN, not FAIL — the fix is a routine re-install, not a
// broken one — and ok=false means there is nothing to judge yet: no env key,
// or no PATH inside it, which is either a box this feature predates or one
// whose settings.json isn't installed at all (doctorHookBinary already
// reports that).
//
// The value is also the whole PATH every hook resolves a suite's command
// against, so a tool the repo's suites run under that is not on it fails the
// check: a hook that cannot start `go` has no verdict to give, and the
// session sees only that nothing ran.
func doctorEnvPath(in DoctorInput) (DoctorCheck, bool) {
	c := DoctorCheck{Name: "agent PATH (settings.json env.PATH)"}
	root, err := readSettings(in.ConfigDir)
	if err != nil {
		return c, false
	}
	env, _ := root["env"].(map[string]any)
	value, ok := env["PATH"].(string)
	if !ok || value == "" {
		return c, false
	}
	dirs := strings.Split(value, envPathSep(hookGOOSFn()))
	if missing := missingSuiteTools(in.Repo, dirs, in.ShimDir); missing != "" {
		c.Detail = fmt.Sprintf("env.PATH has no %s — a hook resolves a suite's command against exactly this value, "+
			"so those suites cannot start; run `aphrollo install` from a shell where they resolve", missing)
		return c, true
	}
	if len(dirs) == 0 || !samePath(dirs[0], in.ShimDir) {
		c.Warn = true
		c.Detail = fmt.Sprintf("no longer starts with the queue shim dir %s — the box's PATH changed since "+
			"the last install; run `aphrollo install` again", in.ShimDir)
		return c, true
	}
	c.OK = true
	return c, true
}

// envPathSep is the separator `aphrollo install` joins settings.json's
// env.PATH with, keyed off the same platform seam doctorForeignHooks already
// reads (hookGOOSFn) rather than a fresh runtime.GOOS check: Windows PATH
// values use ";", every other platform uses ":".
func envPathSep(goos string) string {
	if goos == "windows" {
		return ";"
	}
	return ":"
}

// doctorUserPath audits the user-scope PATH (HKCU\Environment on Windows,
// the one place a PowerShell, cmd or Git Bash session all inherit from): the
// shim dir and the binary dir each exactly once, the shim dir ahead of Git's.
// ok=false when no user PATH was read (not Windows, or the read failed).
func doctorUserPath(in DoctorInput) (DoctorCheck, bool) {
	c := DoctorCheck{Name: "user PATH (HKCU Environment)"}
	if len(in.UserPathDirs) == 0 {
		return c, false
	}
	binDir := in.LauncherDir
	if binDir == "" {
		binDir = winDir(in.Bin)
	}
	problems := AuditUserPath(in.UserPathDirs, in.ShimDir, binDir)
	if len(problems) > 0 {
		c.Warn = true
		c.Detail = strings.Join(problems, "; ") + " — run `aphrollo install`"
		return c, true
	}
	c.OK = true
	return c, true
}

// suiteBinaries are the executables, per runner command, any one of which
// lets that runner start: node serves npx and npm alike, and a pytest root
// runs under python3 or python.
var suiteBinaries = map[string][]string{
	"go":     {"go"},
	"cargo":  {"cargo"},
	"npx":    {"node"},
	"npm":    {"node"},
	"pytest": {"python3", "python"},
}

// missingSuiteTools names, for the repo's suite roots, every tool that is on
// none of the env.PATH directories dirs, with the roots that need it
// ("go (., backend); node or python (web)"). The queue shim dir does not
// count: its cargo only queues, then runs the real one found further along.
// "" when none is missing or the repo has no suite roots to judge.
func missingSuiteTools(repo string, dirs []string, shimDir string) string {
	dirs = slices.DeleteFunc(slices.Clone(dirs), func(dir string) bool { return samePath(dir, shimDir) })
	needs := map[string][]string{}
	for _, root := range repoSuiteRoots(repo) {
		r, ok := DetectRunner(filepath.Join(repo, root))
		binaries := suiteBinaries[r.Cmd]
		if !ok || len(binaries) == 0 || (r.Cmd == "pytest" && hasVenvPython(filepath.Join(repo, root))) {
			continue
		}
		if !slices.ContainsFunc(binaries, func(name string) bool { return onAnyDir(dirs, name) }) {
			label := strings.Join(binaries, " or ")
			needs[label] = append(needs[label], root)
		}
	}
	var parts []string
	for _, label := range slices.Sorted(maps.Keys(needs)) {
		parts = append(parts, fmt.Sprintf("%s (%s)", label, strings.Join(needs[label], ", ")))
	}
	return strings.Join(parts, "; ")
}

// repoSuiteRoots are the directories of the repo, relative to it and sorted,
// that carry a suite manifest at any depth, tracked or not yet committed. A
// dir git cannot list has none.
func repoSuiteRoots(repo string) []string {
	if repo == "" {
		return nil
	}
	// The manifests that mark a directory as a root some suite runs in.
	out, err := gitRead(repo, "ls-files", "--cached", "--others", "--exclude-standard", "--",
		":(glob)**/go.mod", ":(glob)**/Cargo.toml", ":(glob)**/pyproject.toml",
		":(glob)**/setup.py", ":(glob)**/pytest.ini", ":(glob)**/package.json")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for line := range strings.Lines(out) {
		seen[path.Dir(strings.TrimSpace(line))] = true
	}
	return slices.Sorted(maps.Keys(seen))
}

// onAnyDir reports whether an executable called name sits in one of dirs.
func onAnyDir(dirs []string, name string) bool {
	return slices.ContainsFunc(dirs, func(dir string) bool {
		return slices.ContainsFunc(toolNames(name), func(file string) bool {
			return isExecutableFile(filepath.Join(dir, file))
		})
	})
}

// hasVenvPython reports whether root carries its own virtualenv interpreter,
// the POSIX layout or the Windows one.
func hasVenvPython(root string) bool {
	for _, venv := range []string{".venv", "venv"} {
		for _, rel := range []string{filepath.Join("bin", "python"), filepath.Join("Scripts", "python.exe")} {
			if isExecutableFile(filepath.Join(root, venv, rel)) {
				return true
			}
		}
	}
	return false
}
