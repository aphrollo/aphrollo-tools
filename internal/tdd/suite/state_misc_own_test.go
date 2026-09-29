package suite

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// These are suite's own tests of small helpers the other packages' tests reach
// today and suite's own do not: runnerDir, applyEdit, lookNodeFn, the mechanical
// cache's schema guard and empty-key guard, the "gone" stamp in
// worktreeStateHash, and the cargo module-mount helpers.

// TestRunnerDir_IsTheRunnersOwnDirOtherwiseTheRoot pins the fallback.
func TestRunnerDir_IsTheRunnersOwnDirOtherwiseTheRoot(t *testing.T) {
	t.Parallel()
	if got := runnerDir(Runner{Dir: "/ws"}, "/root"); got != "/ws" {
		t.Errorf("runnerDir with Dir = %q, want /ws", got)
	}
	if got := runnerDir(Runner{}, "/root"); got != "/root" {
		t.Errorf("runnerDir without Dir = %q, want /root", got)
	}
}

// TestApplyEdit_IsTheEditToolsOwnSubstitution pins the three arms: an empty old
// string creates the content, replace_all replaces every match, and otherwise
// only the first.
func TestApplyEdit_IsTheEditToolsOwnSubstitution(t *testing.T) {
	t.Parallel()
	if got := applyEdit("anything", "", "fresh", false); got != "fresh" {
		t.Errorf("empty old = %q, want the replacement alone", got)
	}
	if got := applyEdit("x x x", "x", "y", true); got != "y y y" {
		t.Errorf("replace all = %q, want y y y", got)
	}
	if got := applyEdit("x x x", "x", "y", false); got != "y x x" {
		t.Errorf("first only = %q, want y x x", got)
	}
	if got := applyEdit("abc", "zzz", "y", false); got != "abc" {
		t.Errorf("no match = %q, want the content unchanged", got)
	}
}

// TestLookNodeFn_DefaultsToThePathLookup pins the default probe: the same
// answer exec.LookPath gives for node.
func TestLookNodeFn_DefaultsToThePathLookup(t *testing.T) {
	want, wantErr := exec.LookPath("node")
	got, err := lookNodeFn()
	if got != want || (err == nil) != (wantErr == nil) {
		t.Fatalf("lookNodeFn = (%q, %v), want (%q, %v)", got, err, want, wantErr)
	}
}

// Serial: swaps the package's node lookup, a process-wide override.
// TestSetLookNodeForTest_ReplacesAndRestoresTheProbe pins the setter pair.
func TestSetLookNodeForTest_ReplacesAndRestoresTheProbe(t *testing.T) {
	restore := SetLookNodeForTest(func() (string, error) { return "/opt/node", nil })
	if got, _ := lookNodeFn(); got != "/opt/node" {
		t.Fatalf("lookNodeFn = %q, want the installed probe", got)
	}
	restore()
	want, _ := exec.LookPath("node")
	if got, _ := lookNodeFn(); got != want {
		t.Fatalf("lookNodeFn after restore = %q, want %q", got, want)
	}
}

// Serial: keeps its cache under its own CLAUDE_CONFIG_DIR, a process-wide env var.
// TestLoadMechCache_ARecordAtANewerSchemaIsNeitherReadNorOverwritten pins the
// schema guard: a cache written by a newer binary answers no lookup and takes
// no write, so an older gate never clobbers what it cannot interpret.
func TestLoadMechCache_ARecordAtANewerSchemaIsNeitherReadNorOverwritten(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := mechCachePath()
	if path == "" {
		t.Fatal("no cache path under the state dir")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	newer, _ := json.Marshal(map[string]any{"schema": StateSchema + 1, "green": map[string]string{"k": "2026-01-01T00:00:00Z"}})
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}

	if mechCacheHit("k") {
		t.Fatal("an entry from a newer schema must not answer a lookup")
	}
	mechCacheAdd("other")
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(newer) {
		t.Fatalf("the newer-schema record was rewritten: %q (%v)", after, err)
	}
}

// TestMechCacheHit_AnEmptyKeyNeverHits pins the guard.
func TestMechCacheHit_AnEmptyKeyNeverHits(t *testing.T) {
	t.Parallel()
	if mechCacheHit("") {
		t.Fatal("an empty key (no state hash) must never hit")
	}
}

// Serial: makeGoRepo sets git's config through the process-wide environment.
// TestWorktreeStateHash_ADeletedTrackedFileStillShapesTheHash pins the "gone"
// stamp: removing a tracked file moves the hash, and restoring it moves it
// back, so an absence is content.
func TestWorktreeStateHash_ADeletedTrackedFileStillShapesTheHash(t *testing.T) {
	repo := makeGoRepo(t)
	clean := worktreeStateHash(repo)
	if clean == "" {
		t.Fatal("no state hash for a clean repo")
	}
	if err := os.Remove(filepath.Join(repo, "doc.go")); err != nil {
		t.Fatal(err)
	}
	gone := worktreeStateHash(repo)
	if gone == "" || gone == clean {
		t.Fatalf("hash after deleting a tracked file = %q, want a different non-empty hash from %q", gone, clean)
	}
}

// TestCargoSrcDirOf_IsEverySegmentUpToTheFirstSrc pins the split: the crate's
// source dir, and none for a path with no src segment or a trailing one.
func TestCargoSrcDirOf_IsEverySegmentUpToTheFirstSrc(t *testing.T) {
	t.Parallel()
	if dir, ok := cargoSrcDirOf("crates/a/src/x/y.rs"); !ok || dir != "crates/a/src" {
		t.Errorf("cargoSrcDirOf = (%q, %v), want (crates/a/src, true)", dir, ok)
	}
	if _, ok := cargoSrcDirOf("crates/a/tests/it.rs"); ok {
		t.Error("a path with no src segment has no source dir")
	}
	if _, ok := cargoSrcDirOf("crates/a/src"); ok {
		t.Error("a trailing src with nothing under it has no source dir")
	}
}

// TestCargoMountedModulePath_NoMountsMeansNoMountedPath pins the empty scan: a
// crate with no #[path] declarations answers "" so the stem-derived path
// stands, and so does a file outside any src dir.
func TestCargoMountedModulePath_NoMountsMeansNoMountedPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "crates/a/src/lib.rs", "pub mod plain;\n")
	write(t, root, "crates/a/src/plain.rs", "pub fn f() {}\n")
	if got := cargoMountedModulePath(root, "crates/a/src/plain.rs"); got != "" {
		t.Errorf("no mounts: %q, want none", got)
	}
	if got := cargoMountedModulePath(root, "crates/a/tests/it.rs"); got != "" {
		t.Errorf("outside src: %q, want none", got)
	}
}

// TestRustFileIsTestModule_ACfgTestPathMountMakesTheFileTestCode pins the
// mount reading: a file mounted through #[cfg(test)] #[path = ...] mod is a
// test module, one mounted without the cfg is not, and an unmounted one is not.
func TestRustFileIsTestModule_ACfgTestPathMountMakesTheFileTestCode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "crates/a/src/lib.rs",
		"#[cfg(test)]\n#[path = \"tests/wear.rs\"]\nmod wear;\n\n#[path = \"prod/grip.rs\"]\nmod grip;\n")
	write(t, root, "crates/a/src/tests/wear.rs", "fn t() {}\n")
	write(t, root, "crates/a/src/prod/grip.rs", "fn p() {}\n")
	write(t, root, "crates/a/src/loose.rs", "fn l() {}\n")
	file := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

	if !rustFileIsTestModule(root, "crates/a/src/tests/wear.rs", file("crates/a/src/tests/wear.rs")) {
		t.Error("wear.rs is mounted under #[cfg(test)]")
	}
	if rustFileIsTestModule(root, "crates/a/src/prod/grip.rs", file("crates/a/src/prod/grip.rs")) {
		t.Error("grip.rs is mounted without #[cfg(test)]")
	}
	if rustFileIsTestModule(root, "crates/a/src/loose.rs", file("crates/a/src/loose.rs")) {
		t.Error("loose.rs is not mounted at all")
	}
	if rustFileIsTestModule(root, "crates/a/tests/it.rs", file("crates/a/tests/it.rs")) {
		t.Error("a file outside src has no mount")
	}
}

// TestRustMountsAsTest_RejectsAnUnlexableDeclarationSource pins the lex-failure
// arm: a declaring file that cannot be lexed proves no test mount.
func TestRustMountsAsTest_RejectsAnUnlexableDeclarationSource(t *testing.T) {
	t.Parallel()
	src := "#[cfg(test)]\n#[path = \"t.rs\"]\nmod t;\n/* never closed"
	if rustMountsAsTest("/x/src/lib.rs", src, "/x/src/t.rs") {
		t.Fatal("an unterminated block comment must not vouch for a test mount")
	}
}
