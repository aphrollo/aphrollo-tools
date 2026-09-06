package ratchet

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadLaws_RegistryEntryColumnRejectsANegativeIndex proves entry_column
// is validated at load, since a negative index would otherwise reach
// tableCell and silently skip every registry line rather than erroring.
func TestLoadLaws_RegistryEntryColumnRejectsANegativeIndex(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "c", `
name = "c"
description = "d"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "registry-both-ways"
registry_file = "REGISTRY.md"
entry_pattern = "`+"`([a-z_]+)`"+`"
use_pattern = "crate\\.([a-z_]+)\\."
entry_column = -1
`)
	_, err := LoadLaws(dir)
	if err == nil {
		t.Fatal("err = nil, want a rejection: entry_column is negative")
	}
	if !strings.Contains(err.Error(), "entry_column") {
		t.Fatalf("err = %q, want it to name entry_column", err.Error())
	}
}

// #492's second half: registry-both-ways cannot scope entry_pattern to one
// COLUMN of a markdown table. A backticked crate name and a backticked word
// in the prose column beside it are otherwise indistinguishable to a
// single-group regex, since Go's RE2 has neither lookbehind nor a repeated
// capture group.

// entryColumnLaw scopes entry_pattern to column 1 (0-based, second cell) of
// each `|`-delimited registry row — the CRATE column — while the PROSE
// column at index 0 carries its own backticked words that must never be
// read as a registration.
func entryColumnLaw(t *testing.T, root string) Law {
	t.Helper()
	law, err := ParseLaw(`
name = "crate-column"
description = "every crate::<name>:: used is registered in the crate column"
severity = "deny"

[scope]
include = ["crates/**/*.go"]

[matcher]
kind = "registry-both-ways"
registry_file = "REGISTRY.md"
entry_pattern = "`+"`([a-z_]+)`"+`"
entry_column = 1
use_pattern = "crate\.([a-z_]+)\."
`, "crate-column")
	if err != nil {
		t.Fatal(err)
	}
	law.Root = root
	return law
}

func TestRegistry_EntryColumnScopesToTheCrateCellNotTheProseCell(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "REGISTRY.md"), strings.Join([]string{
		"| notes | crate |",
		"|---|---|",
		"| powers the `override` layer | `zone` |",
		"| items and `containers` | `item` |",
		"| unused elsewhere | `ghost` |",
	}, "\n")+"\n")
	rel := "crates/a/main.go"
	content := map[string]string{rel: "package a\n\nfunc f() {\n\tcrate.zone.Do()\n\tcrate.item.Do()\n}\n"}

	law := entryColumnLaw(t, root)
	hits, err := registryHits(root, law, []string{rel}, content, false, true)
	if err != nil {
		t.Fatalf("registryHits: %v", err)
	}
	joined := ""
	for _, h := range hits {
		joined += h.What + "\n"
	}
	if len(hits) != 1 || !strings.Contains(joined, "ghost") {
		t.Fatalf("hits = %+v, want exactly one naming the stale entry ghost", hits)
	}
	if strings.Contains(joined, "override") || strings.Contains(joined, "containers") {
		t.Errorf("the prose column's backticked words must never be read as registered names:\n%s", joined)
	}
	if strings.Contains(joined, "zone") || strings.Contains(joined, "item") {
		t.Errorf("zone and item are both registered AND used, so neither is a finding:\n%s", joined)
	}
}

// TestRegistry_EntryColumnSkipsARowWithTooFewCells proves tableCell's ok=false
// path: a registry line with no second cell (prose, or the table's own
// separator row) registers nothing rather than panicking on an out-of-range
// index or misreading whatever text happens to sit at that offset.
func TestRegistry_EntryColumnSkipsARowWithTooFewCells(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "REGISTRY.md"), "prose with no table structure at all\n")
	rel := "crates/a/main.go"
	content := map[string]string{rel: "package a\n\nfunc f() { crate.zone.Do() }\n"}

	law := entryColumnLaw(t, root)
	hits, err := registryHits(root, law, []string{rel}, content, false, true)
	if err != nil {
		t.Fatalf("registryHits: %v", err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].What, "zone") {
		t.Fatalf("a row with no second cell must not register anything, so zone stays unregistered: %+v", hits)
	}
}

// entryColumnFixtureLaw is entryColumnLaw laid out for the fixture harness
// (see TestRunFixtures_ProvesTheEntryColumnRegistryLaw's own comment for why
// no real `.ratchet/laws/*.toml` lands for it in this repo).
const entryColumnFixtureLaw = `
name = "crate_column_registered"
description = "every crate.<name>. used is registered in the crate column, not the prose column beside it"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "registry-both-ways"
registry_file = "REGISTRY.md"
entry_pattern = "` + "`([a-z_]+)`" + `"
entry_column = 1
use_pattern = "crate\.([a-z_]+)\."
`

// TestRunFixtures_ProvesTheEntryColumnRegistryLaw proves entry_column end to
// end through the fixture harness: the hit case's prose column carries its
// own backticked word ("override") beside the crate column's real name, and
// only the crate column's name may register or be flagged.
func TestRunFixtures_ProvesTheEntryColumnRegistryLaw(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "crate_column_registered", entryColumnFixtureLaw)
	fx := filepath.Join(root, ".ratchet", "fixtures", "crate_column_registered")

	write(t, filepath.Join(fx, "hit", "REGISTRY.md"),
		"| notes | crate |\n|---|---|\n| powers the `override` layer | `zone` |\n")
	write(t, filepath.Join(fx, "hit", "a.go"),
		"package a\n\nfunc f() {\n\tcrate.zone.Do()\n\tcrate.ghost.Do()\n}\n")
	write(t, filepath.Join(fx, "expected.txt"), "a.go:5\n")

	write(t, filepath.Join(fx, "clean", "REGISTRY.md"),
		"| notes | crate |\n|---|---|\n| powers the `override` layer | `zone` |\n")
	write(t, filepath.Join(fx, "clean", "a.go"), "package a\n\nfunc f() { crate.zone.Do() }\n")

	results, err := RunFixtures(root)
	if err != nil {
		t.Fatalf("RunFixtures: %v", err)
	}
	if len(results) != 1 || len(results[0].Failures) != 0 {
		t.Fatalf("results = %+v", results)
	}
}
