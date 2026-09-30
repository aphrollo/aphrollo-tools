package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// languageOnlyRepo carries one language row of its own with its fixtures, and
// no law at all.
func languageOnlyRepo(t *testing.T, masked string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ratchet", "languages", "lua.toml"),
		"name = \"lua\"\nextensions = [\".lua\"]\n[comments]\nline = [\"--\"]\n[string.double]\nopen = '\"'\nescape = \"backslash\"\n")
	writeFile(t, filepath.Join(root, ".ratchet", "fixtures", "languages", "lua", "a.lua"), "x = \"s\" -- it's\n")
	writeFile(t, filepath.Join(root, ".ratchet", "fixtures", "languages", "lua", "a.lua.masked"), masked)
	return root
}

func TestRatchetTest_ProvesARepositoryLanguageRowWithNoLawsAtAll(t *testing.T) {
	root := languageOnlyRepo(t, "x = \" \"        \n")
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "test", "--repo", root}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if want := "ratchet: language/lua ok (1 fixture file(s))\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestRatchetTest_ALanguageRowWhoseFixtureDisagreesFails(t *testing.T) {
	root := languageOnlyRepo(t, "x = \"s\" -- it's\n")
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "test", "--repo", root}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "ratchet: language/lua FAILED — .ratchet/fixtures/languages/lua/a.lua.masked: line 1") {
		t.Errorf("stdout = %q, want the failing answer named", out.String())
	}
}

func TestRatchetTest_LanguageVerdictsAreInTheJSONToo(t *testing.T) {
	root := languageOnlyRepo(t, "x = \" \"        \n")
	var out, errb bytes.Buffer
	if code := Run([]string{"ratchet", "test", "--repo", root, "--format", "json"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	var results []ratchet.FixtureResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Law != "language/lua" || results[0].HitFiles != 1 {
		t.Errorf("results = %+v", results)
	}
}

func TestRatchetTest_ARepositoryWithNeitherLawsNorLanguagesHasNothingToProve(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "test", "--repo", t.TempDir()}, strings.NewReader(""), &out, &errb)
	if code != 0 || !strings.Contains(out.String(), "ratchet: no laws in") {
		t.Errorf("exit = %d stdout = %q", code, out.String())
	}
}
