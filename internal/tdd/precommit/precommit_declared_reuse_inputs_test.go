package precommit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dreuseKeyRepo is a git repo holding web/x.ts and web/y.ts, for the tests of
// what a key is made of.
func dreuseKeyRepo(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	write(t, root, "web/x.ts", "export const x = 1\n")
	write(t, root, "web/y.ts", "export const y = 1\n")
	return root
}

func dreuseKey(root string, inputs ...string) (string, bool) {
	key, _, ok := declaredReuseKey(root, declaredCommand{Argv: []string{"git", "--version"}, Inputs: inputs})
	return key, ok
}

// An input set that selects nothing hashes to a constant, which would reuse
// for ever: a glob that matches no file (a typo, a moved directory) is no key.
func TestDeclaredReuse_AGlobThatMatchesNothingGivesNoKey(t *testing.T) {
	t.Parallel()
	root := dreuseKeyRepo(t)
	for _, inputs := range [][]string{{"wbe/**"}, {"web/**", "nothing/**"}, {"web"}} {
		if key, ok := dreuseKey(root, inputs...); ok {
			t.Errorf("inputs %q gave key %q, want none: a glob matched no file", inputs, key)
		}
	}
	if _, ok := dreuseKey(root, "web/**"); !ok {
		t.Error("web/** over a tree holding web/x.ts gave no key")
	}
}

// ./web/** and web\** are the glob web/** written another way.
func TestDeclaredReuse_GlobsAreReadAsSlashPathsFromTheRoot(t *testing.T) {
	t.Parallel()
	root := dreuseKeyRepo(t)
	want, ok := dreuseKey(root, "web/**")
	if !ok {
		t.Fatal("web/** gave no key")
	}
	for _, g := range []string{"./web/**", `web\**`, `.\web/**`} {
		if got, ok := dreuseKey(root, g); !ok || got != want {
			t.Errorf("%q gave key %q (ok %v), want web/**'s %q", g, got, ok, want)
		}
	}
}

// A glob that leaves the root reads files nobody hashes.
func TestDeclaredReuse_AGlobLeavingTheRootIsRefusedByName(t *testing.T) {
	t.Parallel()
	for _, g := range []string{"../x/**", "/abs/**", `..\x`, ".."} {
		if _, err := normalizeInputGlobs([]string{"web/**", g}); err == nil || !strings.Contains(err.Error(), g) {
			t.Errorf("%q: err = %v, want a refusal naming the glob", g, err)
		}
	}
}

// A dependency or tool-version move changes no listed input, so the lockfiles
// and manifests of the root are part of every key.
func TestDeclaredReuse_ALockfileOrManifestChangeChangesTheKey(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "go.sum", "Cargo.lock", "package.json", "go.mod", "Cargo.toml"} {
		root := dreuseKeyRepo(t)
		write(t, root, name, "one\n")
		before, _ := dreuseKey(root, "web/**")
		write(t, root, name, "two\n")
		after, ok := dreuseKey(root, "web/**")
		if !ok || before == after {
			t.Errorf("%s changed and the key did not (ok %v)", name, ok)
		}
	}
}

// A glob that selects a git-ignored file names a tree the merge checkout does
// not have the same way: no key.
func TestDeclaredReuse_AGlobSelectingAnIgnoredFileGivesNoKey(t *testing.T) {
	t.Parallel()
	root := dreuseKeyRepo(t)
	write(t, root, ".gitignore", "web/gen/\n")
	write(t, root, "web/gen/a.ts", "export const a = 1\n")
	if key, ok := dreuseKey(root, "web/**"); ok {
		t.Errorf("web/** selects the ignored web/gen/a.ts and gave key %q, want none", key)
	}
}

// A file the command read and the tree no longer has is a different input set.
func TestDeclaredReuse_ADeletedInputChangesTheKey(t *testing.T) {
	t.Parallel()
	root := dreuseKeyRepo(t)
	gitDo(t, root, "add", "-A")
	before, _ := dreuseKey(root, "web/**")
	if err := os.Remove(filepath.Join(root, "web", "y.ts")); err != nil {
		t.Fatal(err)
	}
	after, ok := dreuseKey(root, "web/**")
	if !ok || before == after {
		t.Errorf("web/y.ts deleted and the key did not change (ok %v)", ok)
	}
}

// A tool that was upgraded is not the command that went green.
func TestDeclaredReuse_AChangedToolChangesTheKey(t *testing.T) {
	t.Parallel()
	root := dreuseKeyRepo(t)
	tool := filepath.Join(t.TempDir(), "lint-tool.exe")
	if err := os.WriteFile(tool, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := declaredCommand{Argv: []string{tool}, Inputs: []string{"web/**"}}
	before, _, ok := declaredReuseKey(root, c)
	if !ok {
		t.Fatal("no key for a tool that exists")
	}
	if err := os.WriteFile(tool, []byte("version two"), 0o755); err != nil {
		t.Fatal(err)
	}
	after, _, ok := declaredReuseKey(root, c)
	if !ok || before == after {
		t.Errorf("the tool changed and the key did not (ok %v)", ok)
	}
}

// Only a plain command takes part: one judged against HEAD's output never
// reuses.
func TestDeclaredReuse_OnlyABaselineOfNoneOrNothingTakesPart(t *testing.T) {
	t.Parallel()
	root := dreuseKeyRepo(t)
	for baseline, want := range map[string]bool{"": true, "none": true, "lines": false} {
		c := declaredCommand{Argv: []string{"git", "--version"}, Inputs: []string{"web/**"}, Baseline: baseline}
		if _, _, ok := declaredReuseKey(root, c); ok != want {
			t.Errorf("baseline %q: takes part = %v, want %v", baseline, ok, want)
		}
	}
}

// A store that cannot be read, or that a newer binary wrote, answers nothing
// and is not written over.
func TestDeclaredReuse_AnUnreadableStoreAnswersNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found := declaredVerdictAt(corrupt, "k"); found {
		t.Error("a corrupt store answered")
	}
	newer := filepath.Join(dir, "newer.json")
	body := `{"schema": 999, "verdicts": {"k": {"green": true, "secs": 5}}}`
	if err := os.WriteFile(newer, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found := declaredVerdictAt(newer, "k"); found {
		t.Error("a store of a newer schema answered")
	}
	recordDeclaredVerdictAt(newer, "other", declaredVerdict{Green: true}, declaredVerdictsMax)
	if got, _ := os.ReadFile(newer); string(got) != body {
		t.Errorf("a store of a newer schema was written over: %s", got)
	}
}

// The latest verdict for a key wins, so a red after a green stops the reuse.
func TestDeclaredReuse_ARedEntryOverwritesAGreen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "s.json")
	recordDeclaredVerdictAt(path, "k", declaredVerdict{Green: true, Secs: 5}, declaredVerdictsMax)
	recordDeclaredVerdictAt(path, "k", declaredVerdict{Green: false, Secs: 6}, declaredVerdictsMax)
	if v, found := declaredVerdictAt(path, "k"); !found || v.Green {
		t.Errorf("verdict = %+v (found %v), want the red", v, found)
	}
}

// The store is capped; the oldest verdict goes first.
func TestDeclaredReuse_TheStoreKeepsTheNewestVerdictsPastItsCap(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "s.json")
	for i, k := range []string{"old", "mid", "new"} {
		at := time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
		recordDeclaredVerdictAt(path, k, declaredVerdict{Green: true, At: at}, 2)
	}
	for k, want := range map[string]bool{"old": false, "mid": true, "new": true} {
		if _, found := declaredVerdictAt(path, k); found != want {
			t.Errorf("%s kept = %v, want %v", k, found, want)
		}
	}
}

// What a run leaves is recorded only for the tree it ran on, and only when it
// reached a verdict.
func TestDeclaredReuse_ARunIsRecordedOnlyForTheTreeAndOutcomeItJudged(t *testing.T) {
	t.Parallel()
	green := SuiteResult{Passed: true, Duration: 7 * time.Second}
	cases := []struct {
		name   string
		move   bool
		last   SuiteResult
		ran    bool
		stored bool
	}{
		{"unmoved green run", false, green, true, true},
		{"inputs moved during the run", true, green, true, false},
		{"timed out", false, SuiteResult{Passed: true, TimedOut: true}, true, false},
		{"inconclusive", false, SuiteResult{Inconclusive: "OOM-KILLED"}, true, false},
		{"never ran", false, green, false, false},
	}
	for _, c := range cases {
		root := dreuseKeyRepo(t)
		cmd := declaredCommand{Argv: []string{"git", "--version"}, Inputs: []string{"web/**"}}
		key, _, ok := declaredReuseKey(root, cmd)
		if !ok {
			t.Fatal("no key")
		}
		if c.move {
			write(t, root, "web/x.ts", "export const x = 2\n")
		}
		recordDeclaredRun(root, cmd, key, ok, GateResult{}, c.last, c.ran)
		if _, found := declaredVerdictFor(key); found != c.stored {
			t.Errorf("%s: stored = %v, want %v", c.name, found, c.stored)
		}
	}
}
