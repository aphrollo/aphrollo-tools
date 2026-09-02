package tdd

import "testing"

// A rename that also edits the file is what a refactor commit looks like, and
// git records it as R — which the ACM filter dropped, taking the WHOLE commit
// with it (33 files, a modified lib.rs with a test, zero gate activity).
func TestStagedFiles_IncludesRenamedPath(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "alpha.go", "package m\n\nfunc Alpha() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "alpha")

	gitDo(t, root, "mv", "alpha.go", "beta.go")
	write(t, root, "beta.go", "package m\n\nfunc Alpha() int { return 2 }\n")
	gitDo(t, root, "add", "beta.go")

	got := stagedFiles(root)
	if !contains(got, "beta.go") {
		t.Fatalf("a renamed+edited file must reach the gate under its NEW path; staged = %v", got)
	}
	if contains(got, "alpha.go") {
		t.Fatalf("the pre-rename path no longer exists and must not be judged; staged = %v", got)
	}
}

// A pure rename (no content edit) still moves the code into a different
// package/root, so the new path is the one the gate tests.
func TestStagedFiles_IncludesPureRename(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "gamma.go", "package m\n\nfunc Gamma() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "gamma")

	gitDo(t, root, "mv", "gamma.go", "delta.go")

	got := stagedFiles(root)
	if !contains(got, "delta.go") {
		t.Fatalf("a pure rename must reach the gate under its new path; staged = %v", got)
	}
}

// stagedFiles is the ONE reader of the index, so a rename cannot be visible to
// the suite stages and invisible to the law/fixture stages.
func TestStagedTouchesLaws_SeesRenamedLawFile(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, ".ratchet/laws/old_name.toml", "name = \"x\"\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "law")

	gitDo(t, root, "mv", ".ratchet/laws/old_name.toml", ".ratchet/laws/new_name.toml")

	if !stagedTouchesLaws(root) {
		t.Fatalf("renaming a law file changes the laws; staged = %v", stagedFiles(root))
	}
}
