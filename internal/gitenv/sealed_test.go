package gitenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCeilingList_NamesEachDirOnceAndSkipsEmpty(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	sep := string(os.PathListSeparator)

	if got, want := CeilingList(a, "", b), a+sep+b; got != want {
		t.Errorf("ceilingList = %q, want %q", got, want)
	}
	if got := CeilingList(""); got != "" {
		t.Errorf("ceilingList of nothing = %q, want empty", got)
	}
}

func TestCeilingList_AddsTheResolvedSpellingOfASymlinkedDir(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symlinks here: %v", err) // skip-ok: an environment probe, symlinks need privilege on some platforms.
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}

	want := link + string(os.PathListSeparator) + resolved
	if got := CeilingList(link); got != want {
		t.Errorf("ceilingList = %q, want %q", got, want)
	}
}

func TestSealed_DropsEveryGitVariableAndKeepsTheOthers(t *testing.T) {
	area := filepath.Join(t.TempDir(), "area")
	env := []string{"PATH=/bin", "GIT_DIR=/outer/.git", "GIT_INDEX_FILE=/outer/.git/index", "GITX=keep", "GIT_WORK_TREE=/outer", "HOME=/h"}

	got := Sealed(env, area)

	for _, kv := range got {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE":
			t.Errorf("Sealed kept %q", kv)
		}
	}
	for _, want := range []string{"PATH=/bin", "GITX=keep", "HOME=/h"} {
		if !slices.Contains(got, want) {
			t.Errorf("Sealed dropped %q", want)
		}
	}
}

func TestSealed_SealsGitToTheAreaAndLeavesNoConfigOfTheOperators(t *testing.T) {
	area := filepath.Join(t.TempDir(), "area")

	got := Sealed([]string{"GIT_CONFIG_GLOBAL=/home/op/.gitconfig", "GIT_CEILING_DIRECTORIES=/elsewhere"}, area)

	config := filepath.Join(area, "gitconfig")
	for _, want := range []string{
		"GIT_CEILING_DIRECTORIES=" + CeilingList(area),
		"GIT_CONFIG_GLOBAL=" + config,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=3",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("Sealed lacks %q in %v", want, got)
		}
	}
	for _, kv := range got {
		if kv == "GIT_CONFIG_GLOBAL=/home/op/.gitconfig" || kv == "GIT_CEILING_DIRECTORIES=/elsewhere" {
			t.Errorf("Sealed kept the inherited %q", kv)
		}
	}
	if data, err := os.ReadFile(config); err != nil || string(data) != sealedConfigText {
		t.Errorf("the global config = %q, %v; want the neutral identity", data, err)
	}
}

// A config in the area that is not the sealed one, an empty file left by an
// earlier run, is replaced; one that is already right is not rewritten.
func TestSealed_ReplacesAStaleConfigAndLeavesACurrentOneAlone(t *testing.T) {
	area := t.TempDir()
	config := filepath.Join(area, "gitconfig")
	if err := os.WriteFile(config, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	Sealed(nil, area)

	if data, _ := os.ReadFile(config); string(data) != sealedConfigText {
		t.Fatalf("the stale config was kept: %q", data)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(config, past, past); err != nil {
		t.Fatal(err)
	}

	Sealed(nil, area)

	if info, _ := os.Stat(config); info.ModTime().After(past.Add(time.Minute)) {
		t.Errorf("a current config was rewritten: modified %s", info.ModTime())
	}
}

// A sealed child with no identity of its own and no operator config can commit:
// the fixtures of a consuming repo run `git commit` without setting one.
func TestSealed_AChildCanInitAndCommitWithoutAnyIdentityOfItsOwn(t *testing.T) {
	area, repo, home := t.TempDir(), t.TempDir(), t.TempDir()
	var base []string
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); name != "HOME" && name != "USERPROFILE" && name != "XDG_CONFIG_HOME" {
			base = append(base, kv)
		}
	}
	env := Sealed(append(base, "HOME="+home, "USERPROFILE="+home, "GIT_AUTHOR_NAME=", "EMAIL="), area)

	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "x"}, {"symbolic-ref", "HEAD"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		if args[0] == "symbolic-ref" && strings.TrimSpace(string(out)) != "refs/heads/main" {
			t.Errorf("the default branch = %q, want refs/heads/main", out)
		}
	}
}

// ratchet: test_removed TestSealed_KeepsAConfigTheAreaAlreadyHolds: replaced by TestSealed_ReplacesAStaleConfigAndLeavesACurrentOneAlone, since the area's config is now always the sealed one
