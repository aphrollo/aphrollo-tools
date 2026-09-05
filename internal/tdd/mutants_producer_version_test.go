package tdd

import (
	"errors"
	"path/filepath"
	"testing"
)

// The command run to identify the mutator set depends on which producer the
// worktree actually has: a Cargo workspace's tools/mutation_gate.sh means
// cargo-mutants, a bare go.mod means gremlins.
func TestMutantsProducerVersionCmd_PicksTheToolTheWorktreeActuallyHas(t *testing.T) {
	cargoRoot := t.TempDir()
	mustWriteFile(filepath.Join(cargoRoot, "tools", "mutation_gate.sh"), "#!/bin/sh\n")
	name, args, ok := mutantsProducerVersionCmd(cargoRoot)
	if !ok || name != "cargo-mutants" || len(args) != 2 || args[0] != "mutants" || args[1] != "--version" {
		t.Fatalf("mutantsProducerVersionCmd(cargo repo) = %q %v %v, want cargo-mutants mutants --version", name, args, ok)
	}

	goRoot := t.TempDir()
	mustWriteFile(filepath.Join(goRoot, "go.mod"), "module x\n")
	name, args, ok = mutantsProducerVersionCmd(goRoot)
	if !ok || name != gremlinsBin || len(args) != 1 || args[0] != "--version" {
		t.Fatalf("mutantsProducerVersionCmd(go repo) = %q %v %v, want %s --version", name, args, ok, gremlinsBin)
	}

	neither, ok := t.TempDir(), false
	if _, _, ok = mutantsProducerVersionCmd(neither); ok {
		t.Fatal("a worktree with neither manifest must name no producer")
	}
}

// A version query is a diagnostic, never load-bearing: an error probing it
// (tool missing, non-zero exit) must degrade to "", the same as before this
// field existed, rather than block the run it is only trying to annotate.
func TestMutantsProducerVersion_IsEmptyWhenTheProbeErrors(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(filepath.Join(root, "go.mod"), "module x\n")

	prev := mutantsProducerVersionRunFn
	mutantsProducerVersionRunFn = func(name string, args []string) (string, error) {
		return "", errors.New("executable file not found in $PATH")
	}
	t.Cleanup(func() { mutantsProducerVersionRunFn = prev })

	if got := mutantsProducerVersion(root); got != "" {
		t.Fatalf("mutantsProducerVersion = %q, want empty when the probe errors", got)
	}
}

// The version string is trimmed and returned verbatim on success.
func TestMutantsProducerVersion_ReturnsTheProbesTrimmedOutput(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(filepath.Join(root, "go.mod"), "module x\n")

	prev := mutantsProducerVersionRunFn
	mutantsProducerVersionRunFn = func(name string, args []string) (string, error) {
		return "gremlins version 0.6.0 linux/amd64\n", nil
	}
	t.Cleanup(func() { mutantsProducerVersionRunFn = prev })

	if got := mutantsProducerVersion(root); got != "gremlins version 0.6.0 linux/amd64" {
		t.Fatalf("mutantsProducerVersion = %q, want the probe's trimmed output", got)
	}
}

// stampProducerVersion must not mutate the caller's own slice: the Rust
// runner reuses its outcomes slice for the signed receipt right after
// stamping it, and a shared backing array would let the store's stamp bleed
// into the receipt the producer already signed, or vice versa.
func TestStampProducerVersion_LeavesTheInputSliceUntouched(t *testing.T) {
	in := []MutantOutcome{{File: "a.rs", Line: 1, Mutation: "m"}}
	out := stampProducerVersion(in, "gremlins 0.7.0")

	if in[0].ProducerVersion != "" {
		t.Fatalf("input outcome mutated in place: %+v", in[0])
	}
	if len(out) != 1 || out[0].ProducerVersion != "gremlins 0.7.0" {
		t.Fatalf("stampProducerVersion output = %+v, want ProducerVersion stamped", out)
	}
}
