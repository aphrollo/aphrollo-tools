package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --dry is the flag: the baseline keeps the site a fix removed.
func TestRatchetCheck_DryWritesNoBaseline(t *testing.T) {
	root := lawRepo(t)
	writeFile(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = 1;\n")
	want := readFile(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"))

	var out, errb bytes.Buffer
	if code := Run([]string{"ratchet", "check", "--repo", root, "--no-cache", "--dry"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, out.String(), errb.String())
	}
	if got := readFile(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt")); got != want {
		t.Errorf("baseline = %q, want it untouched by --dry (%q)", got, want)
	}
}

// --no-tighten stays for one release with the same meaning.
func TestRatchetCheck_NoTightenIsStillTheSameAsDry(t *testing.T) {
	root := lawRepo(t)
	writeFile(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = 1;\n")
	want := readFile(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"))

	var out, errb bytes.Buffer
	if code := Run([]string{"ratchet", "check", "--repo", root, "--no-cache", "--no-tighten"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, out.String(), errb.String())
	}
	if got := readFile(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt")); got != want {
		t.Errorf("baseline = %q, want it untouched by --no-tighten (%q)", got, want)
	}
}

func TestRatchetCheck_AdoptDryPrintsTheRowsAndWritesNothing(t *testing.T) {
	root := adoptRepo(t)
	var out, errb bytes.Buffer

	code := Run([]string{"ratchet", "check", "--repo", root, "--adopt", "nan-guard", "--dry"}, strings.NewReader(""), &out, &errb)

	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "would adopt nan-guard — 1 row(s)") {
		t.Errorf("output must say what it would adopt: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt")); err == nil {
		t.Error("--adopt --dry wrote the baseline")
	}
}

func TestRatchetInit_DryListsTheFilesAndWritesNone(t *testing.T) {
	root := t.TempDir()
	var out, errb bytes.Buffer

	code := Run([]string{"ratchet", "init", "--repo", root, "--preset", "common",
		"--param", `pattern=^func (Test[A-Za-z0-9_]+)\(`, "--param", "prefixes=BORLD",
		"--param", `include="**/*_test.go"`, "--dry"}, strings.NewReader(""), &out, &errb)

	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "[would write] common/comment_hygiene") {
		t.Errorf("output must name each law it would write: %q", out.String())
	}
	if strings.Contains(out.String(), "[write]") {
		t.Errorf("a dry run reported a write: %q", out.String())
	}
	if !strings.Contains(out.String(), "ratchet init: 10 written, 0 skipped, 0 missing params") {
		t.Errorf("the summary must count what would be written: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".ratchet")); err == nil {
		t.Error("--dry created .ratchet")
	}
}

func TestRatchetInit_UnknownFlagIsRefused(t *testing.T) {
	var out, errb bytes.Buffer

	code := Run([]string{"ratchet", "init", "--repo", t.TempDir(), "--preset", "common", "--bogus"}, strings.NewReader(""), &out, &errb)

	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr: %s", code, errb.String())
	}
}

func TestRatchetInit_StrayArgumentIsRefused(t *testing.T) {
	var out, errb bytes.Buffer

	code := Run([]string{"ratchet", "init", "stray", "--repo", t.TempDir(), "--preset", "common"}, strings.NewReader(""), &out, &errb)

	if code != 2 || !strings.Contains(errb.String(), `unexpected argument "stray"`) {
		t.Fatalf("exit %d, stderr %q; want 2 naming the stray argument", code, errb.String())
	}
}
