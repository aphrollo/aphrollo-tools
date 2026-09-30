package mutation

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// lastBoundValue is the last value the environment binds name to, "" when unbound.
func lastBoundValue(env []string, name string) string {
	val := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			val = v
		}
	}
	return val
}

// Every test process a measurement starts has its git sealed to the run's own
// area: what a hook exported is dropped, no temp dir under the area can find a
// repository by walking up, and the global git config is an empty file there
// (#1043).
func TestMeasureEnv_SealsGitToTheRunsArea(t *testing.T) {
	t.Setenv("GIT_DIR", "/outer/.git")
	t.Setenv("GIT_INDEX_FILE", "/outer/.git/index")
	t.Setenv("GIT_CONFIG_GLOBAL", "/home/op/.gitconfig")
	root := filepath.Join(t.TempDir(), "lane")
	area := measureTempDir(root)

	env := measureEnv(root, MutantsConfig{})

	for _, name := range []string{"GIT_DIR", "GIT_INDEX_FILE"} {
		if got := lastBoundValue(env, name); got != "" {
			t.Errorf("%s = %q, want it dropped", name, got)
		}
	}
	if got := lastBoundValue(env, "GIT_CEILING_DIRECTORIES"); !strings.Contains(got, area) {
		t.Errorf("GIT_CEILING_DIRECTORIES = %q, want it to name the run's area %q", got, area)
	}
	if got, want := lastBoundValue(env, "GIT_CONFIG_GLOBAL"), filepath.Join(area, "gitconfig"); got != want {
		t.Errorf("GIT_CONFIG_GLOBAL = %q, want %q", got, want)
	}
	if got := lastBoundValue(env, "GIT_CONFIG_NOSYSTEM"); got != "1" {
		t.Errorf("GIT_CONFIG_NOSYSTEM = %q, want 1", got)
	}
}

// The map is built from a copy of the checkout that has a git dir of its own,
// never in the checkout itself: a test binary run in the real tree finds the
// real repository from its working directory, and a fixture's bare git call
// then writes to it (#1043).
func TestBuildTestMap_NoCommandRunsInTheRealCheckout(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)

	if _, built, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard); err != nil || !built {
		t.Fatalf("buildTestMap = built %v, err %v", built, err)
	}

	realGit := filepath.Join(root, ".git")
	if len(tc.dirs) == 0 {
		t.Fatal("no command ran")
	}
	for dir := range tc.dirs {
		if rel, err := filepath.Rel(root, dir); err == nil && filepath.IsLocal(rel) {
			t.Errorf("a command ran in %s, inside the real checkout %s", dir, root)
		}
		gitDir := tc.gitDirs[dir]
		if gitDir == "" || gitDir == realGit {
			t.Errorf("in %s git found %q, want a git dir of the copy's own", dir, gitDir)
		}
		if !strings.HasPrefix(dir, filepath.Dir(gitDir)) {
			t.Errorf("in %s git found %q, which does not belong to the copy holding the dir", dir, gitDir)
		}
	}
}

// Nothing the build runs carries what a hook exported, and each command has its
// git sealed to the run's area.
func TestBuildTestMap_EveryCommandRunsWithItsGitSealed(t *testing.T) {
	t.Setenv("GIT_DIR", "/outer/.git")
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)
	area := measureTempDir(root)

	if _, _, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard); err != nil {
		t.Fatal(err)
	}

	if len(tc.envs) < 3 {
		t.Fatalf("%d commands ran, want the compile, the listing and a test", len(tc.envs))
	}
	for i, env := range tc.envs {
		if got := lastBoundValue(env, "GIT_DIR"); got != "" {
			t.Errorf("command %d carried GIT_DIR=%q", i, got)
		}
		if got := lastBoundValue(env, "GIT_CEILING_DIRECTORIES"); !strings.Contains(got, area) {
			t.Errorf("command %d: GIT_CEILING_DIRECTORIES = %q, want it to name %q", i, got, area)
		}
	}
}

// A tree that is not a repository has nothing to give the copy a git dir of its
// own from, so the build refuses rather than run in the tree itself.
func TestBuildTestMap_ATreeThatIsNoRepositoryIsRefused(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "internal", "p", "p.go"), "package p\n")
	t.Cleanup(func() { testMapExecFn = runMutantsTool })
	testMapExecFn = tc.exec
	t.Cleanup(setGoListForTest(func(context.Context, string, string) (string, error) {
		return filepath.Join(root, "internal", "p") + "|p.go|||\n", nil
	}))

	_, built, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard)

	if err == nil || built || !strings.Contains(err.Error(), "not inside a git repository") {
		t.Errorf("buildTestMap = built %v, err %v, want a refusal naming the missing repository", built, err)
	}
	if len(tc.calls) != 0 {
		t.Errorf("%d commands ran in a tree that is no repository, want none: %v", len(tc.calls), tc.calls)
	}
}

// A copy that cannot be made is a refusal too, with nothing run.
func TestBuildTestMap_ACopyThatCannotBeMadeIsRefused(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)
	blocker := proveSandboxArea(root)
	if err := os.MkdirAll(filepath.Dir(blocker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	_, built, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard)

	if err == nil || built || !strings.Contains(err.Error(), "disposable copy") {
		t.Errorf("buildTestMap = built %v, err %v, want a refusal naming the copy", built, err)
	}
	if len(tc.calls) != 0 {
		t.Errorf("%d commands ran without a copy, want none: %v", len(tc.calls), slices.Clone(tc.calls))
	}
}
