package ratchet

import (
	"path/filepath"
	"strings"
	"testing"
)

// A law written for a newer binary must not wedge an older one. The keys this
// binary knows still apply; the ones it has never heard of are skipped, and
// the run names the law so an operator can see WHY a rule is only half read.
const futureLaw = `
schema = 99
name = "future-law"
description = "a law from a later schema"
severity = "deny"
future_root_key = "whatever this means later"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "regex-absent"
pattern = "\\.clamp\\("
future_matcher_key = 3
`

func TestLoadLawsReadsANewerSchemaLeniently(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "future-law", futureLaw)

	laws, err := LoadLaws(dir)
	if err != nil {
		t.Fatalf("a newer schema is never a hard error: %v", err)
	}
	if len(laws) != 1 {
		t.Fatalf("loaded %d laws, want 1", len(laws))
	}
	l := laws[0]
	if l.Schema != 99 || !l.Newer {
		t.Errorf("schema = %d, newer = %v — want 99, true", l.Schema, l.Newer)
	}
	if l.Matcher.Pattern == nil || !l.Matcher.Pattern.MatchString("x.clamp(0.0, 1.0)") {
		t.Error("the keys this binary DOES know must still be read")
	}
}

// Typo protection is the whole reason parsing is strict, and it only stays
// strict where the binary claims to understand the schema.
func TestLoadLawsRejectsAnUnknownKeyAtTheSupportedSchema(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "future-law", strings.Replace(futureLaw, "schema = 99", "schema = 1", 1))

	if _, err := LoadLaws(dir); err == nil || !strings.Contains(err.Error(), "future_root_key") {
		t.Fatalf("err = %v — an unknown key at the supported schema is a typo, not a feature", err)
	}
}

// A declared schema is a version number, so a non-integer or a zero is a
// broken law rather than a lenient one.
func TestLoadLawsRejectsANonVersionSchema(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "bad-schema", `
schema = "two"
name = "bad-schema"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "regex-absent"
pattern = "x"
`)
	if _, err := LoadLaws(dir); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("err = %v — schema is a positive integer", err)
	}
}

// The warning has to reach an operator, so the run reports which laws were
// only partly understood — the gate turns that into `ratchet-law-newer:<law>`.
func TestCheckNamesEveryLawDeclaringANewerSchema(t *testing.T) {
	root := repoWithNanGuard(t)
	writeLaw(t, root, "future-law", futureLaw)
	write(t, filepath.Join(root, ".ratchet", "baselines", "future-law.txt"), "")

	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.NewerLaws) != 1 || res.NewerLaws[0] != (NewerLaw{Name: "future-law", Schema: 99}) {
		t.Fatalf("newer laws = %v, want [{future-law 99}]", res.NewerLaws)
	}
	if len(res.NewerLaws) > 0 && res.Laws != 2 {
		t.Errorf("laws = %d — a newer law is still counted and still judged", res.Laws)
	}
}
