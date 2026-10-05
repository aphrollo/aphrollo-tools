package config_test

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
	"github.com/aphrollo/aphrollo-tools/internal/undercover"
)

const cargoHead = "[workspace]\nmembers = [\n  \"a\",\n]\n\n[dependencies]\nserde = { version = \"1\" }\n\n[workspace.metadata.aphrollo]\n"

func TestAlias_CargoMetadataReadsLikeItsOldReadersOnFixtureRepos(t *testing.T) {
	cases := []struct {
		name          string
		files         map[string]string
		undercover    bool
		requires      string
		mutation      string
		wantAliasHave string
	}{
		{name: "cargo only", files: map[string]string{"Cargo.toml": cargoHead + "undercover = true\nrequires = \">=1.6\"\nmutants-at-commit = true\n"},
			undercover: true, requires: ">=1.6", mutation: "guide", wantAliasHave: "Cargo.toml:undercover"},
		{name: "either file turns undercover on", files: map[string]string{"aphrollo.toml": "[aphrollo]\nundercover = false\n", "Cargo.toml": cargoHead + "undercover = true\n"},
			undercover: true, mutation: "off", wantAliasHave: "aphrollo.toml:undercover Cargo.toml:undercover"},
		{name: "the stricter requires wins", files: map[string]string{"aphrollo.toml": "[aphrollo]\nrequires = \">=1.4\"\n", "Cargo.toml": cargoHead + "requires = \">=1.6\"\n"},
			requires: ">=1.6", mutation: "off"},
		{name: "the stricter requires wins from aphrollo.toml too", files: map[string]string{"aphrollo.toml": "[aphrollo]\nrequires = \">=1.8\"\n", "Cargo.toml": cargoHead + "requires = \">=1.6\"\n"},
			requires: ">=1.8", mutation: "off"},
		{name: "mutation is read from Cargo.toml first", files: map[string]string{"aphrollo.toml": "[aphrollo]\nmutants-at-commit = \"block\"\n", "Cargo.toml": cargoHead + "mutants-at-commit = true\n"},
			mutation: "guide"},
		{name: "mutation keys fall through to aphrollo.toml", files: map[string]string{"aphrollo.toml": "[aphrollo]\nmutants-at-commit = \"block\"\n", "Cargo.toml": cargoHead + "mutants-at-merge = \"ci\"\n"},
			mutation: "block"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := repoWith(t, tc.files)
			cfg := loadRepo(repo)
			if _, old := undercover.Load(repo); cfg.Get("undercover").Value.B != old || old != tc.undercover {
				t.Errorf("undercover = %v, old reader %v, want %v", cfg.Get("undercover").Value.B, old, tc.undercover)
			}
			if got := cfg.Get("requires").Value.S; got != tc.requires {
				t.Errorf("requires = %q, want %q", got, tc.requires)
			}
			if tc.requires != "" {
				// The version guard's own answer: a binary just under the floor is too old.
				req, err := compat.ParseRequires(tc.requires)
				if err != nil {
					t.Fatal(err)
				}
				below := compat.Release(compat.Version{Major: req.Min.Major, Minor: req.Min.Minor - 1})
				if v := compat.Check(repo, below); v.Status != compat.TooOld {
					t.Errorf("compat.Check = %+v, want TooOld below %s", v, tc.requires)
				}
			}
			if got, old := cfg.Get("mutation").Value.S, oldMutation(t, repo); got != old || got != tc.mutation {
				t.Errorf("mutation = %q, old reader %q, want %q", got, old, tc.mutation)
			}
			if tc.wantAliasHave != "" && cfg.Get("undercover").Alias != tc.wantAliasHave {
				t.Errorf("undercover alias = %q, want %q", cfg.Get("undercover").Alias, tc.wantAliasHave)
			}
			if d := cfg.Diagnostics(); len(d) != 0 {
				t.Errorf("diagnostics = %v", d)
			}
		})
	}
}

func TestAlias_ATrailingCommentInUndercoverLoadNowReadsTrue(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"aphrollo.toml": {"aphrollo.toml": "[aphrollo]\nundercover = true # why: trailers reached the remote\n"},
		"Cargo.toml":    {"Cargo.toml": cargoHead + "undercover = true # why\n"},
	} {
		t.Run(name, func(t *testing.T) {
			repo := repoWith(t, files)
			if _, on := undercover.Load(repo); !on {
				t.Fatal("undercover = true # why must read true: the comment is not part of the value")
			}
			if !loadRepo(repo).Get("undercover").Value.B {
				t.Fatal("the schema must agree with undercover.Load")
			}
		})
	}
}

func TestAlias_LegacyKeysOfCargoMetadataAreListedWithTheirSource(t *testing.T) {
	repo := repoWith(t, map[string]string{"Cargo.toml": cargoHead + "clippy-clean = [\"a\", \"b\"]\n"})
	var found bool
	for _, l := range loadRepo(repo).Legacy() {
		if l.Name == "clippy-clean" && l.Source == "Cargo.toml" && len(l.Value.List) == 2 {
			found = true
		}
	}
	if !found {
		t.Fatal("clippy-clean has no schema key and must stay readable under its own name")
	}
}
