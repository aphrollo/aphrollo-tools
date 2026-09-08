package tdd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The argv this binary builds had only ever been exercised through the exec
// seam, so every test agreed with the code about a command line the actual
// tool refuses: `--in-place` beside `--jobs` died before the first mutant on
// the first real pre-merge measurement a Cargo consumer ever ran (issue
// #592). This test spends the minutes a real run costs to close that gap —
// one tiny crate, real cargo-mutants, real nextest — so the flag set is
// proved against the tool rather than against the seam.
//
// It skips when the toolchain is not on the box, the same way the LSP e2e
// tests skip when their server is not installed.
func TestMeasureLane_RealCargoMutantsOnAMinimalCrate(t *testing.T) {
	useRealCargoHome(t)
	requireRealMutationToolchain(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// What is under test is the command line, not the drive it runs on: a
	// box below the free-space budget would refuse before spawning anything
	// and prove nothing either way.
	t.Cleanup(SetFreeSpaceForTest(999, true))

	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	// The profile the runner selects with NEXTEST_PROFILE. It inherits every
	// default; it exists so the measured run reaches the same profile a
	// consuming repo declares for it.
	write(t, root, ".config/nextest.toml", "[profile.mutants]\n")
	write(t, root, "src/lib.rs", minimalCrateBase)
	// The repo's own post-run hook, which the measurement must reach with
	// the verdict in APHROLLO_MUTANTS_STATUS.
	write(t, root, "after.sh", "#!/usr/bin/env bash\nprintf '%s' \"$APHROLLO_MUTANTS_STATUS\" > after-ran.txt\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	base := gitOutT(t, root, "rev-parse", "HEAD")
	// The lane: one more function, with the test that catches every mutant
	// cargo-mutants can make of it. 7 - 2 = 5 differs from 7 + 2, 7 * 2,
	// 7 / 2, 7 % 2 and from the constants 0, 1 and -1, so a run that
	// measures this diff honestly refuses nothing.
	write(t, root, "src/lib.rs", minimalCrateLane)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane")

	var log strings.Builder
	v, err := MeasureLane(root, MutantsConfig{AtMerge: true, After: "after.sh"}, MeasureOpts{Base: base, Log: &log})

	if err != nil {
		t.Fatalf("MeasureLane: %v\n%s", err, log.String())
	}
	// The exact shape of #592: cargo-mutants rejecting the command line
	// before it measures anything.
	if strings.Contains(log.String(), "error: the argument") {
		t.Fatalf("cargo-mutants refused the argv this binary built:\n%s", log.String())
	}
	if v.Refused {
		t.Fatalf("verdict = %+v, want a clean run of a lane whose every mutant its test catches\n%s", v, log.String())
	}
	if v.Tested < 1 {
		t.Fatalf("Tested = %d, want at least one mutant actually built and run\n%s", v.Tested, log.String())
	}
	if got := readFileString(t, filepath.Join(root, "after-ran.txt")); got != "0" {
		t.Errorf("mutants-after recorded %q, want the passing run's own status 0", got)
	}
	// Under -v: what the real tool actually said. A green e2e test that
	// keeps the tool's own narrative to itself leaves nobody able to check
	// which flags ran, or how many mutants there were to catch.
	t.Logf("cargo-mutants said:\n%s", strings.TrimRight(log.String(), "\n"))
}

// minimalCrateBase is the crate before the lane: one function and the test
// that pins it.
const minimalCrateBase = `pub fn add(a: i32, b: i32) -> i32 {
    a + b
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn add_adds() {
        assert_eq!(add(2, 2), 4);
    }
}
`

// minimalCrateLane is the same crate with the lane's own change: the function
// the measurement's diff will name, and the test that catches its mutants.
const minimalCrateLane = `pub fn add(a: i32, b: i32) -> i32 {
    a + b
}

pub fn sub(a: i32, b: i32) -> i32 {
    a - b
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn add_adds() {
        assert_eq!(add(2, 2), 4);
    }

    #[test]
    fn sub_subtracts() {
        assert_eq!(sub(7, 2), 5);
    }
}
`

// realCargoHome is what CARGO_HOME held before TestMain pointed it at a temp
// directory, and whether it was set at all. Only the smoke test reads them:
// every other test in the package wants the isolated one.
var (
	realCargoHome    string
	hadRealCargoHome bool
)

// useRealCargoHome puts the box's own CARGO_HOME back for one test. The whole
// package runs under an isolated cargo home so no test reads the operator's
// ~/.cargo/config.toml, but the cargo the queue shim resolves lives under
// CARGO_HOME: with the isolated one in the environment every `cargo` this
// test spawns fails with "resolve cargo: <tmp>\bin\cargo.exe not found", and
// the test would skip on a box that has the whole toolchain installed.
func useRealCargoHome(t *testing.T) {
	t.Helper()
	if hadRealCargoHome {
		t.Setenv("CARGO_HOME", realCargoHome)
		return
	}
	isolated := os.Getenv("CARGO_HOME")
	if err := os.Unsetenv("CARGO_HOME"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Setenv("CARGO_HOME", isolated) })
}

// requireRealMutationToolchain skips unless this box can actually run the
// measurement. On PATH is not the same as usable, so each tool is asked for
// its own version: a binary that cannot answer that cannot measure a crate
// either, and that is an environment fact rather than something this test can
// assert about.
func requireRealMutationToolchain(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"cargo", "bash"} {
		if _, err := exec.LookPath(bin); err != nil {
			// skip-ok: an environment probe, not a disabled assertion — the test asserts for real wherever the toolchain is installed.
			t.Skipf("%s not on PATH; skipping the real cargo-mutants smoke test", bin)
		}
	}
	for _, sub := range []string{"mutants", "nextest"} {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		probe := exec.CommandContext(ctx, "cargo", sub, "--version")
		// The cargo on PATH may be the build queue's shim, which refuses a
		// bare `cargo mutants` and tells the caller to type the gate verb
		// instead. The probe carries the same marker the measurement's own
		// environment sets, so it asks the real subcommand.
		probe.Env = append(os.Environ(), MutationGateEnv+"="+MutationGateMarked)
		err := probe.Run()
		cancel()
		if err != nil {
			// skip-ok: an environment probe, not a disabled assertion — the test asserts for real wherever the toolchain is installed.
			t.Skipf("cargo %s --version is not runnable (%v); skipping the real cargo-mutants smoke test", sub, err)
		}
	}
}
