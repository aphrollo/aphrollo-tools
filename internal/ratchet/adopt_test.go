package ratchet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAdoptRefusesAnUnchangedLawWithAnExistingBaseline is the RED case: a
// law with a baseline already on disk, reported unchanged since HEAD, must
// never let Adopt through — that would make "adopt" a raised ceiling with an
// extra step.
func TestAdoptRefusesAnUnchangedLawWithAnExistingBaseline(t *testing.T) {
	root := repoWithNanGuard(t)
	_, err := Adopt(AdoptOptions{Root: root, Law: "nan-guard", LawChangedSinceHEAD: false})
	if err == nil {
		t.Fatal("expected a refusal — the law is unchanged and already has a baseline")
	}
	if want := "nan-guard"; !strings.Contains(err.Error(), want) {
		t.Errorf("error must name the law: %v", err)
	}
}

// TestAdoptWritesRowsForANewLawWithNoBaselineYet is the "lands its first
// baseline" case: LawChangedSinceHEAD is irrelevant here because there is no
// baseline file to protect at all.
func TestAdoptWritesRowsForANewLawWithNoBaselineYet(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "nan-guard", nanGuardLaw)
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")

	res, err := Adopt(AdoptOptions{Root: root, Law: "nan-guard", LawChangedSinceHEAD: false})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if res.Rows != 1 {
		t.Errorf("Rows = %d, want 1", res.Rows)
	}
	data, err := os.ReadFile(filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"))
	if err != nil {
		t.Fatalf("baseline was not written: %v", err)
	}
	if got, want := string(data), "crates/a/src/lib.rs | let a = x.clamp(0.0, 1.0);\n"; got != want {
		t.Errorf("baseline = %q, want %q", got, want)
	}

	// Once adopted, an ordinary Check must report it clean.
	res2, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res2.Findings) != 0 {
		t.Errorf("Findings = %+v, want none — the adopted row covers this exact hit", res2.Findings)
	}
}

// TestAdoptWritesRowsForAWidenedLaw proves the other stated case: an existing
// law whose baseline already exists, but is reported changed since HEAD
// (a widened scope, say), gets its new hits recorded rather than rejected.
func TestAdoptWritesRowsForAWidenedLaw(t *testing.T) {
	root := repoWithNanGuard(t)
	// A second, previously out-of-scope offender the existing baseline never
	// saw — as if the law's scope had just been widened to reach it.
	write(t, filepath.Join(root, "crates", "b", "src", "lib.rs"), "let b = y.clamp(0.0, 1.0);\n")

	res, err := Adopt(AdoptOptions{Root: root, Law: "nan-guard", LawChangedSinceHEAD: true})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if res.Rows != 2 {
		t.Errorf("Rows = %d, want 2 (the existing site plus the newly-reached one)", res.Rows)
	}

	res2, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res2.Findings) != 0 {
		t.Errorf("a freshly adopted baseline must report clean: %+v", res2.Findings)
	}
}

// TestAdoptRefusesALawWithNoBaselineDeclared proves the trivial guard: a law
// that names no baseline (a zero-bar law) has nothing for Adopt to write.
func TestAdoptRefusesALawWithNoBaselineDeclared(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "no-baseline", `
name = "no-baseline"
description = "a zero-bar law"
severity = "deny"

[scope]
include = ["**/*.rs"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	if _, err := Adopt(AdoptOptions{Root: root, Law: "no-baseline", LawChangedSinceHEAD: true}); err == nil {
		t.Fatal("expected a refusal — the law declares no baseline")
	}
}
