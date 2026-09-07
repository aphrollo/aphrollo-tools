package tdd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// errBoom is a driver failure with a distinctive message a test can look for
// in the output, standing in for whatever cargo-mutants or gremlins itself
// would report.
var errBoom = errors.New("boom: no toolchain in this sandbox")

// The whole point of issue #522's central constraint: a whole-crate run
// measures more than any lane diff, so letting it satisfy the merge gate
// would launder a proof for a lane it never measured. This file must never
// even MENTION the receipt machinery that would make that possible — a
// static scan rather than a behavioural test, because the failure mode it
// guards is silent and permanent: a future edit that added one call to
// MutationReceiptPathFor or signReceipt here would not fail any existing
// test, only quietly reopen the hole.
func TestMutantsAudit_SourceNeverReferencesTheReceiptMachinery(t *testing.T) {
	data, err := os.ReadFile("mutants_audit.go")
	if err != nil {
		t.Fatal(err)
	}
	// Comment lines are stripped first: the file's own doc comment NAMES
	// tools/mutation_gate.sh to explain why it is avoided, which is exactly
	// the kind of mention this scan must not itself flag. What matters is
	// CODE never invoking these, not prose describing the decision.
	var codeOnly strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		codeOnly.WriteString(line)
		codeOnly.WriteByte('\n')
	}
	src := codeOnly.String()
	for _, forbidden := range []string{
		"MutationReceiptPathFor(", "signReceipt(", "writeReceiptFile(", "mutation_gate.sh",
	} {
		if strings.Contains(src, forbidden) {
			t.Fatalf("mutants_audit.go's CODE (comments excluded) references %q — an audit run must never touch the lane receipt machinery", forbidden)
		}
	}
}

// The same guarantee, proven at the level RunMutantsAudit actually runs at:
// a full dispatch (driver faked, since no toolchain runs in this suite)
// leaves the receipt store exactly as it found it.
func TestRunMutantsAudit_WritesNoFileToTheReceiptStore(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	withFreeSpace(t, 200)
	root := makeCargoRepo(t)

	prev := mutantsAuditRustFn
	mutantsAuditRustFn = func(root, scope string, stdout, stderr io.Writer) (AuditReport, error) {
		return AuditReport{Scope: scope, Total: 1, Caught: 0, Survivors: []AuditSurvivor{{File: "src/lib.rs", Line: 3, Mutation: "replace + with -"}}}, nil
	}
	t.Cleanup(func() { mutantsAuditRustFn = prev })

	before := receiptFilesUnder(t, cfg)
	var out bytes.Buffer
	if code := RunMutantsAudit(root, "m", &out, &out); code != 0 {
		t.Fatalf("RunMutantsAudit = %d, want 0\n%s", code, out.String())
	}
	after := receiptFilesUnder(t, cfg)
	if len(after) != len(before) {
		t.Fatalf("audit left %d new file(s) under the receipt store: %v", len(after)-len(before), after)
	}
}

func receiptFilesUnder(t *testing.T, cfgDir string) []string {
	t.Helper()
	var found []string
	_ = filepath.Walk(cfgDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if strings.HasPrefix(filepath.Base(path), "mutation-receipt.") {
			found = append(found, path)
		}
		return nil
	})
	return found
}

// No --package at all must be a usage error, not a silent no-op: the whole
// purpose of this verb is to measure something specific.
func TestRunMutantsAudit_RefusesAnEmptyScope(t *testing.T) {
	var out, errb bytes.Buffer
	if code := RunMutantsAudit(t.TempDir(), "", &out, &errb); code != 2 {
		t.Fatalf("RunMutantsAudit with empty scope = %d, want 2\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--package") {
		t.Fatalf("stderr = %q, want it to name the flag that is missing", errb.String())
	}
}

func TestRunMutantsAudit_RefusesOutsideAGitRepository(t *testing.T) {
	var out, errb bytes.Buffer
	dir := t.TempDir()
	if code := RunMutantsAudit(dir, "m", &out, &errb); code != 1 {
		t.Fatalf("RunMutantsAudit outside a repo = %d, want 1\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "git repository") {
		t.Fatalf("stderr = %q, want it to say this is not a git repository", errb.String())
	}
}

// A repo with neither manifest has nothing this binary knows how to drive.
func TestRunMutantsAudit_RefusesARepoWithNeitherManifest(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withFreeSpace(t, 200)
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "README.md", "nothing to build here\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "seed")

	var out, errb bytes.Buffer
	if code := RunMutantsAudit(root, "m", &out, &errb); code != 1 {
		t.Fatalf("RunMutantsAudit with no manifest = %d, want 1\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "Cargo.toml") || !strings.Contains(errb.String(), "go.mod") {
		t.Fatalf("stderr = %q, want it to name both manifests it looked for", errb.String())
	}
}

// A drive too small to build in refuses before anything is spawned — the
// same disk guard the lane path uses, applied here too.
func TestRunMutantsAudit_RefusesWhenTheDriveCannotFitTheBuild(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withFreeSpace(t, 12)
	root := makeCargoRepo(t)

	var out, errb bytes.Buffer
	if code := RunMutantsAudit(root, "m", &out, &errb); code != 1 {
		t.Fatalf("RunMutantsAudit on a 12 GB drive = %d, want 1\nstderr: %s", code, errb.String())
	}
}

// A Cargo workspace dispatches to the Rust driver, prints the cost notice
// before any survivor line, and renders survivors ranked as "file:line:
// mutation" the way issue #522 spells the format.
func TestRunMutantsAudit_DispatchesToRustAndRendersRankedSurvivors(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withFreeSpace(t, 200)
	root := makeCargoRepo(t)

	var calledWith string
	prev := mutantsAuditRustFn
	mutantsAuditRustFn = func(root, scope string, stdout, stderr io.Writer) (AuditReport, error) {
		calledWith = scope
		return AuditReport{
			Scope: scope, Total: 3, Caught: 1,
			Survivors: []AuditSurvivor{
				{File: "src/b.rs", Line: 5, Mutation: "replace * with /"},
				{File: "src/a.rs", Line: 20, Mutation: "replace + with -"},
				{File: "src/a.rs", Line: 9, Mutation: "delete !"},
			},
		}, nil
	}
	t.Cleanup(func() { mutantsAuditRustFn = prev })

	var out bytes.Buffer
	if code := RunMutantsAudit(root, "m", &out, &out); code != 0 {
		t.Fatalf("RunMutantsAudit = %d, want 0\n%s", code, out.String())
	}
	if calledWith != "m" {
		t.Fatalf("Rust driver called with scope %q, want %q", calledWith, "m")
	}
	text := out.String()
	wantOrder := []string{
		"auditing m in full",
		"src/a.rs:9: delete !",
		"src/a.rs:20: replace + with -",
		"src/b.rs:5: replace * with /",
	}
	last := -1
	for _, want := range wantOrder {
		i := strings.Index(text, want)
		if i < 0 {
			t.Fatalf("output never contains %q:\n%s", want, text)
		}
		if i < last {
			t.Fatalf("%q appears before something it must follow:\n%s", want, text)
		}
		last = i
	}
}

// A go.mod-only repo (no Cargo.toml) dispatches to the Go driver instead.
func TestRunMutantsAudit_DispatchesToGoForAGoModRepo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withFreeSpace(t, 200)
	root := makeGoRepo(t)

	var calledWith string
	prev := mutantsAuditGoFn
	mutantsAuditGoFn = func(root, scope string, stdout, stderr io.Writer) (AuditReport, error) {
		calledWith = scope
		return AuditReport{Scope: scope, Total: 1, Caught: 1}, nil
	}
	t.Cleanup(func() { mutantsAuditGoFn = prev })

	var out bytes.Buffer
	if code := RunMutantsAudit(root, "./internal/x", &out, &out); code != 0 {
		t.Fatalf("RunMutantsAudit = %d, want 0\n%s", code, out.String())
	}
	if calledWith != "./internal/x" {
		t.Fatalf("Go driver called with scope %q, want %q", calledWith, "./internal/x")
	}
}

// A driver that fails to measure anything is reported, not swallowed as a
// quiet success — the whole purpose of typing this command is to measure
// something.
func TestRunMutantsAudit_ReportsADriverFailure(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withFreeSpace(t, 200)
	root := makeCargoRepo(t)

	prev := mutantsAuditRustFn
	mutantsAuditRustFn = func(root, scope string, stdout, stderr io.Writer) (AuditReport, error) {
		return AuditReport{}, errBoom
	}
	t.Cleanup(func() { mutantsAuditRustFn = prev })

	var out, errb bytes.Buffer
	if code := RunMutantsAudit(root, "m", &out, &errb); code != 1 {
		t.Fatalf("RunMutantsAudit = %d, want 1 for a driver failure\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), errBoom.Error()) {
		t.Fatalf("stderr = %q, want the driver's own error", errb.String())
	}
}

// outcomesToReport ranks survivors by file then line then column, whatever
// order the tool reported them in, and counts caught/total from Status alone.
func TestOutcomesToReport_RanksSurvivorsByFileThenLine(t *testing.T) {
	in := []MutantOutcome{
		{File: "b.rs", Line: 1, Mutation: "x", Status: "missed"},
		{File: "a.rs", Line: 20, Mutation: "y", Status: "missed"},
		{File: "a.rs", Line: 5, Mutation: "z", Status: "caught"},
		{File: "a.rs", Line: 3, Mutation: "w", Status: "missed"},
	}
	r := outcomesToReport("m", in)
	if r.Total != 4 || r.Caught != 1 {
		t.Fatalf("total=%d caught=%d, want 4 and 1", r.Total, r.Caught)
	}
	if len(r.Survivors) != 3 {
		t.Fatalf("survivors = %d, want 3", len(r.Survivors))
	}
	got := make([]string, len(r.Survivors))
	for i, s := range r.Survivors {
		got[i] = s.String()
	}
	// Line is compared NUMERICALLY (3 < 20), never as text — the rendered
	// strings "a.rs:3: w" and "a.rs:20: y" would sort the other way as plain
	// text, since '2' < '3' byte-wise.
	want := []string{"a.rs:3: w", "a.rs:20: y", "b.rs:1: x"}
	if !stringsEqual(got, want) {
		t.Fatalf("survivor order = %v, want %v", got, want)
	}
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
