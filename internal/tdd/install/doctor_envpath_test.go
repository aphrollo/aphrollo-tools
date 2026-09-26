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
