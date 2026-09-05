package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
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

// TestRatchetCheckCLI_BaseFlagReachesOptions proves `--base` is wired through
// to ratchet.Options.Base, via the ratchetCheckFn seam (mirroring the tdd
// package's own ratchetCheckFn) rather than a real git ref, since the point
// here is the CLI's plumbing, not the diff-scoped law itself.
func TestRatchetCheckCLI_BaseFlagReachesOptions(t *testing.T) {
	root := lawRepo(t)
	original := ratchetCheckFn
	t.Cleanup(func() { ratchetCheckFn = original })
	var got ratchet.Options
	ratchetCheckFn = func(opts ratchet.Options) (ratchet.Result, error) {
		got = opts
		return ratchet.Result{}, nil
	}

	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "check", "--repo", root, "--base", "HEAD~1"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, errb.String())
	}
	if got.Base != "HEAD~1" {
		t.Errorf("Options.Base = %q, want %q", got.Base, "HEAD~1")
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
	writeFile(t, filepath.Join(fixtures, "hit", "crates", "a", "src", "bare.rs"), "let b = x.clamp(0.0, 1.0);\n")
	writeFile(t, filepath.Join(fixtures, "expected.txt"), "crates/a/src/bare.rs:1\n")
	writeFile(t, filepath.Join(fixtures, "clean", "crates", "a", "src", "guarded.rs"), "let a = numeric::clamp_or(x, 0.0, 1.0, 0.0);\n")

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

// TestRatchetInit_WritesPresetsAndCheckRunsClean is the acceptance case: init
// into a fresh repo, then check must find zero regressions — the presets it
// copied are self-contained, valid laws from the first run.
func TestRatchetInit_WritesPresetsAndCheckRunsClean(t *testing.T) {
	root := t.TempDir()
	// dev_instrument_registry's registry file is a hardcoded convention path,
	// not a parameter — an empty one is a legitimately empty registry.
	writeFile(t, filepath.Join(root, "docs", "dev_instruments.md"), "")

	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "init", "--repo", root, "--preset", "common",
		"--param", `pattern=^func (Test[A-Za-z0-9_]+)\(`, "--param", "prefixes=BORLD",
		"--param", `include="**/*_test.go"`}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "[write] common/comment_hygiene") {
		t.Errorf("output must name each law it wrote: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".ratchet", "laws", "comment_hygiene.toml")); err != nil {
		t.Fatalf("comment_hygiene.toml was not written: %v", err)
	}
	// common has 10 presets, every required param supplied (test_removed
	// needs its own pattern and include, shared with comment_hygiene's
	// pattern slot): every one is written, none skipped or missing — the
	// exact tally, not just a substring, so a wrong increment/decrement on
	// the counters shows up.
	if !strings.Contains(out.String(), "ratchet init: 10 written, 0 skipped, 0 missing params") {
		t.Errorf("summary line wrong: %q", out.String())
	}

	out.Reset()
	errb.Reset()
	code = Run([]string{"ratchet", "check", "--repo", root, "--no-cache"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("check exit = %d, want 0 (clean)\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

// TestRatchetInit_IsIdempotent proves a second run over an already-adopted
// preset set writes nothing and reports [skip], never clobbering a local edit.
func TestRatchetInit_IsIdempotent(t *testing.T) {
	root := t.TempDir()
	first := []string{"ratchet", "init", "--repo", root, "--preset", "common",
		"--param", `pattern=^func (Test[A-Za-z0-9_]+)\(`, "--param", "prefixes=BORLD",
		"--param", `include="**/*_test.go"`}
	var out, errb bytes.Buffer
	if code := Run(first, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("first init: exit = %d\n%s%s", code, out.String(), errb.String())
	}

	out.Reset()
	errb.Reset()
	code := Run(first, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("second init: exit = %d\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "[skip] common/comment_hygiene") {
		t.Errorf("a repeat run must skip what is already there: %q", out.String())
	}
	if strings.Contains(out.String(), "[write]") {
		t.Errorf("a repeat run must write nothing: %q", out.String())
	}
	// all 10 already exist: every one skipped, none written or missing.
	if !strings.Contains(out.String(), "ratchet init: 0 written, 10 skipped, 0 missing params") {
		t.Errorf("summary line wrong: %q", out.String())
	}
}

// TestRatchetInit_RefusesAPresetWithAnUnfilledParam is the RED case: no
// --param means comment_hygiene's {{pattern}} slot never gets filled, and
// init must refuse to write a law with a literal template slot in it.
func TestRatchetInit_RefusesAPresetWithAnUnfilledParam(t *testing.T) {
	root := t.TempDir()
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "init", "--repo", root, "--preset", "common"}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "[skip] common/comment_hygiene") || !strings.Contains(out.String(), "pattern") {
		t.Errorf("output must name the preset and the missing param: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".ratchet", "laws", "comment_hygiene.toml")); err == nil {
		t.Error("a law missing a required param must never be written")
	}
	if !strings.Contains(out.String(), "[skip] common/test_removed — missing --param include=<value> --param pattern=<value>") {
		t.Errorf("output must name test_removed and both of its missing params: %q", out.String())
	}
	// comment_hygiene and dev_instrument_registry need a param each,
	// test_removed needs two (pattern and include) and goes missing too;
	// the other 7 of the 10 common presets need none and are written.
	if !strings.Contains(out.String(), "ratchet init: 7 written, 0 skipped, 3 missing params") {
		t.Errorf("summary line wrong: %q", out.String())
	}
}

// TestRatchetCheck_WarnsWhenALocalMatcherDriftsFromItsPreset proves the OTHER
// direction: a law that extends a preset but was hand-forked away from it is
// flagged by name, never silently treated as still in sync.
func TestRatchetCheck_WarnsWhenALocalMatcherDriftsFromItsPreset(t *testing.T) {
	root := t.TempDir()
	// Written directly through the ratchet package rather than `ratchet init
	// --preset rust`, which would also copy the group's dep-graph-forbids
	// presets — those run a real `cargo metadata` at check time and have
	// nothing to do with what this test is proving.
	raw, err := ratchet.LoadPresetText("rust", "nan_guard")
	if err != nil {
		t.Fatalf("LoadPresetText: %v", err)
	}
	rendered, missing := ratchet.RenderPresetText(raw, nil)
	if len(missing) != 0 {
		t.Fatalf("nan_guard takes no params, missing = %v", missing)
	}
	final := ratchet.WithExtends(rendered, "rust", "nan_guard", nil, nil)
	nanGuard := filepath.Join(root, ".ratchet", "laws", "nan_guard.toml")
	writeFile(t, nanGuard, final)

	forked := strings.Replace(final, `pattern = "\\.clamp\\("`, `pattern = "\\.forked\\("`, 1)
	if forked == final {
		t.Fatal("test setup: the pattern line was not found to fork")
	}
	writeFile(t, nanGuard, forked)

	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "check", "--repo", root, "--no-cache"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("a matcher fork is a warning, never a block: exit = %d\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "nan_guard") || !strings.Contains(errb.String(), "preset:rust/nan_guard") {
		t.Errorf("stderr must name the law and its preset: %q", errb.String())
	}
}

// adoptRepo is a git repo carrying a committed law with no baseline yet,
// then one offending file staged/committed on top.
func adoptRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	isolateGit(t)
	gitInitRepo(t, root)
	writeFile(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), `
name = "nan-guard"
description = "A float clamp is not a NaN guard"
severity = "deny"
baseline = ".ratchet/baselines/nan-guard.txt"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "regex-absent"
pattern = "\\.clamp\\("
`)
	writeFile(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")
	gitCommitAll(t, root, "seed")
	return root
}

// TestRatchetCheck_AdoptWritesTheFirstBaseline is the "a new deny law with
// hits" case from the escape: no baseline file exists yet, so adoption is
// allowed regardless of whether the law changed since HEAD.
func TestRatchetCheck_AdoptWritesTheFirstBaseline(t *testing.T) {
	root := adoptRepo(t)
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "check", "--repo", root, "--adopt", "nan-guard"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "adopted nan-guard") {
		t.Errorf("output must say what it adopted: %q", out.String())
	}
	data, err := os.ReadFile(filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"))
	if err != nil {
		t.Fatalf("baseline was not written: %v", err)
	}
	if !strings.Contains(string(data), "crates/a/src/lib.rs") {
		t.Errorf("baseline = %q", string(data))
	}
}

// TestRatchetCheck_AdoptRefusesAnUnchangedLaw proves the refusal: once a
// baseline exists and the law's .toml is untouched since HEAD, --adopt on a
// freshly widened tree must not silently raise the ceiling.
func TestRatchetCheck_AdoptRefusesAnUnchangedLaw(t *testing.T) {
	root := adoptRepo(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"ratchet", "check", "--repo", root, "--adopt", "nan-guard"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("first adopt: exit = %d\n%s%s", code, out.String(), errb.String())
	}
	gitCommitAll(t, root, "adopt")

	// A second offender appears with no change to the law itself.
	writeFile(t, filepath.Join(root, "crates", "b", "src", "lib.rs"), "let b = y.clamp(0.0, 1.0);\n")

	out.Reset()
	errb.Reset()
	code := Run([]string{"ratchet", "check", "--repo", root, "--adopt", "nan-guard"}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 — the law is unchanged, adoption must refuse\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "nan-guard") {
		t.Errorf("stderr must name the law: %q", errb.String())
	}
}

func TestRatchetPresets_ListsGroupsAndParams(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "presets"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, out.String(), errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "rust/nan_guard\n") {
		t.Errorf("a param-free preset must list with no trailing params text: %q", got)
	}
	if !strings.Contains(got, "common/comment_hygiene  params: pattern") {
		t.Errorf("a parameterized preset must list its params: %q", got)
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

func gitCommitAll(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
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

// `gate init` leaves the law schema in the repo it initialises, so a law can
// cite `.ratchet/README.md` instead of a path on one developer's machine.
func TestGateInitWritesTheLawSpecBesideTheLaws(t *testing.T) {
	isolateGit(t) // init sets core.hooksPath; without this it is the RUNNER's
	repo := t.TempDir()
	gitInitRepo(t, repo)
	writeFile(t, filepath.Join(repo, ".ratchet", "laws", "placeholder.txt"), "")
	t.Chdir(repo)

	args := []string{"gate", "init", "--config-dir", t.TempDir(), "--bin", "/usr/local/bin/aphrollo",
		"--git-hooks-dir", filepath.Join(t.TempDir(), "githooks"),
		"--cargo-shim-dir", filepath.Join(t.TempDir(), "cargo-queue")}

	var out, errb bytes.Buffer
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("init exit = %d\n%s%s", code, out.String(), errb.String())
	}
	spec := readFile(t, filepath.Join(repo, ".ratchet", "README.md"))
	if !strings.Contains(spec, "regex-absent") || !strings.Contains(spec, "baseline") {
		t.Fatalf(".ratchet/README.md must carry the law schema:\n%s", spec[:min(len(spec), 300)])
	}
	if !strings.Contains(out.String(), "law spec") {
		t.Errorf("init must say it wrote the spec: %q", out.String())
	}

	out.Reset()
	if code := Run(args, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("second init exit = %d", code)
	}
	if again := readFile(t, filepath.Join(repo, ".ratchet", "README.md")); again != spec {
		t.Error("a second init must leave the spec byte-identical")
	}
	if strings.Contains(out.String(), "law spec") {
		t.Errorf("an unchanged spec is not news: %q", out.String())
	}
}

// TestRatchetCheck_AdoptRefusesACosmeticLawEditThatDoesNotChangeTheMatcher
// proves the changed-since-HEAD guard is a SEMANTIC diff of [matcher],
// [scope] and severity — not a raw byte diff of the whole .toml. Editing
// only the law's description must not unlock adoption of an unrelated,
// already-present violation that has nothing to do with that edit.
func TestRatchetCheck_AdoptRefusesACosmeticLawEditThatDoesNotChangeTheMatcher(t *testing.T) {
	root := adoptRepo(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"ratchet", "check", "--repo", root, "--adopt", "nan-guard"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("first adopt: exit = %d\n%s%s", code, out.String(), errb.String())
	}
	gitCommitAll(t, root, "adopt")

	// A second offender appears, unrelated to the law's own rule.
	writeFile(t, filepath.Join(root, "crates", "b", "src", "lib.rs"), "let b = y.clamp(0.0, 1.0);\n")

	// A purely cosmetic edit to the law: description text only, [matcher]/
	// [scope]/severity untouched.
	lawPath := filepath.Join(root, ".ratchet", "laws", "nan-guard.toml")
	cosmetic := strings.Replace(
		string(mustReadFile(t, lawPath)),
		`description = "A float clamp is not a NaN guard"`,
		`description = "A float clamp is not a NaN guard (reworded)"`,
		1)
	writeFile(t, lawPath, cosmetic)

	out.Reset()
	errb.Reset()
	code := Run([]string{"ratchet", "check", "--repo", root, "--adopt", "nan-guard"}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 — a description-only edit must not unlock adoption of an unrelated violation\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "nan-guard") {
		t.Errorf("stderr must name the law: %q", errb.String())
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
