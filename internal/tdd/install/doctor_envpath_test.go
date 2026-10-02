package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A healthy install (see healthyInstall) writes env.PATH with the shim dir
// first — TestDoctor_HealthyInstallPassesEveryCheck already proves this check
// passes there. This file covers the two ways the snapshot goes stale or
// never existed.

// A box this feature predates (settings.json installed, but no env.PATH
// entry ever written) reports the check as not applicable, not a failure —
// nothing here is broken, there is simply nothing yet to judge.
func TestDoctorEnvPath_NotApplicableWhenNoEnvPATH(t *testing.T) {
	in := healthyInstall(t)
	path := filepath.Join(in.ConfigDir, "settings.json")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture settings.json: %v", err)
	}
	root, err := parseSettings(existing)
	if err != nil {
		t.Fatalf("parseSettings: %v", err)
	}
	delete(root, "env")
	out, err := marshalSettings(root)
	if err != nil {
		t.Fatalf("marshalSettings: %v", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("writing fixture settings.json: %v", err)
	}
	if _, ok := doctorEnvPath(in); ok {
		t.Fatal("expected doctorEnvPath to report not-applicable with no env.PATH key")
	}
}

// A box whose PATH changed since the last install (a new shell-profile line,
// a machine-wide toolchain install) leaves env.PATH's snapshot stale: the
// shim dir is still IN the value, but no longer first. WARN, not FAIL, and
// name the fix.
func TestDoctorEnvPath_WarnsWhenShimDirNoLongerFirst(t *testing.T) {
	in := healthyInstall(t)
	// A machine-wide toolchain install landed ahead of the shim dir: the
	// shim dir is still IN the value, but a new entry now precedes it.
	path := filepath.Join(in.ConfigDir, "settings.json")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture settings.json: %v", err)
	}
	stale := BuildEnvPath("/usr/local/bin", []string{in.ShimDir, "/usr/bin"}, ":")
	out, changed, err := PatchSettingsEnvPath(existing, stale)
	if err != nil {
		t.Fatalf("PatchSettingsEnvPath: %v", err)
	}
	if !changed {
		t.Fatal("expected the stale rewrite to change settings.json")
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("writing stale settings.json: %v", err)
	}

	c, ok := doctorEnvPath(in)
	if !ok {
		t.Fatal("expected doctorEnvPath to apply once env.PATH is set")
	}
	if c.OK {
		t.Fatal("a stale env.PATH (shim dir present but not first) must not pass")
	}
	if !c.Warn {
		t.Error("a stale env.PATH is a WARNING, not a hard failure")
	}
	for _, want := range []string{"queue shim dir", in.ShimDir, "aphrollo install"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail = %q, want it to contain %q", c.Detail, want)
		}
	}
}

// A healthy env.PATH (shim dir first) passes outright.
func TestDoctorEnvPath_OKWhenShimDirFirst(t *testing.T) {
	in := healthyInstall(t)
	c, ok := doctorEnvPath(in)
	if !ok {
		t.Fatal("expected doctorEnvPath to apply on a healthy install")
	}
	if !c.OK {
		t.Errorf("expected ok, got: %s", c.Detail)
	}
}

// A Windows env.PATH is joined with ";", not ":" — a check that split it the
// POSIX way would cut every entry at its drive letter (C:\...) and never
// find the shim dir first even on a healthy install. Pinned on either host
// via hookGOOSFn, the same platform seam doctorForeignHooks already reads.
func TestDoctorEnvPath_SplitsOnSemicolonOnWindows(t *testing.T) {
	prev := hookGOOSFn
	hookGOOSFn = func() string { return "windows" }
	t.Cleanup(func() { hookGOOSFn = prev })

	shim := `C:\Users\olive\bin\cargo-queue`
	in := DoctorInput{
		ConfigDir: t.TempDir(),
		ShimDir:   shim,
	}
	value := BuildEnvPath(shim, []string{`C:\Windows\System32`, `C:\Windows`}, ";")
	out, _, err := PatchSettingsEnvPath(nil, value)
	if err != nil {
		t.Fatalf("PatchSettingsEnvPath: %v", err)
	}
	if err := os.WriteFile(filepath.Join(in.ConfigDir, "settings.json"), out, 0o644); err != nil {
		t.Fatal(err)
	}

	c, ok := doctorEnvPath(in)
	if !ok {
		t.Fatal("expected doctorEnvPath to apply")
	}
	if !c.OK {
		t.Errorf("a healthy Windows env.PATH must pass when split on ';': %s", c.Detail)
	}
}

// A user PATH that carries the shim dir twice and lacks the binary dir is a
// WARN naming both defects; a converged one is OK; no registry read at all
// (non-Windows) is not applicable.
func TestDoctorUserPath_WarnsOnDuplicateAndMissing(t *testing.T) {
	in := healthyInstall(t)
	in.Bin = `C:\Users\me\bin\aphrollo.exe`
	in.ShimDir = `C:\Users\me\bin\cargo-queue`
	in.UserPathDirs = []string{in.ShimDir, `C:\Tools`, in.ShimDir}
	c, ok := doctorUserPath(in)
	if !ok || !c.Warn || c.OK {
		t.Fatalf("want a warning, got ok=%v %+v", ok, c)
	}
	for _, want := range []string{"appears 2 times", `C:\Users\me\bin is missing`, "aphrollo install"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q lacks %q", c.Detail, want)
		}
	}

	in.UserPathDirs = []string{in.ShimDir, `C:\Users\me\bin`, `C:\Tools`}
	if c, ok := doctorUserPath(in); !ok || !c.OK {
		t.Fatalf("converged PATH must be OK, got ok=%v %+v", ok, c)
	}

	in.UserPathDirs = nil
	if _, ok := doctorUserPath(in); ok {
		t.Fatal("no user PATH read must be not-applicable")
	}
}

// writeAgentPath sets the settings.json env.PATH of a healthy install to the
// shim dir followed by dirs, the way an install wrote it.
func writeAgentPath(t *testing.T, in DoctorInput, dirs ...string) {
	t.Helper()
	path := filepath.Join(in.ConfigDir, "settings.json")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := PatchSettingsEnvPath(existing, BuildEnvPath(in.ShimDir, dirs, envPathSep(hookGOOSFn())))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// putTool lays an executable called name in dir.
func putTool(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".exe"), []byte("tool"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// repoWithManifest is a git repo that carries one file at manifest.
func repoWithManifest(t *testing.T, manifest string) string {
	t.Helper()
	repo := t.TempDir()
	gitInit(t, repo)
	mustWrite(t, filepath.Join(repo, filepath.FromSlash(manifest)), "{}\n")
	return repo
}

// The hook's PATH is the literal env.PATH value: a Go suite whose go is not on
// it cannot start there, and every edit then reports a run that never
// happened. Each toolchain a repo's suites run under is judged on its own.
func TestDoctorEnvPath_FailsWhenAToolTheRepoSuitesNeedIsMissing(t *testing.T) {
	cases := []struct {
		manifest string
		root     string
		need     string
		binaries []string
	}{
		{"go.mod", ".", "go", []string{"go"}},
		{"crates/core/Cargo.toml", "crates/core", "cargo", []string{"cargo"}},
		{"web/package.json", "web", "node", []string{"node"}},
		{"py/pyproject.toml", "py", "python", []string{"python3", "python"}},
	}
	for _, tc := range cases {
		in := healthyInstall(t)
		in.Repo = repoWithManifest(t, tc.manifest)
		bin := t.TempDir()
		writeAgentPath(t, in, bin)

		c, ok := doctorEnvPath(in)
		if !ok || c.OK || c.Warn {
			t.Fatalf("%s: env.PATH without %s: ok=%v %+v, want a failure", tc.manifest, tc.need, ok, c)
		}
		for _, want := range []string{tc.need, tc.root, "aphrollo install"} {
			if !strings.Contains(c.Detail, want) {
				t.Errorf("%s: detail %q lacks %q", tc.manifest, c.Detail, want)
			}
		}

		for _, name := range tc.binaries {
			dir := t.TempDir()
			putTool(t, dir, name)
			writeAgentPath(t, in, dir)
			if c, ok := doctorEnvPath(in); !ok || !c.OK {
				t.Errorf("%s: env.PATH with %s on it: ok=%v %+v, want ok", tc.manifest, name, ok, c)
			}
		}
	}
}

// The queue shim dir holds a `cargo` of its own, which only queues and then
// runs the real one found further along the PATH: it is no cargo toolchain.
func TestDoctorEnvPath_TheQueueShimIsNoCargo(t *testing.T) {
	in := healthyInstall(t)
	in.Repo = repoWithManifest(t, "Cargo.toml")
	if !onAnyDir([]string{in.ShimDir}, "cargo") {
		t.Fatal("fixture: the shim dir of a healthy install should hold a cargo shim")
	}
	writeAgentPath(t, in, t.TempDir())

	if c, ok := doctorEnvPath(in); !ok || c.OK || !strings.Contains(c.Detail, "cargo") {
		t.Fatalf("the cargo shim alone must not satisfy a cargo root: ok=%v %+v", ok, c)
	}
}

// A directory with both a go.mod and a package.json runs the Go suite alone:
// the gate picks one runner per root, so node is not a need of that root.
func TestDoctorEnvPath_JudgesOnlyTheRunnerARootActuallyUses(t *testing.T) {
	in := healthyInstall(t)
	in.Repo = repoWithManifest(t, "go.mod")
	mustWrite(t, filepath.Join(in.Repo, "package.json"), "{}\n")
	bin := t.TempDir()
	putTool(t, bin, "go")
	writeAgentPath(t, in, bin)

	if c, ok := doctorEnvPath(in); !ok || !c.OK {
		t.Fatalf("go on env.PATH is all a go.mod root needs: ok=%v %+v", ok, c)
	}
}

// A pytest root with its own virtualenv runs that interpreter, never the one
// on PATH, so a PATH with no python is no failure there.
func TestDoctorEnvPath_APytestRootWithItsOwnVenvNeedsNoPathPython(t *testing.T) {
	in := healthyInstall(t)
	in.Repo = repoWithManifest(t, "api/pyproject.toml")
	venvBin := filepath.Join(in.Repo, "api", ".venv", "bin")
	if err := os.MkdirAll(venvBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(venvBin, "python"), []byte("python"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAgentPath(t, in, t.TempDir())

	if c, ok := doctorEnvPath(in); !ok || !c.OK {
		t.Fatalf("a root with .venv/bin/python needs no PATH python: ok=%v %+v", ok, c)
	}
}

// Several missing tools are all named in the one finding.
func TestDoctorEnvPath_NamesEveryMissingTool(t *testing.T) {
	in := healthyInstall(t)
	in.Repo = repoWithManifest(t, "go.mod")
	mustWrite(t, filepath.Join(in.Repo, "web", "package.json"), "{}\n")
	writeAgentPath(t, in, t.TempDir())

	c, _ := doctorEnvPath(in)
	for _, want := range []string{"go", "node", "web"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q lacks %q", c.Detail, want)
		}
	}
}
