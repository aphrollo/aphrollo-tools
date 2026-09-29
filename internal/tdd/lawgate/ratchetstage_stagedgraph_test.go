package lawgate

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// A dep-graph law asks cargo or go for the RESOLVED graph, and both read the
// manifests and sources off the disk they run in. At commit time the tree
// under judgement is the index, so the query runs over a checkout of it: an
// edit left unstaged is in no commit and must not decide one, and a file the
// commit deletes must not be read back off the disk. The merge gate and a
// plain `ratchet check` judge the real tree.

// goGraphTree is a committed Go module whose package a imports example.com/dep,
// a local module replaced in from ./dep. ./dep2 is the same module with a
// package that reaches example.com/dep/bad, so pointing go.mod's replace at it
// — a go.mod-only edit — is what flips the law's verdict. Package b is the
// module's own forbidden package.
func goGraphTree(t *testing.T) string {
	t.Helper()
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "no-bad.toml"), `
name = "no-bad"
description = "a reaches neither bad package"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "go-dep-graph-forbids"
roots = ["example.com/m/a"]
forbidden = ["example.com/dep/bad", "example.com/m/b"]
`)
	mustWrite(t, filepath.Join(root, "go.mod"), goGraphMod("./dep"))
	mustWrite(t, filepath.Join(root, "a", "a.go"), "package a\n\nimport _ \"example.com/dep\"\n")
	mustWrite(t, filepath.Join(root, "b", "b.go"), "package b\n")
	mustWrite(t, filepath.Join(root, "dep", "go.mod"), "module example.com/dep\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, "dep", "dep.go"), "package dep\n")
	mustWrite(t, filepath.Join(root, "dep2", "go.mod"), "module example.com/dep\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, "dep2", "dep.go"), "package dep\n\nimport _ \"example.com/dep/bad\"\n")
	mustWrite(t, filepath.Join(root, "dep2", "bad", "bad.go"), "package bad\n")
	gitAddAll(t, root)
	commitAll(t, root)
	return root
}

func goGraphMod(replace string) string {
	return "module example.com/m\n\ngo 1.22\n\nrequire example.com/dep v0.0.0\n\nreplace example.com/dep => " + replace + "\n"
}

// requireNoGateWorktree fails when the commit left its checkout of the index
// registered or on disk.
func requireNoGateWorktree(t *testing.T, root string) {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(out), "worktree "); n != 1 {
		t.Errorf("the checkout of the index is still registered:\n%s", out)
	}
	dirs, _ := os.ReadDir(filepath.Join(StateDir(), "failfirst-wt"))
	for _, d := range dirs {
		t.Errorf("the checkout of the index is still on disk at %s", d.Name())
	}
}

func TestCommitRatchetStage_GoGraphIgnoresAnUnstagedGoModEdit(t *testing.T) {
	root := goGraphTree(t)
	mustWrite(t, filepath.Join(root, "go.mod"), goGraphMod("./dep2"))

	if res := ratchetStage("premerge", root); !res.Blocked || !strings.Contains(res.Message, "example.com/dep/bad") {
		t.Fatalf("setup: the real tree must reach example.com/dep/bad, got %+v", res)
	}
	if res := commitRatchetStage(root); res.Blocked {
		t.Fatalf("the go.mod edit is not staged, the commit does not reach anything forbidden: %s", res.Message)
	}
	requireNoGateWorktree(t, root)
}

func TestCommitRatchetStage_GoGraphJudgesAStagedGoModEdit(t *testing.T) {
	root := goGraphTree(t)
	mustWrite(t, filepath.Join(root, "go.mod"), goGraphMod("./dep2"))
	gitAddAll(t, root)

	res := commitRatchetStage(root)
	if !res.Blocked || !strings.Contains(res.Message, "example.com/dep/bad") {
		t.Fatalf("a staged go.mod edit that reaches a forbidden package must reject: %+v", res)
	}
}

func TestCommitRatchetStage_GoGraphJudgesAStagedEditUndoneOnlyOnDisk(t *testing.T) {
	root := goGraphTree(t)
	mustWrite(t, filepath.Join(root, "go.mod"), goGraphMod("./dep2"))
	gitAddAll(t, root)
	mustWrite(t, filepath.Join(root, "go.mod"), goGraphMod("./dep"))

	res := commitRatchetStage(root)
	if !res.Blocked || !strings.Contains(res.Message, "example.com/dep/bad") {
		t.Fatalf("a staged edit undone on disk only still lands in the commit and must reject: %+v", res)
	}
}

func TestCommitRatchetStage_GoGraphIgnoresAFileDeletedFromTheIndex(t *testing.T) {
	root := goGraphTree(t)
	mustWrite(t, filepath.Join(root, "a", "extra.go"), "package a\n\nimport _ \"example.com/m/b\"\n")
	gitAddAll(t, root)
	commitAll(t, root)
	gitDo(t, root, "rm", "-q", "--cached", "a/extra.go")

	if res := ratchetStage("premerge", root); !res.Blocked || !strings.Contains(res.Message, "example.com/m/b") {
		t.Fatalf("setup: the real tree must still reach example.com/m/b, got %+v", res)
	}
	if res := commitRatchetStage(root); res.Blocked {
		t.Fatalf("the commit deletes a/extra.go, so its import is in no commit: %s", res.Message)
	}
	requireNoGateWorktree(t, root)
}

// fakeCargo installs a `cargo` (through $CARGO, which the matcher honours)
// that answers `cargo metadata` from the manifest it is pointed at: package a
// always depends on ok, and on bad as well when its Cargo.toml names bad.
// Every call appends its argv and CARGO_TARGET_DIR to the returned log.
func fakeCargo(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		// skip-ok: the fake cargo is a POSIX shell script; every assertion runs on each POSIX box.
		t.Skip("the fake cargo is a POSIX shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "cargo.log")
	script := `#!/bin/sh
manifest=
prev=
for a in "$@"; do
  if [ "$prev" = "--manifest-path" ]; then manifest=$a; fi
  prev=$a
done
printf 'args=%s\n' "$*" >> '` + log + `'
printf 'target=%s\n' "$CARGO_TARGET_DIR" >> '` + log + `'
extra=
if grep -q bad "$manifest"; then extra=',{"pkg":"bad","dep_kinds":[{"kind":null}]}'; fi
printf '{"packages":[{"id":"a","name":"a"},{"id":"ok","name":"ok"},{"id":"bad","name":"bad"}],"workspace_members":["a"],"resolve":{"nodes":[{"id":"a","deps":[{"pkg":"ok","dep_kinds":[{"kind":null}]}%s]}]}}\n' "$extra"
`
	if err := proc.WriteExecutable(filepath.Join(dir, "cargo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARGO", filepath.Join(dir, "cargo"))
	return log
}

// cargoGraphTree is a committed repo whose law forbids package a reaching bad,
// with a Cargo.lock when lock is set.
func cargoGraphTree(t *testing.T, lock bool) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "no-bad.toml"), `
name = "no-bad"
description = "a never reaches bad"
severity = "deny"

[scope]
include = ["**/Cargo.toml"]

[matcher]
kind = "dep-graph-forbids"
roots = ["a"]
forbidden = ["bad"]
`)
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"a\"\n")
	if lock {
		mustWrite(t, filepath.Join(root, "Cargo.lock"), "version = 4\n")
	}
	gitAddAll(t, root)
	commitAll(t, root)
	return root
}

const cargoBadDep = "[package]\nname = \"a\"\n\n[dependencies]\nbad = { path = \"bad\" }\n"

func readCargoLog(t *testing.T, log string) string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("cargo never ran: %v", err)
	}
	return string(data)
}

func TestCommitRatchetStage_CargoGraphIgnoresAnUnstagedManifestEdit(t *testing.T) {
	log := fakeCargo(t)
	root := cargoGraphTree(t, true)
	mustWrite(t, filepath.Join(root, "Cargo.toml"), cargoBadDep)

	if res := ratchetStage("premerge", root); !res.Blocked || !strings.Contains(res.Message, "a->bad") {
		t.Fatalf("setup: the real tree must reach bad, got %+v", res)
	}
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if res := commitRatchetStage(root); res.Blocked {
		t.Fatalf("the Cargo.toml edit is not staged, the commit does not reach bad: %s", res.Message)
	}
	got := readCargoLog(t, log)
	if strings.Contains(got, root) {
		t.Errorf("cargo ran over the real tree, want a checkout of the index:\n%s", got)
	}
	if !strings.Contains(got, " --offline") {
		t.Errorf("the lockfile is unchanged, so the query must run --offline:\n%s", got)
	}
	target := ""
	for line := range strings.Lines(got) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "target="); ok {
			target = v
		}
	}
	if target == "" || strings.HasPrefix(target, root) || !strings.Contains(got, filepath.Dir(target)+string(filepath.Separator)+"Cargo.toml") {
		t.Errorf("CARGO_TARGET_DIR = %q, want a dir of the checkout's own:\n%s", target, got)
	}
	requireNoGateWorktree(t, root)
}

func TestCommitRatchetStage_CargoGraphJudgesAStagedManifestEdit(t *testing.T) {
	fakeCargo(t)
	root := cargoGraphTree(t, true)
	mustWrite(t, filepath.Join(root, "Cargo.toml"), cargoBadDep)
	gitAddAll(t, root)

	res := commitRatchetStage(root)
	if !res.Blocked || !strings.Contains(res.Message, "a->bad") {
		t.Fatalf("a staged Cargo.toml edit that reaches bad must reject: %+v", res)
	}
}

func TestCommitRatchetStage_CargoGraphGoesOnlineForAChangedLockfile(t *testing.T) {
	log := fakeCargo(t)
	root := cargoGraphTree(t, true)
	mustWrite(t, filepath.Join(root, "Cargo.lock"), "version = 4\n# changed\n")
	gitAddAll(t, root)
	mustWrite(t, filepath.Join(root, "notes.txt"), "untracked\n")

	if res := commitRatchetStage(root); res.Blocked {
		t.Fatalf("nothing reaches bad: %s", res.Message)
	}
	if got := readCargoLog(t, log); strings.Contains(got, "--offline") || strings.Contains(got, root) {
		t.Errorf("a changed lockfile may need the registry, and the untracked file forces a checkout:\n%s", got)
	}
}

func TestCommitRatchetStage_CargoGraphGoesOnlineWithNoLockfile(t *testing.T) {
	log := fakeCargo(t)
	root := cargoGraphTree(t, false)
	mustWrite(t, filepath.Join(root, "notes.txt"), "untracked\n")

	if res := commitRatchetStage(root); res.Blocked {
		t.Fatalf("nothing reaches bad: %s", res.Message)
	}
	if got := readCargoLog(t, log); strings.Contains(got, "--offline") || strings.Contains(got, root) {
		t.Errorf("with no lockfile there is nothing to resolve offline from:\n%s", got)
	}
}

// A tree whose disk is exactly the index needs no checkout: the query runs
// over the real tree, as it always has.
func TestCommitRatchetStage_CargoGraphQueriesTheTreeItselfWhenItIsTheIndex(t *testing.T) {
	log := fakeCargo(t)
	root := cargoGraphTree(t, true)
	mustWrite(t, filepath.Join(root, "Cargo.toml"), cargoBadDep)
	gitAddAll(t, root)

	commitRatchetStage(root)
	if got := readCargoLog(t, log); !strings.Contains(got, filepath.Join(root, "Cargo.toml")) {
		t.Errorf("the disk is the index, the query must run over the tree itself:\n%s", got)
	}
}

// scopeFileTree is a committed repo whose law names one file outright.
func scopeFileTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "spec.toml"), `
name = "spec"
description = "no TODO in the spec"
severity = "deny"

[scope]
include = ["docs/spec.md"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	mustWrite(t, filepath.Join(root, "docs", "spec.md"), "the spec\n")
	gitAddAll(t, root)
	commitAll(t, root)
	return root
}

func TestRatchetStage_ScopeFileDeletedFromTheIndexIsMissing(t *testing.T) {
	root := scopeFileTree(t)
	gitDo(t, root, "rm", "-q", "--cached", "docs/spec.md")

	res := ratchetStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "docs/spec.md is named by scope.include but is not there") {
		t.Fatalf("the commit deletes docs/spec.md, the copy left on disk must not stand in for it: %+v", res)
	}
}

func TestRatchetStage_ScopeFileDeletedOnlyOnDiskIsStillThere(t *testing.T) {
	root := scopeFileTree(t)
	if err := os.Remove(filepath.Join(root, "docs", "spec.md")); err != nil {
		t.Fatal(err)
	}

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("the deletion is not staged, the commit still carries docs/spec.md: %s", res.Message)
	}
}

// With no HEAD there is nothing to check the index out onto: the commit is
// refused with that cause, never judged over the disk instead.
func TestCommitRatchetStage_NoHeadToCheckOutRefusesWithTheCause(t *testing.T) {
	fakeCargo(t)
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "no-bad.toml"), "name = \"no-bad\"\ndescription = \"a never reaches bad\"\nseverity = \"deny\"\n\n[scope]\ninclude = [\"**/Cargo.toml\"]\n\n[matcher]\nkind = \"dep-graph-forbids\"\nroots = [\"a\"]\nforbidden = [\"bad\"]\n")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"a\"\n")

	res := commitRatchetStage(root)
	if !res.Blocked || !strings.Contains(res.Message, "the tree to query the dependency graph in") || strings.Contains(res.Message, "reading the staged tree") {
		t.Fatalf("a checkout that could not be made must refuse and say so: %+v", res)
	}
	requireNoGateWorktree(t, root)
}

// An index git cannot write a tree from (unmerged entries) cannot be read
// into the checkout, and the refusal says which step failed.
func TestCommitRatchetStage_UnwritableIndexRefusesWithTheCause(t *testing.T) {
	fakeCargo(t)
	root := cargoGraphTree(t, true)
	gitDo(t, root, "checkout", "-q", "-b", "side")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"a\"\nversion = \"1.0.0\"\n")
	gitAddAll(t, root)
	commitAll(t, root)
	gitDo(t, root, "checkout", "-q", "-")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"a\"\nversion = \"2.0.0\"\n")
	gitAddAll(t, root)
	commitAll(t, root)
	cmd := exec.Command("git", "-c", "core.hooksPath=", "merge", "-q", "side")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("setup: the merge must conflict:\n%s", out)
	}

	res := commitRatchetStage(root)
	if !res.Blocked || !strings.Contains(res.Message, "reading the staged tree into") {
		t.Fatalf("an index that cannot be read into the checkout must refuse and say so: %+v", res)
	}
	requireNoGateWorktree(t, root)
}
