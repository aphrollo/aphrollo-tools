package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestIsCargoReadOnlyVerb_ClassifiesTheQueryVerbs pins WHICH verbs skip the
// build slots: the ones that read the manifest/lockfile and compile nothing.
// `check` and `clippy` are the two that look read-only and are not — they
// run the compiler front end into the shared target dir, which is exactly
// what the slots govern.
func TestIsCargoReadOnlyVerb_ClassifiesTheQueryVerbs(t *testing.T) {
	readOnly := [][]string{
		{"metadata", "--format-version", "1"},
		{"tree", "-e", "features", "-i", "bevy_ecs"},
		{"fmt", "--all"},
		{"locate-project"},
		{"pkgid"},
		{"read-manifest"},
		{"--version"},
		{"-V"},
		{"--color=always", "metadata"},
	}
	for _, args := range readOnly {
		if !isCargoReadOnlyVerb(args) {
			t.Errorf("cargo %s must bypass the build slots", strings.Join(args, " "))
		}
	}
	compiling := [][]string{
		{"check"},
		{"clippy", "-p", "server"},
		{"build", "-p", "server"},
		{"test"},
		{"nextest", "run"},
		{"run", "-p", "client"},
		{"bench"},
		// The test binary's own --version is not cargo's.
		{"test", "-p", "shared", "--", "--version"},
	}
	for _, args := range compiling {
		if isCargoReadOnlyVerb(args) {
			t.Errorf("cargo %s compiles into the shared target — it must take a slot", strings.Join(args, " "))
		}
	}
}

// TestRunCargoShim_ReadOnlyVerbRunsWhileSlotsAreBusy pins the behaviour that
// matters at the terminal: `cargo metadata` (or `--version`, which every
// tool-detection path calls) must answer INSTANTLY even while a multi-minute
// build owns every slot, printing no queued line and never waiting.
func TestRunCargoShim_ReadOnlyVerbRunsWhileSlotsAreBusy(t *testing.T) {
	withIsolatedCargoLock(t)

	_, release, ok := tdd.TryAcquireBuildSlot(shimTargetDir(), "cargo nextest run -p other-crate", "/some/other/repo")
	if !ok {
		t.Fatal("setup: must be able to take the isolated slot")
	}
	defer release()

	cfg := cargoShimConfig{
		waitBudget:   10 * time.Second,
		pollInterval: 20 * time.Millisecond,
		realCargo:    runVerbStub(t),
	}
	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := runCargoShim([]string{"metadata"}, strings.NewReader(""), &stdout, &stderr, cfg)
	elapsed := time.Since(start)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 — a read-only verb must run, not queue", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("a read-only verb must print nothing about locking, got: %q", stderr.String())
	}
	if elapsed > 2*time.Second {
		t.Fatalf("a read-only verb took %s while the slots were busy — it waited", elapsed)
	}
}

// TestRunCargoShim_ReadOnlyVerbLeavesTheSlotsAlone pins the other half: the
// bypass must not touch the slots at all — no owner file claiming a build
// that is not happening, and every slot still free afterwards.
func TestRunCargoShim_ReadOnlyVerbLeavesTheSlotsAlone(t *testing.T) {
	withIsolatedCargoLock(t)
	cfg := testCargoShimConfig()
	cfg.realCargo = runVerbStub(t)

	var stdout, stderr bytes.Buffer
	runCargoShim([]string{"metadata"}, strings.NewReader(""), &stdout, &stderr, cfg)

	if _, ok := tdd.ReadBuildSlotOwner(shimTargetDir()); ok {
		t.Fatal("a read-only verb must never write an owner file")
	}
	_, release, ok := tdd.TryAcquireBuildSlot(shimTargetDir(), "cargo nextest run -p other-crate", "/some/other/repo")
	if !ok {
		t.Fatal("the slot must be free after a read-only verb — it was never taken")
	}
	release()
}
