package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestCargoTestRunBuildArgs_KeepsTheSlotWhenTheRunWouldCompile pins the
// cases where releasing the slot after --no-run would let a compile happen
// outside it, or where cargo would refuse the compile form outright. A
// `cargo test` with no target flag runs the library's doctests, which
// rustdoc compiles at run time; --doc cannot be combined with --no-run at
// all; an invocation already carrying --no-run is a compile, not a run;
// nextest's list and archive build and never execute.
func TestCargoTestRunBuildArgs_KeepsTheSlotWhenTheRunWouldCompile(t *testing.T) {
	for _, args := range [][]string{
		{"test", "-p", "forge"},
		{"test", "-p", "forge", "--", "--ignored"},
		{"test", "--doc", "-p", "forge"},
		{"test", "--test", "integration", "--no-run"},
		{"nextest", "list", "-p", "forge"},
		{"nextest", "archive", "--archive-file", "a.tar.zst"},
		{"nextest", "run", "--no-run"},
		{"build", "--tests"},
	} {
		if got, split := cargoTestRunBuildArgs(args); split {
			t.Errorf("cargoTestRunBuildArgs(%v) splits into %v, want the slot kept for the whole call", args, got)
		}
	}
	// A target flag in its --flag=value form removes doctests just the same.
	got, split := cargoTestRunBuildArgs([]string{"test", "--test=integration", "--", "--ignored"})
	want := []string{"test", "--test=integration", "--no-run"}
	if !split || !reflect.DeepEqual(got, want) {
		t.Fatalf("cargoTestRunBuildArgs(--test=integration) = %v, %v; want %v, true", got, split, want)
	}
}

// TestRunCargoShim_TestRun_BuildsUnderSlotThenRunsSlotFree is issue #727: a
// ten-minute `--ignored` measurement run held a build slot for its whole
// runtime, so a merge gate in another lane queued behind a test binary that
// had finished compiling long before. The slot must cover the compile only:
// the same invocation with --no-run (everything from the first bare "--"
// dropped, those are the test harness's own arguments) under the slot, then
// the ORIGINAL argv with the slot free. Both runner shapes a soak uses are
// covered, the libtest form the issue quotes and nextest's --run-ignored,
// and so is `cargo bench`, whose runtime is the same executing-not-compiling
// shape. The box is given ONE global slot, so a run phase that kept the
// global slot while handing back only the target lock is caught too, and
// the run phase must carry no slot token to lend.
func TestRunCargoShim_TestRun_BuildsUnderSlotThenRunsSlotFree(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantBuild []string
	}{
		{
			name: "cargo test with an ignored measurement filter",
			args: []string{"test", "-p", "forge", "--release", "--test", "integration", "--",
				"tire_rig::tests::launch_step_probe", "--ignored", "--nocapture", "--test-threads=1"},
			wantBuild: []string{"test", "-p", "forge", "--release", "--test", "integration", "--no-run"},
		},
		{
			name:      "nextest run with --run-ignored",
			args:      []string{"nextest", "run", "-p", "forge", "--release", "--run-ignored", "only", "-E", "test(launch_step_probe)"},
			wantBuild: []string{"nextest", "run", "-p", "forge", "--release", "--run-ignored", "only", "-E", "test(launch_step_probe)", "--no-run"},
		},
		{
			name:      "cargo bench with criterion arguments",
			args:      []string{"bench", "-p", "movement", "--bench", "step", "--", "--save-baseline", "main"},
			wantBuild: []string{"bench", "-p", "movement", "--bench", "step", "--no-run"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withIsolatedCargoLock(t)
			t.Setenv("APHROLLO_BUILD_SLOTS", "1")
			envOut := filepath.Join(t.TempDir(), "child-env")
			t.Setenv("APHROLLO_TEST_STUB_ENV_OUT", envOut)
			cfg := cargoShimConfig{
				waitBudget:   time.Second,
				pollInterval: 20 * time.Millisecond,
				realCargo:    runVerbStub(t),
			}

			var calls [][]string
			var slotHeld []bool
			execCargoHookForTest = func(args []string) {
				calls = append(calls, append([]string{}, args...))
				_, release, ok := tdd.TryAcquireBuildSlot(shimTargetDir(), "cargo nextest run -p other-crate", "/some/other/repo")
				slotHeld = append(slotHeld, !ok)
				if ok {
					release()
				}
			}
			t.Cleanup(func() { execCargoHookForTest = nil })

			var stdout, stderr bytes.Buffer
			if code := runCargoShim(c.args, strings.NewReader(""), &stdout, &stderr, cfg); code != 0 {
				t.Fatalf("exit = %d, want 0, stderr=%s", code, stderr.String())
			}
			if len(calls) != 2 {
				t.Fatalf("want 2 cargo calls (build, then run), got %d: %+v", len(calls), calls)
			}
			if !reflect.DeepEqual(calls[0], c.wantBuild) {
				t.Fatalf("build call = %+v, want %+v", calls[0], c.wantBuild)
			}
			if !reflect.DeepEqual(calls[1], c.args) {
				t.Fatalf("run call = %+v, want the original argv %+v", calls[1], c.args)
			}
			if !slotHeld[0] {
				t.Fatal("the slot must be HELD while the test binaries compile")
			}
			if slotHeld[1] {
				t.Fatal("the slot must be FREE while the built tests run")
			}
			// Both phases share a verb, so the record left is the RUN phase's.
			if token, err := os.ReadFile(envOut + "." + c.args[0]); err != nil || len(token) != 0 {
				t.Fatalf("run phase slot token = %q (err %v), want none: the run holds no slot to lend", token, err)
			}
		})
	}
}
