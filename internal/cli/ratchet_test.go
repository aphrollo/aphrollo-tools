package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// lawRepo is a repo carrying one deny law and its baseline, plus one file that
// already offends at the baselined ceiling.
func lawRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), `
name = "nan-guard"
description = "A float clamp is not a NaN guard"
severity = "deny"
escape = "// nan-safe:"
baseline = ".ratchet/baselines/nan-guard.txt"

[scope]
include = ["crates/**/*.rs"]
exclude = ["**/target/**"]

[matcher]
kind = "regex-absent"
pattern = "\\.clamp\\("
`)
	writeFile(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a/src/lib.rs | let a = x.clamp(0.0, 1.0);\n")
	writeFile(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")
	return root
}

func TestRatchetCheckIsSilentAndZeroOnACleanTree(t *testing.T) {
	root := lawRepo(t)
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "check", "--repo", root, "--no-cache"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "1 law") {
		t.Errorf("a clean run must still say what it checked: %q", out.String())
	}
}

func TestRatchetCheckExitsOneAndNamesTheRegression(t *testing.T) {
	root := lawRepo(t)
	writeFile(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")

	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "check", "--repo", root, "--no-cache"}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String() + errb.String()
	for _, want := range []string{"nan-guard:", "crates/a/src/lib.rs:2", "escape: // nan-safe:"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q does not carry %q", got, want)
		}
	}
}

func TestRatchetCheckJSONCarriesTheFindings(t *testing.T) {
	root := lawRepo(t)
	writeFile(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")

	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "check", "--repo", root, "--no-cache", "--format", "json"},
		strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr: %s)", code, errb.String())
	}
	var doc struct {
		Findings []struct {
			Law, File, Severity string
			Line                int
		}
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if len(doc.Findings) != 1 || doc.Findings[0].Law != "nan-guard" || doc.Findings[0].Line != 2 {
		t.Errorf("findings = %+v", doc.Findings)
	}
}

func TestRatchetCheckJudgesProposedContentFromAFile(t *testing.T) {
	root := lawRepo(t)
	content := filepath.Join(t.TempDir(), "proposed.rs")
	writeFile(t, content, "let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")

	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "check", "--repo", root, "--no-cache",
		"--proposed", "crates/a/src/lib.rs=" + content}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 — the proposal introduces a new site\n%s%s", code, out.String(), errb.String())
	}
	if got := readFile(t, filepath.Join(root, "crates", "a", "src", "lib.rs")); strings.Contains(got, "y.clamp") {
		t.Error("--proposed must not touch the file on disk")
	}
}

func TestRatchetCheckSaysSoWhenARepoHasNoLaws(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "check", "--repo", t.TempDir()}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "no laws") {
		t.Errorf("output = %q, want a plain 'no laws' line", out.String())
	}
}

func TestRatchetCheckTightensTheBaselineByDefault(t *testing.T) {
	root := lawRepo(t)
	writeFile(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = 1;\n")

	var out, errb bytes.Buffer
	if code := Run([]string{"ratchet", "check", "--repo", root, "--no-cache"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, out.String(), errb.String())
	}
	if got := readFile(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt")); got != "" {
		t.Errorf("baseline = %q, want the fixed site dropped", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRatchetTestRunsEveryLawAgainstItsFixtures(t *testing.T) {
	root := lawRepo(t)
	fixtures := filepath.Join(root, ".ratchet", "fixtures", "nan-guard")
	writeFile(t, filepath.Join(fixtures, "hit", "bare.rs"), "let b = x.clamp(0.0, 1.0);\n")
	writeFile(t, filepath.Join(fixtures, "expected.txt"), "bare.rs:1\n")
	writeFile(t, filepath.Join(fixtures, "clean", "guarded.rs"), "let a = numeric::clamp_or(x, 0.0, 1.0, 0.0);\n")

	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "test", "--repo", root}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "nan-guard") {
		t.Errorf("output must name each law it proved: %q", out.String())
	}
}

func TestRatchetTestFailsALawWithNoFixtures(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "test", "--repo", lawRepo(t)}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out.String()+errb.String(), "catches nothing") {
		t.Errorf("output = %q%q", out.String(), errb.String())
	}
}

// `gate init` writes the operating instructions into the repo's CLAUDE.md —
// the one file a session always reads — and a second init changes nothing.
func TestGateInitWritesTheManagedClaudeMDBlockIdempotently(t *testing.T) {
	isolateGit(t)
	repo := t.TempDir()
	gitInitRepo(t, repo)
	claude := filepath.Join(repo, "CLAUDE.md")
	writeFile(t, claude, "# Project\n\nGuidance.\n")

	cfg := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "githooks")
	shims := filepath.Join(t.TempDir(), "bin", "cargo-queue")
	args := []string{"gate", "init", "--config-dir", cfg, "--bin", "/usr/local/bin/aphrollo",
		"--git-hooks-dir", hooks, "--cargo-shim-dir", shims}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })

	var out, errb bytes.Buffer
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("init exit = %d\n%s%s", code, out.String(), errb.String())
	}
	first := readFile(t, claude)
	if !strings.Contains(first, "<!-- aphrollo:begin -->") || !strings.Contains(first, "Guidance.") {
		t.Fatalf("CLAUDE.md = %q", first)
	}
	if !strings.Contains(out.String(), "managed block") {
		t.Errorf("init must say it wrote the block: %q", out.String())
	}

	out.Reset()
	errb.Reset()
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("second init exit = %d\n%s%s", code, out.String(), errb.String())
	}
	if second := readFile(t, claude); second != first {
		t.Error("a second init rewrote CLAUDE.md — the block must be byte-identical")
	}
	if strings.Count(readFile(t, claude), "<!-- aphrollo:begin -->") != 1 {
		t.Error("the block was duplicated")
	}
}

func gitInitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
}
