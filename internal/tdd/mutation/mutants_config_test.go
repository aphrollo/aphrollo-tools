package mutation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A repo that still declares `mutation-receipt = true` believes it is gated
// and is not: the receipt it names no longer exists. So a retired key is a
// loud refusal that names its replacement, never a key quietly ignored
// (criterion 2).
func TestMutantsConfig_RefusesRetiredKeyNamingReplacement(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"mutation-receipt", "mutants-local", "mutants-judge-local", "mutation-runner"} {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, "aphrollo.toml"), "[aphrollo]\n"+key+" = true\n")

		_, err := ReadMutantsConfig(root)

		want := key + " is retired: declare mutants-at-merge = true instead"
		if err == nil {
			t.Fatalf("%s: ReadMutantsConfig returned no error, want %q", key, want)
		}
		if err.Error() != want {
			t.Errorf("%s: error = %q, want %q", key, err.Error(), want)
		}
	}
}

// The stage is opt-in: a repo that declares nothing gets the zero value and
// no error, so reading the config costs a repo that never asked for it
// nothing at all (criterion 1).
func TestMutantsConfig_NotDeclaredMeansStageOff(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "aphrollo.toml"), "[aphrollo]\nundercover = true\n")

	cfg, err := ReadMutantsConfig(root)

	if err != nil {
		t.Fatalf("ReadMutantsConfig: %v", err)
	}
	if cfg.AtMerge {
		t.Errorf("AtMerge = true for a repo that declared nothing")
	}
}

// Mutation measurement costs a consuming repo CPU and wall-clock it never
// agreed to pay, so it stays off until the repo declares it (issue #875).
// Both switches are pinned, for the repo shapes that never mention aphrollo
// at all: no manifest, and a Cargo workspace with no aphrollo metadata. The
// pre-merge gate reads AtMerge, `workspace pr`/`ship`/`submit` read BeforePR.
func TestMutantsConfig_ARepoThatNeverOptedInMeasuresNothingBeforeAPROrAMerge(t *testing.T) {
	t.Parallel()
	bare := t.TempDir()
	cargo := t.TempDir()
	mustWrite(t, filepath.Join(cargo, "Cargo.toml"), "[workspace]\nmembers = [\"a\"]\n")

	for name, root := range map[string]string{"no manifest": bare, "cargo workspace": cargo} {
		cfg, err := ReadMutantsConfig(root)

		if err != nil {
			t.Fatalf("%s: ReadMutantsConfig: %v", name, err)
		}
		if cfg.AtMerge {
			t.Errorf("%s: AtMerge = true, want the merge unmeasured", name)
		}
		if cfg.BeforePR {
			t.Errorf("%s: BeforePR = true, want the PR opened unmeasured", name)
		}
	}
}

// One configuration surface, two spellings of it: a Cargo workspace declares
// the keys under [workspace.metadata.aphrollo], everything else under
// [aphrollo] in aphrollo.toml, and both must produce the same struct
// (criterion 3).
func TestMutantsConfig_ReadsCargoMetadataAndAphrolloTomlAlike(t *testing.T) {
	t.Parallel()
	keys := `mutants-at-merge = true
mutants-env = ["BORLD_GPU=1"]
mutation-baseline-exclude = ["test(conditioner_burst) # wall-clock under load"]
mutation-accept = ["crates/a/src/lib.rs:3:5 replace + with - # kind=equivalent: same value"]
mutants-after = "tools/after.sh"
`
	want := MutantsConfig{
		AtMerge:         true,
		Env:             []string{"BORLD_GPU=1"},
		BaselineExclude: []string{"test(conditioner_burst) # wall-clock under load"},
		Accept:          []string{"crates/a/src/lib.rs:3:5 replace + with - # kind=equivalent: same value"},
		After:           "tools/after.sh",
	}

	for _, tc := range []struct{ name, file, table string }{
		{"cargo", "Cargo.toml", "[workspace.metadata.aphrollo]"},
		{"aphrollo.toml", "aphrollo.toml", "[aphrollo]"},
	} {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, tc.file), tc.table+"\n"+keys)
		mustWrite(t, filepath.Join(root, "tools", "after.sh"), "#!/bin/sh\n")

		cfg, err := ReadMutantsConfig(root)

		if err != nil {
			t.Fatalf("%s: ReadMutantsConfig: %v", tc.name, err)
		}
		if !sameMutantsConfig(cfg, want) {
			t.Errorf("%s: config = %+v, want %+v", tc.name, cfg, want)
		}
	}
}

// mutants-after names a path relative to the repo root, and a missing file is
// a refusal rather than a silent skip: the hook exists so a repo can reclaim
// what a timeout-killed test binary left behind, and one that never ran is
// the case nobody notices (criterion 3).
func TestMutantsConfig_MissingAfterHookIsARefusal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "aphrollo.toml"), "[aphrollo]\nmutants-at-merge = true\nmutants-after = \"tools/nope.sh\"\n")

	_, err := ReadMutantsConfig(root)

	if err == nil {
		t.Fatal("ReadMutantsConfig accepted a mutants-after path with no file behind it")
	}
	if !strings.Contains(err.Error(), "tools/nope.sh") {
		t.Errorf("error = %q, want the missing path named", err.Error())
	}
}

// A mutation-accept array whose entries are not comma-separated is invalid
// TOML — aphrollo.toml's own shipped array had exactly this shape and no
// tool downstream noticed, because tomlStringsIn's quotedWords extraction
// does not care what sits between two quoted entries. It must never be read
// leniently: a malformed accept-list is a list nobody can trust, reported
// the same way every other unreadable accept-list is (criterion 3).
func TestMutantsConfig_MutationAcceptMissingCommaIsARefusal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "aphrollo.toml"), strings.Join([]string{
		"[aphrollo]",
		"mutation-accept = [",
		`  "calc.go:1 CONDITIONALS_BOUNDARY # first entry, no comma after it"`,
		`  "calc.go:2 ARITHMETIC_BASE # second entry"`,
		"]",
	}, "\n"))

	_, err := ReadMutantsConfig(root)

	if err == nil {
		t.Fatal("ReadMutantsConfig accepted a mutation-accept array whose entries are not comma-separated")
	}
	if !strings.Contains(err.Error(), "the accept-list could not be read") {
		t.Errorf("error = %q, want it to say the accept-list could not be read", err.Error())
	}
}

// aphrollo.toml's own mutation-accept array shipped without the commas TOML
// requires between elements, and neither this scanner nor any downstream
// reader noticed until a real TOML parser choked on it. Pinning the check
// against the repo's OWN shipped file, not a fixture, is the only thing that
// actually stands between this array and a repeat of that regression.
func TestAphrolloToml_MutationAcceptArrayStaysCommaSeparated(t *testing.T) {
	t.Parallel()
	root := tddRepoRoot(t)
	if err := tomlArrayCommaError(filepath.Join(root, "aphrollo.toml"), "[aphrollo]", mutantsAcceptKey); err != nil {
		t.Fatalf("aphrollo.toml's own mutation-accept array: %v", err)
	}
}

// tddRepoRoot walks up from the test's own working directory to the
// directory holding go.mod, so a test reading this repo's OWN aphrollo.toml
// finds it regardless of which package directory `go test` runs it from.
func tddRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from the test's working directory")
		}
		dir = parent
	}
}

// sameMutantsConfig compares two configs field by field, so a failure names
// the struct rather than a reflect verdict.
func sameMutantsConfig(a, b MutantsConfig) bool {
	return a.AtMerge == b.AtMerge && a.After == b.After &&
		sameStrings(a.Env, b.Env) && sameStrings(a.BaselineExclude, b.BaselineExclude) &&
		sameStrings(a.Accept, b.Accept)
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
