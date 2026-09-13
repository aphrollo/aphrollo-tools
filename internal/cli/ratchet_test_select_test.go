package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// twoLawFixtureRepo carries two laws with fixtures: `nan-guard`, whose hit
// row is produced, and `broken-guard`, whose is not. A run that judges both
// fails; a run scoped to the first alone passes.
func twoLawFixtureRepo(t *testing.T) string {
	t.Helper()
	root := lawRepo(t)
	fx := func(law, dir, name, body string) {
		writeFile(t, filepath.Join(root, ".ratchet", "fixtures", law, dir, "crates", "a", "src", name), body)
	}
	fx("nan-guard", "hit", "bare.rs", "let a = x.clamp(0.0, 1.0);\n")
	fx("nan-guard", "clean", "guarded.rs", "let a = safe(x);\n")
	writeFile(t, filepath.Join(root, ".ratchet", "fixtures", "nan-guard", "expected.txt"),
		"crates/a/src/bare.rs:1\n")

	writeFile(t, filepath.Join(root, ".ratchet", "laws", "broken-guard.toml"), `
name = "broken-guard"
description = "A float clamp is not a NaN guard"
severity = "deny"
escape = "// nan-safe:"

[scope]
include = ["crates/**/*.rs"]
exclude = ["**/target/**"]

[matcher]
kind = "regex-absent"
pattern = "\\.clamp\\("
`)
	fx("broken-guard", "hit", "bare.rs", "let a = safe(x);\n")
	fx("broken-guard", "clean", "guarded.rs", "let a = safe(x);\n")
	writeFile(t, filepath.Join(root, ".ratchet", "fixtures", "broken-guard", "expected.txt"),
		"crates/a/src/bare.rs:1\n")
	return root
}

// The gate splits a fixtures run between two binaries when a lane's own law
// change is what the installed one would reject (#659, #673). The half run
// under the lane's build is spelled on the command line, so `--only` has to
// mean "these laws and no others" — a run that quietly judged the rest would
// put the lane's binary in charge of laws the lane never touched.
func TestRatchetTest_OnlyJudgesTheNamedLawsAndIgnoresTheRest(t *testing.T) {
	root := twoLawFixtureRepo(t)
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "test", "--repo", root, "--only", "nan-guard"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "broken-guard") {
		t.Fatalf("--only nan-guard must not judge broken-guard; stdout: %s", out.String())
	}
}

// Without the narrowing the same tree fails, which is what makes the
// narrowing above a real selection rather than a tree that passes anyway.
func TestRatchetTest_JudgesEveryLawWhenOnlyIsAbsent(t *testing.T) {
	root := twoLawFixtureRepo(t)
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "test", "--repo", root}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (broken-guard's hit row is not produced)\nstdout: %s", code, out.String())
	}
}

// The caller on the other side of the split is a program, not a reader: it
// needs each law's verdict as data, and a text format it has to re-parse is
// the drift this repo's design contract calls lossy.
func TestRatchetTest_ReportsEachLawsVerdictAsJSON(t *testing.T) {
	root := twoLawFixtureRepo(t)
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "test", "--repo", root, "--format", "json"}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	var got []ratchet.FixtureResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not a FixtureResult list: %v\n%s", err, out.String())
	}
	if len(got) != 2 {
		t.Fatalf("both laws must report a verdict; got %+v", got)
	}
	byLaw := map[string][]string{}
	for _, r := range got {
		byLaw[r.Law] = r.Failures
	}
	if len(byLaw["nan-guard"]) != 0 {
		t.Errorf("nan-guard's fixtures are sound; failures = %v", byLaw["nan-guard"])
	}
	if len(byLaw["broken-guard"]) != 1 {
		t.Errorf("broken-guard's hit row is not produced; failures = %v", byLaw["broken-guard"])
	}
}

// A law name nobody can answer is a caller that will never get the verdict it
// believes it asked for. Exiting 0 with an empty list is the silent hole the
// split exists to avoid.
func TestRatchetTest_RefusesAnOnlyNamingALawTheRepoDoesNotHave(t *testing.T) {
	root := twoLawFixtureRepo(t)
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "test", "--repo", root, "--only", "no-such-law"}, strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatalf("exit = 0 for a law the repo does not have\nstdout: %s\nstderr: %s", out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "no-such-law") {
		t.Fatalf("the refusal must name the law; stderr: %s", errb.String())
	}
}
