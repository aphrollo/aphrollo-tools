package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// falsePositiveLabelled is an issue the loop owns as a false positive, whose
// closes-by names no file -- the fixture-based path (issue #468) must judge
// it on the DIFF and the law itself, never on a name it never gave.
const falsePositiveLabelled = `{"labels":[{"name":"false-positive"}],"body":"a false positive.\n"}`

// writeProvenFixtureLaw writes a minimal, real law plus a fixture pair that
// RunFixtures proves cleanly in both directions: a `hit/` file matching the
// law's own forbidden pattern at a known line, and a `clean/` file that does
// not. It exists so closureChangesAFixture exercises the REAL ratchet engine
// (LoadLaws, RunFixtures) rather than a stand-in for it.
func writeProvenFixtureLaw(t *testing.T, root, name string) {
	t.Helper()
	lawsDir := filepath.Join(root, ".ratchet", "laws")
	if err := os.MkdirAll(lawsDir, 0o777); err != nil {
		t.Fatal(err)
	}
	toml := "name        = \"" + name + "\"\n" +
		"description = \"test-only law for closureChangesAFixture\"\n" +
		"severity    = \"deny\"\n\n" +
		"[scope]\n" +
		"include = [\"a/**/*.go\"]\n\n" +
		"[matcher]\n" +
		"kind    = \"regex-absent\"\n" +
		"pattern = \"forbidden\\\\(\"\n" +
		"key     = \"file:line-content-hash\"\n"
	if err := os.WriteFile(filepath.Join(lawsDir, name+".toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile := func(rel, content string) {
		p := filepath.Join(root, ".ratchet", "fixtures", name, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile("hit/a/bare.go", "package a\n\nfunc x() {\n\tforbidden()\n}\n")
	writeFixtureFile("clean/a/bare.go", "package a\n\nfunc x() {\n\tallowed()\n}\n")
	writeFixtureFile("expected.txt", "a/bare.go:4\n")
}

// TestVerifyClosureAcceptsFalsePositive_WithFixtureProvenBothDirections pins
// #468's core mechanism: a false-positive issue closes when the diff
// touches a fixture under .ratchet/fixtures/<law>/ AND `aphrollo ratchet
// test` (via RunFixtures) proves that law in both directions.
func TestVerifyClosureAcceptsFalsePositive_WithFixtureProvenBothDirections(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	writeProvenFixtureLaw(t, root, "probe_law")

	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor(".ratchet/fixtures/probe_law/hit/a/bare.go", "+\tforbidden()"),
		falsePositiveLabelled)

	var out strings.Builder
	ok, err := VerifyClosure(root, "31", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(out.String(), "ok") {
		t.Fatalf("a fixture that proves in both directions closes a false positive:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "probe_law") {
		t.Errorf("the verdict must name which law's fixture closed it:\n%s", out.String())
	}
}

// TestVerifyClosureRejectsFalsePositive_WhenFixtureFailsRatchetTest pins the
// half of #468 that distinguishes it from a bare path check: touching a
// file under .ratchet/fixtures/<law>/ is not enough on its own -- the law
// must actually prove clean, or the fixture that "closed" the issue never
// ran.
func TestVerifyClosureRejectsFalsePositive_WhenFixtureFailsRatchetTest(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	writeProvenFixtureLaw(t, root, "probe_law")
	// Break the fixture: the clean file now ALSO trips the law's own
	// pattern, so RunFixtures reports a failure for probe_law instead of a
	// clean pass in both directions.
	brokenClean := filepath.Join(root, ".ratchet", "fixtures", "probe_law", "clean", "a", "bare.go")
	if err := os.WriteFile(brokenClean, []byte("package a\n\nfunc x() {\n\tforbidden()\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	verifyStub(t, `{"body":"Closes #42\n","commits":[]}`,
		diffFor(".ratchet/fixtures/probe_law/hit/a/bare.go", "+\tforbidden()"),
		falsePositiveLabelled)

	var out strings.Builder
	ok, err := VerifyClosure(root, "31", &out)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("a fixture whose law does not prove clean must not close the issue:\n%s", out.String())
	}
	// The generic check would ALSO fail this diff (it touches nothing under
	// .ratchet/laws/), so a bare "#42 ... FAIL" is not enough to prove this
	// exercised the fixture-specific path at all -- assert the wording only
	// the false-positive branch prints, or this test passes identically
	// against pre-edit code that has no such branch.
	if !strings.Contains(out.String(), "#42 FAIL") || !strings.Contains(out.String(), "a false-positive issue changes no check") {
		t.Errorf("expected the false-positive-specific FAIL wording naming the issue:\n%s", out.String())
	}
}

// TestVerifyClosureRejectsFalsePositive_ClosedWithOnlyASentence pins that
// the fixture door does not accidentally widen the generic one: a
// false-positive issue closed by a docs-only diff still fails, exactly
// like a plain escape.
func TestVerifyClosureRejectsFalsePositive_ClosedWithOnlyASentence(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	verifyStub(t, `{"body":"Fixes it.\n\nCloses #42\n","commits":[]}`,
		diffFor("docs/notes.md", "+a note"), falsePositiveLabelled)

	var out strings.Builder
	ok, err := VerifyClosure(t.TempDir(), "31", &out)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("a docs-only PR must not close a false-positive issue:\n%s", out.String())
	}
	// Same reasoning as the broken-fixture case above: the generic check
	// already rejects a docs-only diff on pre-edit code too, so the
	// false-positive-specific wording is what actually proves this ran
	// through the new branch rather than passing by coincidence.
	if !strings.Contains(out.String(), "#42 FAIL") || !strings.Contains(out.String(), "a false-positive issue changes no check") {
		t.Errorf("expected the false-positive-specific FAIL wording naming the issue:\n%s", out.String())
	}
}
