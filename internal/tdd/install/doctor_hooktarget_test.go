package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repointHooks rewrites the managed shims in the fixture's hooks dir at a
// different binary — the state a `gate init --bin <typo>` used to leave
// behind, built here rather than by breaking the box that runs the suite.
func repointHooks(t *testing.T, hooksDir, bin string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte(binShim(bin, "precommit", "")), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Issue #681: `gate init` accepted a --bin that did not exist and wrote the
// global hooks at it. Every hook then exits 127 and nothing on the box says
// the gate is gone — doctor judged that the hooks dir existed and carried
// this tool's shims, which it did, and never asked whether the binary they
// exec is there.
func TestDoctor_ReportsAGitHookRunningABinaryThatIsNotThere(t *testing.T) {
	in := healthyInstall(t)
	missing := filepath.Join(t.TempDir(), "aphrollo")
	repointHooks(t, in.GitHooksPath, missing)

	c := check(t, Doctor(in), "git hook binary")
	if c.OK {
		t.Fatalf("hooks pointing at a binary that does not exist must be a finding, got ok: %s", c.Detail)
	}
	// Named in the shim's own spelling — forward slashes, the way the hook
	// file carries it — so the operator can find the string they are being
	// told about by opening the hook.
	for _, want := range []string{filepath.ToSlash(missing), "pre-commit"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail = %q, want it to carry %q", c.Detail, want)
		}
	}
	// The consequence, in the operator's words: not "the path is wrong" but
	// what the wrong path costs them. Case is the report's business.
	if !strings.Contains(strings.ToLower(c.Detail), "ungated") {
		t.Errorf("detail = %q, want it to say the box is ungated", c.Detail)
	}
	if !strings.Contains(c.Detail, "aphrollo install") {
		t.Errorf("detail = %q, want the remedy that repoints the hooks", c.Detail)
	}
}

// The healthy install: the shims name the binary that is actually there, and
// the row says so instead of adding noise to every clean report.
func TestDoctor_GitHookBinaryPassesWhenTheShimsNameTheInstalledBinary(t *testing.T) {
	in := healthyInstall(t)

	if c := check(t, Doctor(in), "git hook binary"); !c.OK {
		t.Fatalf("a healthy install must pass this check, got: %s", c.Detail)
	}
}

// A file that is there but cannot be spawned fails the same way for the same
// reason: the hook execs it and gets nothing back. The fixture is the shape
// BOTH platforms refuse — no extension and no executable bit — because
// `which aphrollo` printing the extensionless form is how issue #681 was hit
// in the first place, and a shim carrying that spelling is not runnable on
// the box it was written for.
func TestDoctor_ReportsAGitHookBinaryTheBoxCannotSpawn(t *testing.T) {
	in := healthyInstall(t)
	notRunnable := filepath.Join(t.TempDir(), "aphrollo")
	if err := os.WriteFile(notRunnable, []byte("APHROLLO"), 0o644); err != nil {
		t.Fatal(err)
	}
	repointHooks(t, in.GitHooksPath, notRunnable)

	c := check(t, Doctor(in), "git hook binary")
	if c.OK {
		t.Fatalf("hooks pointing at a file the box cannot spawn must be a finding, got ok: %s", c.Detail)
	}
	if want := filepath.ToSlash(notRunnable); !strings.Contains(c.Detail, want) {
		t.Errorf("detail = %q, want it to name %q", c.Detail, want)
	}
}

// An unset or missing core.hooksPath is doctorGitHooksPath's finding. A
// second row about the same fact is noise, and a FAILURE about it would make
// a box with no git gate at all report two problems where it has one.
func TestDoctor_GitHookBinaryIsSilentWithNoHooksDirToRead(t *testing.T) {
	for name, path := range map[string]string{
		"unset":   "",
		"missing": filepath.Join(t.TempDir(), "gone"),
		"empty":   t.TempDir(),
	} {
		t.Run(name, func(t *testing.T) {
			in := healthyInstall(t)
			in.GitHooksPath = path
			if c := check(t, Doctor(in), "git hook binary"); !c.OK {
				t.Errorf("%s hooks dir: want ok, got %s", name, c.Detail)
			}
		})
	}
}
