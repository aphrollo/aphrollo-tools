package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTomlBoolSetIn_ReportsValueAndWhetherTheKeyWasWritten(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "aphrollo.toml")
	body := "[aphrollo]\nundercover = true\nother = false\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if v, set := tomlBoolSetIn(path, "[aphrollo]", "undercover"); !v || !set {
		t.Fatalf("undercover: v=%v set=%v, want true,true", v, set)
	}
	if v, set := tomlBoolSetIn(path, "[aphrollo]", "other"); v || !set {
		t.Fatalf("other: v=%v set=%v, want false,true", v, set)
	}
	if v, set := tomlBoolSetIn(path, "[aphrollo]", "absent"); v || set {
		t.Fatalf("absent key: v=%v set=%v, want false,false", v, set)
	}
	if v, set := tomlBoolSetIn(path, "[missing]", "undercover"); v || set {
		t.Fatalf("wrong table: v=%v set=%v, want false,false", v, set)
	}
	if v, set := tomlBoolSetIn(filepath.Join(dir, "nope.toml"), "[aphrollo]", "undercover"); v || set {
		t.Fatalf("unreadable manifest: v=%v set=%v, want false,false", v, set)
	}
}

func TestTomlBoolIn_DropsTheSetFlag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "aphrollo.toml")
	if err := os.WriteFile(path, []byte("[aphrollo]\nundercover = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !tomlBoolIn(path, "[aphrollo]", "undercover") {
		t.Fatal("tomlBoolIn should report the written true")
	}
	if tomlBoolIn(path, "[aphrollo]", "absent") {
		t.Fatal("tomlBoolIn should report false for an absent key")
	}
}

func TestTomlStringIn_ReadsAScalarStringWithQuotesStripped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "aphrollo.toml")
	body := "[aphrollo]\nname = \"borld\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if v, set := tomlStringIn(path, "[aphrollo]", "name"); v != "borld" || !set {
		t.Fatalf("name: v=%q set=%v, want borld,true", v, set)
	}
	if v, set := tomlStringIn(path, "[aphrollo]", "absent"); v != "" || set {
		t.Fatalf("absent key: v=%q set=%v, want \"\",false", v, set)
	}
	if v, set := tomlStringIn(filepath.Join(dir, "nope.toml"), "[aphrollo]", "name"); v != "" || set {
		t.Fatalf("unreadable manifest: v=%q set=%v, want \"\",false", v, set)
	}
}

func TestCargoPackageName_ReadsThePackageNameOrEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	real := filepath.Join(dir, "Cargo.toml")
	if err := os.WriteFile(real, []byte("[package]\nname = \"mycrate\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := cargoPackageName(real); got != "mycrate" {
		t.Fatalf("cargoPackageName = %q, want mycrate", got)
	}

	virtual := filepath.Join(dir, "workspace-Cargo.toml")
	if err := os.WriteFile(virtual, []byte("[workspace]\nmembers = [\"a\", \"b\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := cargoPackageName(virtual); got != "" {
		t.Fatalf("virtual manifest cargoPackageName = %q, want \"\"", got)
	}

	if got := cargoPackageName(filepath.Join(dir, "missing", "Cargo.toml")); got != "" {
		t.Fatalf("missing manifest cargoPackageName = %q, want \"\"", got)
	}
}

func TestDedupeSorted_DropsEmptyDuplicatesAndSorts(t *testing.T) {
	t.Parallel()
	got := dedupeSorted([]string{"b", "", "a", "b", "", "c", "a"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("dedupeSorted = %#v, want %#v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("dedupeSorted = %#v, want %#v", got, want)
		}
	}
	if got := dedupeSorted(nil); got != nil {
		t.Fatalf("dedupeSorted(nil) = %#v, want nil", got)
	}
}
