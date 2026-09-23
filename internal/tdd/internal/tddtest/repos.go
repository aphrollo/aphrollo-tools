package tddtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// BaselineRepo commits one counted baseline, then leaves `after` staged.
func BaselineRepo(t *testing.T, path, before, after string) string {
	t.Helper()
	root := t.TempDir()
	GitInit(t, root)
	MustWrite(t, filepath.Join(root, path), before)
	GitAddAll(t, root)
	CommitAll(t, root)
	MustWrite(t, filepath.Join(root, path), after)
	GitAddAll(t, root)
	return root
}

// BashSuiteRoot makes a directory findRootFrom resolves as a project root,
// with no git and no suite actually runnable — DecideBashSuite never runs
// anything, so a bare go.mod marker is enough.
func BashSuiteRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	Write(t, dir, "go.mod", "module fixture\n\ngo 1.22\n")
	return dir
}

// CargoCrate builds a crate root whose manifest names pkg.
func CargoCrate(t *testing.T, pkg string) string {
	t.Helper()
	root := t.TempDir()
	Write(t, root, "Cargo.toml", "[package]\nname = \""+pkg+"\"\nversion = \"0.1.0\"\n")
	Write(t, root, "src/lib.rs", "pub fn x() {}\n")
	return root
}

// CommitInitial turns a fresh git dir into a repo with one commit, so a
// worktree can be added against it.
func CommitInitial(t *testing.T, repo string) {
	t.Helper()
	Write(t, repo, "main.go", "package main\n")
	GitDo(t, repo, "add", "-A")
	GitDo(t, repo, "commit", "-q", "-m", "init")
}

// DeclareMutantsAtMerge is the one key that turns the stage on.
func DeclareMutantsAtMerge(t *testing.T, root string) {
	t.Helper()
	Write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-merge = true\n")
}

// GoPrimaryWithLane builds the shape a shared box actually has: a merge-only
// primary checkout on main, plus one linked lane worktree, both Go projects.
func GoPrimaryWithLane(t *testing.T) (primary, lane string) {
	t.Helper()
	primary = MakeGoRepo(t)
	GitDo(t, primary, "checkout", "-q", "-B", "main")
	lane = filepath.Join(t.TempDir(), "lane")
	GitDo(t, primary, "worktree", "add", "-q", "-b", "lane/x", lane)
	return primary, lane
}

// PrimaryRepo builds a repo on `main` with one linked worktree, and returns
// the primary checkout and the linked worktree.
func PrimaryRepo(t *testing.T) (primary, linked string) {
	t.Helper()
	return PrimaryRepoNamed(t, "repo")
}

// PrimaryRepoNamed is PrimaryRepo with the primary checkout's directory named.
func PrimaryRepoNamed(t *testing.T, name string) (primary, linked string) {
	t.Helper()
	primary = filepath.Join(t.TempDir(), name)
	GitInit(t, primary)
	GitDo(t, primary, "checkout", "-q", "-B", "main")
	CommitInitial(t, primary)
	linked = filepath.Join(t.TempDir(), "lane")
	GitDo(t, primary, "worktree", "add", "-q", "-b", "lane/x", linked)
	return primary, linked
}

// LawTree is a git repo carrying one deny law whose baseline records the one
// offending site that already exists.
func LawTree(t *testing.T, severity string) string {
	t.Helper()
	root := t.TempDir()
	GitInit(t, root)
	MustWrite(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), `
name = "nan-guard"
description = "A float clamp is not a NaN guard"
severity = "`+severity+`"
escape = "// nan-safe:"
baseline = ".ratchet/baselines/nan-guard.txt"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "regex-absent"
pattern = "\\.clamp\\("
`)
	MustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a/src/lib.rs | let a = x.clamp(0.0, 1.0);\n")
	MustWrite(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")
	return root
}

// LedgerRepo is a cargo repo whose HEAD already carries src/widget.rs with
// production code only.
func LedgerRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := MakeCargoRepo(t)
	Write(t, root, "src/lib.rs", "pub mod widget;\n")
	Write(t, root, "src/widget.rs", LedgerWidgetImpl)
	GitDo(t, root, "add", ".")
	GitDo(t, root, "commit", "-qm", "widget")
	return root
}

// WriteMeasureBase lays down the one-crate workspace both measure fixtures
// start from: a workspace manifest, a crate with a mutable source and a test,
// and a file that is neither.
func WriteMeasureBase(t *testing.T, root string) {
	t.Helper()
	Write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/a\"]\n")
	Write(t, root, "crates/a/Cargo.toml", "[package]\nname = \"a\"\nversion = \"0.1.0\"\n")
	Write(t, root, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a + b }\n")
	Write(t, root, "crates/a/tests/t.rs", "#[test]\nfn t() {}\n")
	Write(t, root, "README.md", "base\n")
}

// MakeForkedRepo commits a base, puts one crate-source change on a `lane`
// branch and returns to trunk. Every fixture built on it starts here and
// differs only in what it does with the lane afterwards. git is the
// package's own git runner, which CurrentBranch reads the trunk with.
func MakeForkedRepo(t *testing.T, git func(dir string, args ...string) (string, error)) (root, trunk string) {
	t.Helper()
	root = t.TempDir()
	GitInit(t, root)
	WriteMeasureBase(t, root)
	GitDo(t, root, "add", ".")
	GitDo(t, root, "commit", "-qm", "base")
	trunk = CurrentBranch(t, root, git)
	GitDo(t, root, "checkout", "-q", "-b", "lane")
	Write(t, root, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a - b }\n")
	GitDo(t, root, "add", ".")
	GitDo(t, root, "commit", "-qm", "lane")
	GitDo(t, root, "checkout", "-q", trunk)
	return root, trunk
}

// MakeGitHubRepo is a committed repo whose origin is on GitHub, so the escape
// loop believes there is somewhere to open an issue.
func MakeGitHubRepo(t *testing.T) string {
	t.Helper()
	root := MakeGoRepo(t)
	GitDo(t, root, "remote", "add", "origin", "https://github.com/o/r.git")
	return root
}

// MakeGoMeasureRepo builds a Go module with a base commit and a lane commit
// that changes a source file.
func MakeGoMeasureRepo(t *testing.T) (root, base string) {
	t.Helper()
	root = MakeGoRepo(t)
	base = strings.TrimSpace(GitOutT(t, root, "rev-parse", "HEAD"))
	Write(t, root, "calc.go", "package m\n\nfunc Add(a, b int) int { return a + b }\n")
	GitDo(t, root, "add", ".")
	GitDo(t, root, "commit", "-qm", "lane")
	return root, base
}

// MakeMeasureRepo builds a one-crate cargo workspace with a base commit and a
// lane commit on top of it, and answers the root and the base sha. pinJobs is
// the package's shard-count override: the count is pinned to ONE so a test
// built on it is about the step it names rather than about how many cores the
// box running the suite has; a test about the sharding pins its own number
// after this call.
func MakeMeasureRepo(t *testing.T, pinJobs func(jobs int, why string) (restore func()), lane map[string]string) (root, base string) {
	t.Helper()
	t.Cleanup(pinJobs(1, "pinned"))
	root = t.TempDir()
	GitInit(t, root)
	WriteMeasureBase(t, root)
	GitDo(t, root, "add", ".")
	GitDo(t, root, "commit", "-qm", "base")
	base = strings.TrimSpace(GitOutT(t, root, "rev-parse", "HEAD"))
	for rel, content := range lane {
		Write(t, root, rel, content)
	}
	GitDo(t, root, "add", ".")
	GitDo(t, root, "commit", "-qm", "lane")
	return root, base
}

// MkGoModule writes a module with a test-less package, and returns its root.
func MkGoModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	Write(t, root, "go.mod", "module example.com/m\n\ngo 1.22\n")
	Write(t, root, filepath.Join("internal", "proc", "proc.go"), "package proc\n\nfunc Spawn() int { return 1 }\n")
	Write(t, root, filepath.Join("internal", "tdd", "tdd.go"), "package tdd\n\nimport \"example.com/m/internal/proc\"\n\nfunc Run() int { return proc.Spawn() }\n")
	return root
}

// UndercoverRepo is a repo whose workspace manifest opts into the check.
func UndercoverRepo(t *testing.T, on bool) string {
	t.Helper()
	root := t.TempDir()
	manifest := "[workspace]\n"
	if on {
		manifest += "[workspace.metadata.aphrollo]\nundercover = true\n"
	}
	Write(t, root, "Cargo.toml", manifest)
	return root
}
