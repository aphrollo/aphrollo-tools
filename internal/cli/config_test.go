package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// rowValue is the value column of key's row in the feature table, "" when
// the output has no row for it.
func rowValue(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == key {
			return f[1]
		}
	}
	return ""
}

// `aphrollo config` prints the opt-in table on demand, with what THIS repo
// declares, not the defaults (issue #877).
func TestConfig_PrintsTheFeatureTableWithThisReposValues(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "aphrollo.toml"), []byte("[aphrollo]\nmutants-before-pr = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"config", "--repo", repo}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("config exit = %d\nstderr: %s", code, errb.String())
	}

	if got := rowValue(out.String(), "mutants-before-pr"); got != "on" {
		t.Errorf("mutants-before-pr = %q, want on:\n%s", got, out.String())
	}
	if got := rowValue(out.String(), "mutants-at-merge"); got != "off" {
		t.Errorf("mutants-at-merge = %q, want off:\n%s", got, out.String())
	}
}

// The first install in a repo shows the table; the second has nothing new to
// say and says nothing, so the table never becomes noise.
func TestInstall_ShowsTheFeatureTableOnlyOnTheFirstRun(t *testing.T) {
	isolateGit(t)
	t.Setenv(tdd.HooksDirUnsafeEnv, "1")
	repo := t.TempDir()
	gitInitRepo(t, repo)
	args := []string{"install",
		"--repo", repo,
		"--bin", fakeInstalledBin(t),
		"--config-dir", t.TempDir(),
		"--git-hooks-dir", t.TempDir(),
		"--cargo-shim-dir", filepath.Join(t.TempDir(), "cargo-queue"),
	}

	var first, second, errb bytes.Buffer
	if code := Run(args, strings.NewReader(""), &first, &errb); code != 0 {
		t.Fatalf("first install exit = %d\nstderr: %s", code, errb.String())
	}
	if code := Run(args, strings.NewReader(""), &second, &errb); code != 0 {
		t.Fatalf("second install exit = %d\nstderr: %s", code, errb.String())
	}

	if rowValue(first.String(), "mutants-at-merge") != "off" {
		t.Errorf("the first install does not show mutants-at-merge off:\n%s", first.String())
	}
	if rowValue(second.String(), "mutants-at-merge") != "" {
		t.Errorf("the second install shows the table again:\n%s", second.String())
	}
}

// Each help spelling prints the usage and succeeds; a spelling that fell
// through to the flag parser would exit 2 and print nothing on stdout.
func TestConfig_EveryHelpSpellingPrintsTheUsage(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		var out, errb bytes.Buffer
		if code := Run([]string{"config", arg}, strings.NewReader(""), &out, &errb); code != 0 {
			t.Errorf("config %s exit = %d, want 0\nstderr: %s", arg, code, errb.String())
		}
		if !strings.HasPrefix(out.String(), "usage: aphrollo config") {
			t.Errorf("config %s printed %q, want the usage", arg, out.String())
		}
	}
}
