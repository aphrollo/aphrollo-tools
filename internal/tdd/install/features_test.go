package install

import (
	"path/filepath"
	"strings"
	"testing"
)

// featureLine is the rendered line that opens key's row, "" when the
// rendering has no row for it.
func featureLine(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == key {
			return line
		}
	}
	return ""
}

// A first install is the only moment a new user reads install's output
// closely, and it named nothing that was off (issue #877). The table is
// printed there once per repo; a second install that has nothing new to say
// says nothing, rather than repeating the table on every run.
func TestFeaturesNotYetShown_PrintsEveryRowOnceThenNothing(t *testing.T) {
	root := makeGoRepo(t)

	first, err := FeaturesNotYetShown(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range features {
		if featureLine(first, f.Key) == "" {
			t.Errorf("the first install does not show %s:\n%s", f.Key, first)
		}
	}
	second, err := FeaturesNotYetShown(root)
	if err != nil {
		t.Fatal(err)
	}
	if second != "" {
		t.Errorf("a second install repeats the table:\n%s", second)
	}
}

// A key this build added after the repo's first install is news, and only
// it is: the rows already shown stay quiet.
func TestFeaturesNotYetShown_ShowsOnlyAKeyItHasNotShownBefore(t *testing.T) {
	root := makeGoRepo(t)
	if _, err := featuresNotYetShown(root, features[:2]); err != nil {
		t.Fatal(err)
	}

	later, err := featuresNotYetShown(root, features)
	if err != nil {
		t.Fatal(err)
	}
	if featureLine(later, features[2].Key) == "" {
		t.Errorf("the new key %s is not shown:\n%s", features[2].Key, later)
	}
	for _, f := range features[:2] {
		if featureLine(later, f.Key) != "" {
			t.Errorf("%s was shown on the first install and is shown again:\n%s", f.Key, later)
		}
	}
}

// Once per REPO, not per checkout: a lane is the same repo as its primary,
// and an install run from each must not show the table twice.
func TestFeaturesNotYetShown_ALaneOfAnAlreadyShownRepoShowsNothing(t *testing.T) {
	root := makeGoRepo(t)
	lane := addWorktree(t, root, "lane-a")
	if _, err := FeaturesNotYetShown(root); err != nil {
		t.Fatal(err)
	}

	got, err := FeaturesNotYetShown(lane)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("a lane of a repo that was already shown the table shows it again:\n%s", got)
	}
}

// `aphrollo config` answers "what is on HERE", so each row carries the value
// this repo declares, and the default where it declares nothing.
func TestRenderFeatures_StatesThisReposValues(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "aphrollo.toml"),
		"[aphrollo]\nmutants-at-merge = true\nmutants-shards = 3\n")

	text := RenderFeatures(root)

	for key, want := range map[string]string{
		"mutants-at-merge":  "on",
		"mutants-before-pr": "off",
		"mutants-shards":    "3",
		"mutants-slots":     "1",
		"undercover":        "off",
	} {
		f := strings.Fields(featureLine(text, key))
		if len(f) < 2 || f[1] != want {
			t.Errorf("%s: row %q, want the value %q", key, featureLine(text, key), want)
		}
	}
	for _, f := range features {
		for _, want := range []string{"cost: " + f.Cost, "enable: " + f.Enable} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the table does not carry %q", f.Key, want)
			}
		}
	}
}

// Anything with a real resource cost stays off until a repo asks for it
// (issues #875, #877), and a repo that declares nothing is shown the defaults.
func TestRenderFeatures_ARepoThatDeclaresNothingShowsTheDefaults(t *testing.T) {
	t.Parallel()
	text := RenderFeatures(t.TempDir())
	for key, want := range map[string]string{
		"mutants-at-merge":  "off",
		"mutants-before-pr": "off",
		"mutants-shards":    "derived",
		"mutants-slots":     "1",
		"undercover":        "off",
	} {
		if f := strings.Fields(featureLine(text, key)); len(f) < 2 || f[1] != want {
			t.Errorf("%s in a repo that declares nothing: %q, want %s", key, featureLine(text, key), want)
		}
	}
}

// A config the mutation reader refuses measures nothing, and the table must
// not dress that up as a default the repo chose.
func TestRenderFeatures_ARefusedMutationConfigReadsAsUnreadable(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "aphrollo.toml"), "[aphrollo]\nmutation-receipt = true\n")

	text := RenderFeatures(root)

	for _, key := range []string{"mutants-at-merge", "mutants-before-pr", "mutants-shards"} {
		if f := strings.Fields(featureLine(text, key)); len(f) < 2 || f[1] != "unreadable" {
			t.Errorf("%s under a refused config: %q, want unreadable", key, featureLine(text, key))
		}
	}
}

// With no git dir there is nowhere to remember what was shown; saying so
// beats showing the table on every run. The ceiling keeps git from finding
// a repository the temp dir happens to sit inside.
func TestFeaturesNotYetShown_OutsideAGitRepoIsAnError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if _, err := FeaturesNotYetShown(dir); err == nil {
		t.Error("a directory with no git dir returned no error")
	}
}
