package ratchet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeScopes(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".ratchet"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(ScopesFile)), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const aliasedLaw = `
name = "tier1-size"
description = "Tier-1 files stay small"
severity = "deny"

[scope]
alias = "tier1"

[matcher]
kind = "line-count"
max = 5
`

func TestLoadScopeSets_ParsesTheSetsTable(t *testing.T) {
	dir := t.TempDir()
	writeScopes(t, dir, `
[sets]
tier1 = ["crates/movement/**", "crates/pose/**"]
presentation = ["crates/ui/**"]
`)
	sets, err := LoadScopeSets(dir)
	if err != nil {
		t.Fatalf("LoadScopeSets: %v", err)
	}
	if len(sets["tier1"]) != 2 || sets["tier1"][0] != "crates/movement/**" {
		t.Errorf("tier1 = %v", sets["tier1"])
	}
	if len(sets["presentation"]) != 1 {
		t.Errorf("presentation = %v", sets["presentation"])
	}
}

func TestLoadScopeSets_AbsentFileIsEmptyNotError(t *testing.T) {
	dir := t.TempDir()
	sets, err := LoadScopeSets(dir)
	if err != nil || sets != nil {
		t.Fatalf("sets = %v, err = %v — a repo with no scopes.toml declares zero aliases", sets, err)
	}
}

// TestLoadLaws_ResolvesAliasIntoInclude proves a law naming `[scope].alias`
// judges exactly the files the named set lists, merged with any include of
// its own — the alias behaves like the include list it stands in for.
func TestLoadLaws_ResolvesAliasIntoInclude(t *testing.T) {
	dir := t.TempDir()
	writeScopes(t, dir, `
[sets]
tier1 = ["crates/movement/**/*.rs"]
`)
	writeLaw(t, dir, "tier1-size", aliasedLaw)

	laws, err := LoadLaws(dir)
	if err != nil {
		t.Fatalf("LoadLaws: %v", err)
	}
	if len(laws) != 1 {
		t.Fatalf("loaded %d laws, want 1", len(laws))
	}
	l := laws[0]
	if l.Scope.Alias != "tier1" {
		t.Errorf("Scope.Alias = %q, want %q", l.Scope.Alias, "tier1")
	}
	if !l.Scope.Matches("crates/movement/src/lib.rs") {
		t.Error("a law scoped by alias must match a file the named set covers")
	}
	if l.Scope.Matches("crates/pose/src/lib.rs") {
		t.Error("a law scoped by alias must not match a file outside the named set")
	}
}

// TestLoadLaws_MergesAliasWithOwnInclude proves the alias's globs are a BASE,
// not a replacement: a law may still widen its own scope with more include
// entries alongside the alias.
func TestLoadLaws_MergesAliasWithOwnInclude(t *testing.T) {
	dir := t.TempDir()
	writeScopes(t, dir, `
[sets]
tier1 = ["crates/movement/**/*.rs"]
`)
	writeLaw(t, dir, "tier1-size", `
name = "tier1-size"
description = "Tier-1 files stay small"
severity = "deny"

[scope]
alias = "tier1"
include = ["crates/pose/**/*.rs"]

[matcher]
kind = "line-count"
max = 5
`)
	laws, err := LoadLaws(dir)
	if err != nil {
		t.Fatalf("LoadLaws: %v", err)
	}
	l := laws[0]
	if !l.Scope.Matches("crates/movement/src/lib.rs") {
		t.Error("alias-derived include must still match")
	}
	if !l.Scope.Matches("crates/pose/src/lib.rs") {
		t.Error("the law's own include must be merged in, not dropped")
	}
}

// TestLoadLaws_MergesAliasWhenOwnIncludeOutnumbersTheAliasSet proves the
// merge's capacity is computed as a SUM of both lists' lengths, not a
// difference: an alias set shorter than the law's own include list must
// still merge cleanly (a subtraction here would make the capacity go
// negative and panic on `make`), and every glob from both lists must survive.
func TestLoadLaws_MergesAliasWhenOwnIncludeOutnumbersTheAliasSet(t *testing.T) {
	dir := t.TempDir()
	writeScopes(t, dir, `
[sets]
tier1 = ["crates/movement/**/*.rs"]
`)
	writeLaw(t, dir, "tier1-size", `
name = "tier1-size"
description = "Tier-1 files stay small"
severity = "deny"

[scope]
alias = "tier1"
include = ["crates/pose/**/*.rs", "crates/pose_ik/**/*.rs", "crates/ui/**/*.rs"]

[matcher]
kind = "line-count"
max = 5
`)
	laws, err := LoadLaws(dir)
	if err != nil {
		t.Fatalf("LoadLaws: %v", err)
	}
	l := laws[0]
	for _, file := range []string{
		"crates/movement/src/lib.rs",
		"crates/pose/src/lib.rs",
		"crates/pose_ik/src/lib.rs",
		"crates/ui/src/lib.rs",
	} {
		if !l.Scope.Matches(file) {
			t.Errorf("%s must match — merged from the alias and the law's own include", file)
		}
	}
}

// TestLoadLaws_RefusesAnAliasNoSetDefines is the deny path: a law naming an
// alias scopes.toml never declared is a hard, one-line error naming both.
func TestLoadLaws_RefusesAnAliasNoSetDefines(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "tier1-size", aliasedLaw)

	_, err := LoadLaws(dir)
	if err == nil {
		t.Fatal("expected an error — no scopes.toml declares the tier1 set")
	}
	msg := err.Error()
	if !strings.Contains(msg, "tier1-size") || !strings.Contains(msg, "tier1") || !strings.Contains(msg, ScopesFile) {
		t.Errorf("error must name the law and the alias: %s", msg)
	}
	if strings.Count(msg, "\n") > 0 {
		t.Errorf("expected one line, got: %s", msg)
	}
}

// TestCheck_WarnsOnAScopeSetNoLawUses proves the OTHER direction: a set that
// sits in scopes.toml unreferenced is reported, never silently kept.
func TestCheck_WarnsOnAScopeSetNoLawUses(t *testing.T) {
	dir := t.TempDir()
	writeScopes(t, dir, `
[sets]
tier1 = ["crates/movement/**/*.rs"]
dead = ["crates/nowhere/**"]
`)
	writeLaw(t, dir, "tier1-size", aliasedLaw)

	res, err := Check(Options{Root: dir, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.UnusedScopeSets) != 1 || res.UnusedScopeSets[0] != "dead" {
		t.Errorf("UnusedScopeSets = %v, want [dead]", res.UnusedScopeSets)
	}
}

// TestRunFixtures_ProvesLawThroughAlias is the fixture-proving path: a law
// scoped ONLY by alias still catches its hit fixture and stays silent on its
// clean one, proving `ratchet test` resolves an alias the same way `ratchet
// check` does — through the same LoadLaws call.
func TestRunFixtures_ProvesLawThroughAlias(t *testing.T) {
	dir := t.TempDir()
	writeScopes(t, dir, `
[sets]
tier1 = ["crates/movement/**/*.rs"]
`)
	writeLaw(t, dir, "tier1-size", aliasedLaw)

	fixtureDir := filepath.Join(dir, ".ratchet", "fixtures", "tier1-size")
	write := func(rel, body string) {
		p := filepath.Join(fixtureDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// max = 5: the hit fixture has 6 lines, the clean one has 3.
	write("hit/crates/movement/src/big.rs", "1\n2\n3\n4\n5\n6\n")
	write("clean/crates/movement/src/small.rs", "1\n2\n3\n")
	write("expected.txt", "crates/movement/src/big.rs\n")

	results, err := RunFixtures(dir)
	if err != nil {
		t.Fatalf("RunFixtures: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	if len(r.Failures) != 0 {
		t.Fatalf("fixtures failed: %v", r.Failures)
	}
	if r.HitFiles != 1 || r.CleanFiles != 1 {
		t.Errorf("HitFiles=%d CleanFiles=%d, want 1 and 1", r.HitFiles, r.CleanFiles)
	}
}
