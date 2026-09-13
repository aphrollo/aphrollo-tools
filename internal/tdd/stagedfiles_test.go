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

// A fixture edit can change a fixture's own verdict exactly as a law edit
// can — the trigger must fire for either.
func TestStagedTouchesLaws_FiresOnAFixtureEdit(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, ".ratchet/fixtures/x/hit/a.go", "package a\n")
	gitDo(t, root, "add", ".")

	if !stagedTouchesLaws(root) {
		t.Fatalf("staging a fixture must re-prove it; staged = %v", stagedFiles(root))
	}
}

// A generated doc under .ratchet/ — the README pinned by
// TestOwnRatchetReadme_MatchesTheGeneratedOutput — cannot change any law's or
// fixture's outcome, so staging it alone must not run the fixtures stage.
func TestStagedTouchesLaws_IgnoresAGeneratedDocOutsideLawsAndFixtures(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, ".ratchet/README.md", "# generated\n")
	gitDo(t, root, "add", ".")

	if stagedTouchesLaws(root) {
		t.Fatalf("staging only the generated README must not fire the fixtures stage; staged = %v", stagedFiles(root))
	}
}

// The staged-baseline guard (baselineStage) already judges a raised baseline
// ahead of this stage in precommitDecide — see baselineGlobs' default
// ".ratchet/baselines/*.txt", which matches every file under
// .ratchet/baselines in this repo. A baseline edit alone must not also pay
// for a fixtures re-run it cannot affect.
func TestStagedTouchesLaws_IgnoresABaselineEdit(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, ".ratchet/baselines/x.txt", "a.go | 1\n")
	gitDo(t, root, "add", ".")

	if stagedTouchesLaws(root) {
		t.Fatalf("staging only a baseline must not fire the fixtures stage; staged = %v", stagedFiles(root))
	}
}
