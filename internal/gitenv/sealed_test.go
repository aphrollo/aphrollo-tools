package gitenv

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
	if data, err := os.ReadFile(config); err != nil || len(data) != 0 {
		t.Errorf("the global config = %q, %v; want an empty file", data, err)
	}
}

// A config the area already holds is the run's own, and is left alone.
func TestSealed_KeepsAConfigTheAreaAlreadyHolds(t *testing.T) {
	area := t.TempDir()
	config := filepath.Join(area, "gitconfig")
	if err := os.WriteFile(config, []byte("[user]\n\tname = kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	Sealed(nil, area)

	if data, _ := os.ReadFile(config); string(data) != "[user]\n\tname = kept\n" {
		t.Errorf("the config was rewritten: %q", data)
	}
}
