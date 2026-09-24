package suite

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/lock"
)

// TestCargoBuildOnlyArgv_DropsTheRunFlagsNextestRefusesBesideNoRun is issue
// #798: the build half of a build-then-run split kept every flag the typed
// run carried and appended --no-run, and nextest refuses --no-run beside
// --no-fail-fast, --fail-fast, --max-fail, --debugger and --tracer ("the
// argument '--no-run' cannot be used with '--no-fail-fast'"). A sanctioned
// mutation proof, and the prover's own scoped run, died on that argument
// error before anything compiled. The flags only steer a run, so the build
// form drops them, in both spellings and with a separate value, and leaves
// everything that decides what gets compiled.
func TestCargoBuildOnlyArgv_DropsTheRunFlagsNextestRefusesBesideNoRun(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "the hand proof from the issue",
			in:   []string{"nextest", "run", "-p", "forge_solver", "--lib", "--no-fail-fast", "-E", "test(/friction/)"},
			want: []string{"nextest", "run", "-p", "forge_solver", "--lib", "-E", "test(/friction/)", "--no-run"},
		},
		{
			name: "fail-fast and a separate max-fail value",
			in:   []string{"cargo", "nextest", "run", "--fail-fast", "--max-fail", "3", "-p", "a"},
			want: []string{"cargo", "nextest", "run", "-p", "a", "--no-run"},
		},
		{
			name: "joined values for max-fail, debugger and tracer",
			in:   []string{"nextest", "run", "--max-fail=2", "--debugger=gdb", "--tracer=strace", "--release"},
			want: []string{"nextest", "run", "--release", "--no-run"},
		},
		{
			name: "separate debugger and tracer values",
			in:   []string{"nextest", "run", "--debugger", "gdb", "--tracer", "strace", "-p", "a"},
			want: []string{"nextest", "run", "-p", "a", "--no-run"},
		},
		{
			name: "an empty argv still builds only",
			in:   nil,
			want: []string{"--no-run"},
		},
		{
			name: "harness arguments past the separator never reach the build",
			in:   []string{"test", "-p", "forge", "--test", "integration", "--", "probe", "--ignored"},
			want: []string{"test", "-p", "forge", "--test", "integration", "--no-run"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CargoBuildOnlyArgv(c.in); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("CargoBuildOnlyArgv(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// TestCargoBuildOnlyArgv_RealNextestCompilesTheNoFailFastProof runs the
// build form of the issue's own command against a real crate: nextest is the
// authority on which flags it refuses beside --no-run, and the table above
// only restates what this box's nextest answered.
func TestCargoBuildOnlyArgv_RealNextestCompilesTheNoFailFastProof(t *testing.T) {
	tddtest.UseRealCargoHome(t)
	for _, bin := range []string{"cargo", "cargo-nextest", "rustc"} {
		if _, err := exec.LookPath(bin); err != nil {
			// skip-ok: an environment probe, not a disabled assertion — the test asserts for real wherever the toolchain is installed.
			t.Skipf("%s not on PATH; skipping the real-nextest build-form test", bin)
		}
	}
	root := t.TempDir()
	tddtest.Write(t, root, "Cargo.toml", "[package]\nname = \"friction\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	tddtest.Write(t, root, "src/lib.rs", "#[cfg(test)]\nmod friction {\n    #[test]\n    fn holds() { assert_eq!(2 + 2, 4); }\n}\n")

	argv := CargoBuildOnlyArgv([]string{"nextest", "run", "--lib", "--no-fail-fast", "--max-fail", "1", "-E", "test(/friction/)"})
	cmd := exec.Command("cargo", argv...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), lock.BuildLockHeldEnv+"=1", "CARGO_TARGET_DIR="+filepath.Join(root, "target"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cargo %q: %v\n%s", argv, err, out)
	}
}
