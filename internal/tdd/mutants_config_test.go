package tdd

import (
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
