package compat

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		writeFile(t, filepath.Join(root, name), body)
	}
	return root
}

const toml14 = "[aphrollo]\nrequires = \">=1.4\"\n"

func TestCheck_NoDeclarationIsSatisfied(t *testing.T) {
	root := repoWith(t, map[string]string{"aphrollo.toml": "[aphrollo]\nundercover = true\n"})
	if got := Check(root, Release(Version{0, 0, 1})); got != (Verdict{Status: Satisfied}) {
		t.Fatalf("Check = %+v, want Satisfied with no line", got)
	}
}

func TestCheck_NoConfigFileAtAllIsSatisfied(t *testing.T) {
	root := repoWith(t, nil)
	if got := Check(root, Release(Version{0, 0, 1})); got.Status != Satisfied {
		t.Fatalf("Check = %+v, want Satisfied", got)
	}
}

func TestCheck_AnOlderBinaryIsTooOldAndNamesTheFix(t *testing.T) {
	root := repoWith(t, map[string]string{"aphrollo.toml": toml14})
	got := Check(root, Release(Version{1, 3, 0}))
	want := Verdict{TooOld, "aphrollo too old here: this repo requires >=1.4, this is 1.3.0; run aphrollo update"}
	if got != want {
		t.Fatalf("Check = %+v, want %+v", got, want)
	}
}

func TestCheck_ABinaryAtOrAboveTheMinimumIsSatisfied(t *testing.T) {
	root := repoWith(t, map[string]string{"aphrollo.toml": toml14})
	for _, have := range []Version{{1, 4, 0}, {1, 4, 7}, {1, 5, 0}, {2, 0, 0}} {
		if got := Check(root, Release(have)); got.Status != Satisfied {
			t.Errorf("Check(%v) = %+v, want Satisfied", have, got)
		}
	}
}

func TestCheck_APatchLevelMinimumIsHonoured(t *testing.T) {
	root := repoWith(t, map[string]string{"aphrollo.toml": "[aphrollo]\nrequires = \">=1.4.2\"\n"})
	if got := Check(root, Release(Version{1, 4, 1})); got.Status != TooOld {
		t.Errorf("Check(1.4.1) = %+v, want TooOld", got)
	}
	if got := Check(root, Release(Version{1, 4, 2})); got.Status != Satisfied {
		t.Errorf("Check(1.4.2) = %+v, want Satisfied", got)
	}
}

func TestCheck_ReadsACargoWorkspacesMetadataTable(t *testing.T) {
	root := repoWith(t, map[string]string{"Cargo.toml": "[workspace]\nmembers = []\n\n[workspace.metadata.aphrollo]\nrequires = \">=2.0\"\n"})
	got := Check(root, Release(Version{1, 9, 0}))
	want := Verdict{TooOld, "aphrollo too old here: this repo requires >=2.0, this is 1.9.0; run aphrollo update"}
	if got != want {
		t.Fatalf("Check = %+v, want %+v", got, want)
	}
}

func TestCheck_TheStricterOfTwoDeclarationsWins(t *testing.T) {
	root := repoWith(t, map[string]string{
		"aphrollo.toml": toml14,
		"Cargo.toml":    "[workspace.metadata.aphrollo]\nrequires = \">=1.6\"\n",
	})
	got := Check(root, Release(Version{1, 5, 0}))
	want := Verdict{TooOld, "aphrollo too old here: this repo requires >=1.6, this is 1.5.0; run aphrollo update"}
	if got != want {
		t.Fatalf("Check = %+v, want %+v", got, want)
	}
	// The same pair, the stricter one first on disk order, must agree.
	root = repoWith(t, map[string]string{
		"aphrollo.toml": "[aphrollo]\nrequires = \">=1.8\"\n",
		"Cargo.toml":    "[workspace.metadata.aphrollo]\nrequires = \">=1.6\"\n",
	})
	want.Line = "aphrollo too old here: this repo requires >=1.8, this is 1.5.0; run aphrollo update"
	if got := Check(root, Release(Version{1, 5, 0})); got != want {
		t.Fatalf("Check = %+v, want %+v", got, want)
	}
}

func TestCheck_AKeyUnderAnotherTableIsNotADeclaration(t *testing.T) {
	root := repoWith(t, map[string]string{"aphrollo.toml": "[other]\nrequires = \">=9.0\"\n"})
	if got := Check(root, Release(Version{1, 0, 0})); got.Status != Satisfied {
		t.Fatalf("Check = %+v, want Satisfied: requires belongs under [aphrollo]", got)
	}
}

func TestCheck_ATrailingCommentAfterTheValueStillParses(t *testing.T) {
	root := repoWith(t, map[string]string{"aphrollo.toml": "[aphrollo]\nrequires = \">=1.4\" # the scan view\n"})
	got := Check(root, Release(Version{1, 3, 0}))
	if got.Status != TooOld {
		t.Fatalf("Check = %+v, want TooOld, not Malformed, for a value with a comment after it", got)
	}
}

func TestCheck_AMalformedRequiresIsRefusedWithAFix(t *testing.T) {
	root := repoWith(t, map[string]string{"aphrollo.toml": "[aphrollo]\nrequires = \"newest\"\n"})
	got := Check(root, Release(Version{9, 9, 9}))
	want := Verdict{Malformed, "aphrollo: aphrollo.toml: requires = \"newest\" is not a version this binary can compare; write the oldest version the repo accepts, like requires = \">=1.4\""}
	if got != want {
		t.Fatalf("Check = %+v, want %+v", got, want)
	}
}

func TestCheck_AMalformedCargoDeclarationNamesCargoToml(t *testing.T) {
	root := repoWith(t, map[string]string{"Cargo.toml": "[workspace.metadata.aphrollo]\nrequires = \"~1\"\n"})
	got := Check(root, Release(Version{9, 9, 9}))
	want := Verdict{Malformed, "aphrollo: Cargo.toml: requires = \"~1\" is not a version this binary can compare; write the oldest version the repo accepts, like requires = \">=1.4\""}
	if got != want {
		t.Fatalf("Check = %+v, want %+v", got, want)
	}
}

func TestCheck_AMalformedDeclarationBeatsATooOldOne(t *testing.T) {
	root := repoWith(t, map[string]string{
		"aphrollo.toml": toml14,
		"Cargo.toml":    "[workspace.metadata.aphrollo]\nrequires = \"~1\"\n",
	})
	if got := Check(root, Release(Version{1, 0, 0})); got.Status != Malformed {
		t.Fatalf("Check = %+v, want Malformed: a constraint that cannot be read decides nothing", got)
	}
}

func TestRepoRoot_FindsTheCheckoutAboveASubdirectory(t *testing.T) {
	root := repoWith(t, map[string]string{"a/b/c.txt": "x"})
	if got := RepoRoot(filepath.Join(root, "a", "b")); got != root {
		t.Fatalf("RepoRoot = %q, want %q", got, root)
	}
}

func TestRepoRoot_AcceptsAWorktreesGitFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git"), "gitdir: /elsewhere/.git/worktrees/lane\n")
	if got := RepoRoot(filepath.Join(root, "x")); got != root {
		t.Fatalf("RepoRoot = %q, want %q", got, root)
	}
}

func TestRepoRoot_AdirectoryThatDoesNotExistYetStillResolvesItsRepo(t *testing.T) {
	root := repoWith(t, nil)
	if got := RepoRoot(filepath.Join(root, "not", "created", "yet")); got != root {
		t.Fatalf("RepoRoot = %q, want %q", got, root)
	}
}

func TestRepoRoot_OutsideAnyRepoIsEmpty(t *testing.T) {
	if got := RepoRoot(t.TempDir()); got != "" {
		t.Fatalf("RepoRoot = %q, want empty", got)
	}
}

func TestCheckAt_JudgesTheRepoTheDirectorySitsIn(t *testing.T) {
	root := repoWith(t, map[string]string{"aphrollo.toml": toml14, "sub/x.txt": "x"})
	got := CheckAt(filepath.Join(root, "sub"), Release(Version{1, 3, 0}))
	if got.Status != TooOld {
		t.Fatalf("CheckAt = %+v, want TooOld", got)
	}
}

func TestCheckAt_OutsideAnyRepoIsSatisfied(t *testing.T) {
	if got := CheckAt(t.TempDir(), Release(Version{0, 0, 1})); got.Status != Satisfied {
		t.Fatalf("CheckAt = %+v, want Satisfied", got)
	}
}

// A binary built at no release tag has no version to compare, so a repo's
// minimum is neither met nor missed: the dev build runs, and says in one line
// that it did not check.
func TestCheck_ADevBuildSatisfiesADeclaredMinimumWithANotice(t *testing.T) {
	root := repoWith(t, map[string]string{"aphrollo.toml": toml14})
	got := Check(root, Build{Dev: true, Label: "0.0.0-dev+ca47dba"})
	want := Verdict{DevBuild, "aphrollo: dev build 0.0.0-dev+ca47dba is not at a release tag, so this repo's requires >=1.4 is not checked"}
	if got != want {
		t.Fatalf("Check = %+v, want %+v", got, want)
	}
}

func TestCheck_ADevBuildInARepoThatDeclaresNothingSaysNothing(t *testing.T) {
	root := repoWith(t, map[string]string{"aphrollo.toml": "[aphrollo]\nundercover = true\n"})
	if got := Check(root, Build{Dev: true, Label: "0.0.0-dev"}); got != (Verdict{Status: Satisfied}) {
		t.Fatalf("Check = %+v, want Satisfied with no line", got)
	}
}

func TestCheck_ADevBuildStillRefusesADeclarationItCannotRead(t *testing.T) {
	root := repoWith(t, map[string]string{"aphrollo.toml": "[aphrollo]\nrequires = \"newest\"\n"})
	if got := Check(root, Build{Dev: true, Label: "0.0.0-dev"}); got.Status != Malformed {
		t.Fatalf("Check = %+v, want Malformed: a dev build reads the declaration like any other", got)
	}
}
