package ratchet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoWithNanGuard builds a tiny consuming repo: one deny law keyed by line
// content, a baseline recording the one site that already exists, and a tree
// with build output that must never be read.
func repoWithNanGuard(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeLaw(t, root, "nan-guard", nanGuardLaw)
	write(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"# one known site\ncrates/a/src/lib.rs | let a = x.clamp(0.0, 1.0);\n")
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")
	write(t, filepath.Join(root, "crates", "a", "target", "junk.rs"), "let j = z.clamp(0.0, 1.0);\n")
	return root
}

func TestCheckIsCleanWhenTheTreeMatchesItsBaseline(t *testing.T) {
	root := repoWithNanGuard(t)
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v", res.Findings)
	}
	if res.Laws != 1 || res.FilesScanned == 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestCheckReportsANewSiteWithItsLineBaselineAndEscape(t *testing.T) {
	root := repoWithNanGuard(t)
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")

	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v", res.Findings)
	}
	f := res.Findings[0]
	if f.Law != "nan-guard" || f.File != "crates/a/src/lib.rs" || f.Line != 2 {
		t.Errorf("finding = %+v", f)
	}
	if f.Baseline != 0 || f.Measured != 1 || f.Escape != "// nan-safe:" {
		t.Errorf("finding = %+v", f)
	}
	if !res.Blocked() {
		t.Error("a deny law's regression must block")
	}
	line := res.Lines()[0]
	for _, want := range []string{"nan-guard:", "crates/a/src/lib.rs:2", "y.clamp(0.0, 1.0)", "baseline 0, now 1", "escape: // nan-safe:"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q does not carry %q", line, want)
		}
	}
}

func TestCheckSkipsExcludedAndGitignoredTrees(t *testing.T) {
	root := repoWithNanGuard(t)
	write(t, filepath.Join(root, ".gitignore"), "generated/\n")
	write(t, filepath.Join(root, "crates", "a", "generated", "gen.rs"), "let g = q.clamp(0.0, 1.0);\n")

	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a gitignored or excluded tree must not be scanned: %+v", res.Findings)
	}
}

func TestCheckTightensTheBaselineWhenASiteIsFixed(t *testing.T) {
	root := repoWithNanGuard(t)
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = numeric::clamp_or(x, 0.0, 1.0, 0.0);\n")

	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 || len(res.Tightened) != 1 {
		t.Fatalf("result = %+v", res)
	}
	got := read(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"))
	if got != "# one known site\n" {
		t.Errorf("baseline = %q — the fixed site must be dropped, the header kept", got)
	}
}

func TestCheckNeverTightensWhileProposedContentIsOverlaid(t *testing.T) {
	root := repoWithNanGuard(t)
	before := read(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"))
	res, err := Check(Options{
		Root:     root,
		Tighten:  true,
		Proposed: map[string]string{"crates/a/src/lib.rs": "let a = 1;\n"},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Tightened) != 0 {
		t.Errorf("a hypothetical tree must never rewrite a baseline: %+v", res.Tightened)
	}
	if read(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt")) != before {
		t.Error("baseline file changed under a proposed overlay")
	}
}

func TestCheckJudgesProposedContentInsteadOfDisk(t *testing.T) {
	root := repoWithNanGuard(t)
	res, err := Check(Options{
		Root:     root,
		Proposed: map[string]string{"crates/a/src/new.rs": "let n = w.clamp(0.0, 1.0);\n"},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].File != "crates/a/src/new.rs" {
		t.Fatalf("a file that exists only in the proposal must be judged: %+v", res.Findings)
	}
}

func TestCheckOnlyRunsTheNamedLaw(t *testing.T) {
	root := repoWithNanGuard(t)
	writeLaw(t, root, "big-file", `
name = "big-file"
description = "modules stay small"
severity = "deny"

[scope]
include = ["crates/**/*.rs"]
exclude = ["**/target/**"]

[matcher]
kind = "line-count"
max = 1
`)
	write(t, filepath.Join(root, "crates", "a", "src", "big.rs"), "let a = 1;\nlet b = 2;\n")
	res, err := Check(Options{Root: root, Only: "big-file"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Laws != 1 {
		t.Fatalf("laws = %d, want only the named one", res.Laws)
	}
	if len(res.Findings) != 1 || res.Findings[0].Law != "big-file" {
		t.Fatalf("findings = %+v", res.Findings)
	}
	if _, err := Check(Options{Root: root, Only: "nope"}); err == nil {
		t.Error("naming a law that does not exist must fail loudly")
	}
}

func TestCheckWarnLawDoesNotBlock(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "no-todo", `
name = "no-todo"
description = "no TODO markers"
severity = "warn"

[scope]
include = ["**/*.go"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	write(t, filepath.Join(root, "main.go"), "// TODO later\n")
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Blocked() {
		t.Fatalf("a warn law reports but never blocks: %+v blocked=%v", res.Findings, res.Blocked())
	}
}

func TestCheckRegistryBothWaysNamesUnregisteredUsesAndStaleEntries(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "env-registry", `
name = "env-registry"
description = "every env switch is registered"
severity = "deny"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "registry-both-ways"
registry_file = ".ratchet/registry/env.txt"
entry_pattern = "^([A-Z][A-Z0-9_]+) \\|"
use_pattern = "env::var\\(\"([A-Z][A-Z0-9_]+)\"\\)"
`)
	write(t, filepath.Join(root, ".ratchet", "registry", "env.txt"),
		"# switches\nBORLD_KNOWN | client | does a thing\nBORLD_GONE | client | nothing reads it\n")
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = env::var(\"BORLD_KNOWN\");\nlet b = env::var(\"BORLD_NEW\");\n")

	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("findings = %+v", res.Lines())
	}
	joined := strings.Join(res.Lines(), "\n")
	if !strings.Contains(joined, "BORLD_NEW") || !strings.Contains(joined, "BORLD_GONE") {
		t.Errorf("both directions must be named:\n%s", joined)
	}
	if !strings.Contains(joined, "crates/a/src/lib.rs:2") {
		t.Errorf("an unregistered use must name where it is read:\n%s", joined)
	}
}

func TestCheckCacheServesARepeatRunAndNoticesAnEditedFile(t *testing.T) {
	root := repoWithNanGuard(t)
	cache := t.TempDir()
	if _, err := Check(Options{Root: root, CacheDir: cache}); err != nil {
		t.Fatal(err)
	}
	second, err := Check(Options{Root: root, CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if second.FilesRead != 0 {
		t.Errorf("a repeat run read %d files — the cache is not serving", second.FilesRead)
	}
	if len(second.Findings) != 0 {
		t.Errorf("a cached run must reach the same verdict: %+v", second.Findings)
	}

	path := filepath.Join(root, "crates", "a", "src", "lib.rs")
	write(t, path, "let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")
	bumpMtime(t, path)
	third, err := Check(Options{Root: root, CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if third.FilesRead == 0 || len(third.Findings) != 1 {
		t.Errorf("an edited file must be re-read and judged: read=%d findings=%+v", third.FilesRead, third.Findings)
	}
}

func TestCheckCacheIsDroppedWhenALawChanges(t *testing.T) {
	root := repoWithNanGuard(t)
	cache := t.TempDir()
	if _, err := Check(Options{Root: root, CacheDir: cache}); err != nil {
		t.Fatal(err)
	}
	writeLaw(t, root, "nan-guard", strings.Replace(nanGuardLaw, `pattern = "\\.clamp\\("`, `pattern = "clamp"`, 1))
	res, err := Check(Options{Root: root, CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesRead == 0 {
		t.Error("a changed law must invalidate the cache, not inherit its verdicts")
	}
}

func bumpMtime(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	future := fi.ModTime().Add(3 * 1e9)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
}

// A narrowed run (the pre-edit path) reads one file, so it can honestly report
// a use nobody registered — but never that a registry line is stale, which
// would call every other file's switches dead.
func TestCheckNarrowedToOneFileReportsUnregisteredUsesButNotStaleEntries(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "env-registry", `
name = "env-registry"
description = "every env switch is registered"
severity = "deny"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "registry-both-ways"
registry_file = ".ratchet/registry/env.txt"
entry_pattern = "^([A-Z][A-Z0-9_]+) \|"
use_pattern = "env::var\(\"([A-Z][A-Z0-9_]+)\"\)"
`)
	write(t, filepath.Join(root, ".ratchet", "registry", "env.txt"), "BORLD_ELSEWHERE | other | read by another file\n")
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = env::var(\"BORLD_NEW\");\n")

	res, err := Check(Options{
		Root:     root,
		Files:    []string{"crates/a/src/lib.rs"},
		Proposed: map[string]string{"crates/a/src/lib.rs": "let a = env::var(\"BORLD_NEW\");\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || !strings.Contains(res.Findings[0].What, "BORLD_NEW") {
		t.Fatalf("findings = %v", res.Lines())
	}
}
