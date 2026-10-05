package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/config"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/undercover"
)

func repoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func loadRepo(repo string) *config.Config {
	return config.Load(config.Options{Repo: repo, RepoID: "id", Root: filepath.Join(repo, "no-user-config")})
}

// oldMutation is the mutation level the old reader's answer amounts to: a
// pinned block, else any measurement that reports, else off.
func oldMutation(t *testing.T, repo string) string {
	t.Helper()
	cfg, err := tdd.ReadMutantsConfig(repo)
	if err != nil {
		t.Fatalf("old reader refused the fixture: %v", err)
	}
	switch {
	case cfg.AtCommitBlock || (cfg.AtMerge && cfg.AtMergeBlock):
		return "block"
	case cfg.AtMerge || cfg.AtCommit:
		return "guide"
	}
	return "off"
}

// realFile is a frozen copy of a real aphrollo.toml: multi-line arrays whose
// entries carry brackets, quotes and hashes, comments between keys.
func realFile(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "aphrollo.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAlias_EveryAliasReadsWhatItsOldReaderReadsOnFixtureRepos(t *testing.T) {
	cases := []struct {
		name, toml             string
		undercover             bool
		ci, mutation, requires string
		wantAlias              map[string]string
	}{
		{name: "everything declared", toml: "[aphrollo]\nundercover = true\nci = \"local\"\nrequires = \">=1.4\"\nmutants-at-merge = \"ci\"\nmutants-at-commit = true\n",
			undercover: true, ci: "local", mutation: "guide", requires: ">=1.4",
			wantAlias: map[string]string{"undercover": "aphrollo.toml:undercover", "ci": "aphrollo.toml:ci", "requires": "aphrollo.toml:requires", "mutation": "aphrollo.toml:mutants-at-merge,mutants-at-commit"}},
		{name: "commit block pins block", toml: "[aphrollo]\nmutants-at-commit = \"block\"\n", ci: "auto", mutation: "block"},
		{name: "merge level block with merge on", toml: "[aphrollo]\nmutants-at-merge = true\nmutants-at-merge-level = \"block\"\n", ci: "auto", mutation: "block"},
		{name: "merge level block with merge off", toml: "[aphrollo]\nmutants-at-merge = false\nmutants-at-merge-level = \"block\"\n", ci: "auto", mutation: "off"},
		{name: "both off", toml: "[aphrollo]\nmutants-at-merge = false\nmutants-at-commit = false\nundercover = false\n", ci: "auto", mutation: "off"},
		{name: "commit report", toml: "[aphrollo]\nmutants-at-commit = \"report\"\nci = \"github\"\n", ci: "github", mutation: "guide"},
		{name: "keys in another table are not read", toml: "[other]\nundercover = true\nci = \"local\"\n", ci: "auto", mutation: "off"},
		{name: "no aphrollo.toml keys", toml: "", ci: "auto", mutation: "off"},
		{name: "a real file with multi-line arrays", toml: realFile(t), undercover: true, ci: "auto", mutation: "guide"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			if tc.toml != "" {
				files["aphrollo.toml"] = tc.toml
			}
			repo := repoWith(t, files)
			cfg := loadRepo(repo)

			_, oldUndercover := undercover.Load(repo)
			if got := cfg.Get("undercover").Value.B; got != oldUndercover || got != tc.undercover {
				t.Errorf("undercover = %v, old reader %v, want %v", got, oldUndercover, tc.undercover)
			}
			oldCI, err := tdd.ReadCIMode(repo)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Get("ci").Value.S; got != oldCI || got != tc.ci {
				t.Errorf("ci = %q, old reader %q, want %q", got, oldCI, tc.ci)
			}
			if got, old := cfg.Get("mutation").Value.S, oldMutation(t, repo); got != old || got != tc.mutation {
				t.Errorf("mutation = %q, old reader %q, want %q", got, old, tc.mutation)
			}
			if got := cfg.Get("requires").Value.S; got != tc.requires {
				t.Errorf("requires = %q, want %q", got, tc.requires)
			}
			for key, want := range tc.wantAlias {
				if got := cfg.Get(key); got.Alias != want || got.Layer != config.Repo {
					t.Errorf("%s: alias %q at %v, want %q at repo", key, got.Alias, got.Layer, want)
				}
			}
			if d := cfg.Diagnostics(); len(d) != 0 {
				t.Errorf("a file the old readers accept raised diagnostics: %v", d)
			}
		})
	}
}

func TestAlias_KeysWithoutAnEquivalentStayReadableUnderTheirOwnNames(t *testing.T) {
	repo := repoWith(t, map[string]string{"aphrollo.toml": realFile(t)})
	legacy := map[string]config.Legacy{}
	for _, l := range loadRepo(repo).Legacy() {
		legacy[l.Name] = l
	}
	if got := legacy["retro-slow-merge-minutes"].Value.N; got != 120 {
		t.Errorf("retro-slow-merge-minutes = %d, want 120", got)
	}
	reads := legacy["go-test-reads"].Value.List
	if len(reads) != 5 || reads[0] != "internal/tdd/ -> tools/tddsplit" {
		t.Errorf("go-test-reads = %v, want its 5 entries read across lines", reads)
	}
	if got := legacy["mutants-before-pr"].Value.S; got != "ci" {
		t.Errorf("mutants-before-pr = %q, want ci", got)
	}
	if n := len(legacy["mutation-accept"].Value.List); n < 20 {
		t.Errorf("mutation-accept has %d entries, want the whole multi-line list", n)
	}
	for _, aliased := range []string{"undercover", "mutants-at-commit", "mutants-at-merge"} {
		if _, ok := legacy[aliased]; ok {
			t.Errorf("%s has a schema key and is read through it, not as a legacy key", aliased)
		}
	}
}

func TestAlias_TrellisTomlWinsAndTheShadowedAliasIsNamed(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"aphrollo.toml": "[aphrollo]\nundercover = true\nci = \"local\"\n",
		"trellis.toml":  "undercover = false\n",
	})
	cfg := loadRepo(repo)
	got := cfg.Get("undercover")
	if got.Value.B || got.Alias != "" || len(got.Overrides) != 1 || got.Overrides[0] != "aphrollo.toml:undercover" {
		t.Fatalf("undercover = %+v, want false from trellis.toml naming the alias it overrides", got)
	}
	if got := cfg.Get("ci"); got.Value.S != "local" || got.Alias != "aphrollo.toml:ci" {
		t.Fatalf("ci = %+v: a key trellis.toml leaves alone still reads from the alias", got)
	}
}

func TestAlias_ABadAliasedValueIsNamedWithTheOldFileAndFallsBackToBuiltIn(t *testing.T) {
	repo := repoWith(t, map[string]string{"aphrollo.toml": "[aphrollo]\nci = \"gitub\"\nundercover = yes\nunrelated = 1.5\nrequires = \"newest\"\n"})
	cfg := loadRepo(repo)
	if got := cfg.Get("ci"); got.Value.S != "auto" || got.Layer != config.BuiltIn {
		t.Errorf("ci = %+v, want built-in auto", got)
	}
	if got := cfg.Get("undercover"); got.Value.B || got.Layer != config.BuiltIn {
		t.Errorf("undercover = %+v, want built-in false for an unreadable value", got)
	}
	d := cfg.Diagnostics()
	if len(d) != 3 {
		t.Fatalf("diagnostics = %+v, want ci, undercover and requires, and nothing for the unrelated key", d)
	}
	for i, want := range []struct {
		key  string
		line int
	}{{"ci", 2}, {"undercover", 3}, {"requires", 5}} {
		if d[i].Key != want.key || d[i].Line != want.line || d[i].Layer != config.Repo || filepath.Base(d[i].File) != "aphrollo.toml" {
			t.Errorf("diagnostic %d = %+v, want %s at aphrollo.toml:%d", i, d[i], want.key, want.line)
		}
	}
}

func TestAlias_ATrailingCommentOnAnAliasedBoolStillReadsTrue(t *testing.T) {
	repo := repoWith(t, map[string]string{"aphrollo.toml": "[aphrollo]\nundercover = true # why: 104 trailers reached the remote\n"})
	cfg := loadRepo(repo)
	if got := cfg.Get("undercover"); !got.Value.B || got.Layer != config.Repo {
		t.Fatalf("undercover = %+v, want true", got)
	}
	if len(cfg.Diagnostics()) != 0 {
		t.Fatalf("diagnostics = %v", cfg.Diagnostics())
	}
	if !strings.Contains(cfg.Get("undercover").Alias, "undercover") {
		t.Fatalf("alias = %q", cfg.Get("undercover").Alias)
	}
}
