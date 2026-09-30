package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func TestGateMutantsRun_ABadShardIsRefusedAsBadFlags(t *testing.T) {
	gateConfigDir(t)
	root, base := cargoLaneRepo(t)
	inDir(t, root)

	for _, spec := range []string{"3/3", "-1/2", "0/0", "x"} {
		var out, errb bytes.Buffer
		code := Run([]string{"gate", "mutants", "run", "--base", base, "--shard", spec, "--report", "r.json"},
			strings.NewReader(""), &out, &errb)
		if code != 2 || !strings.Contains(errb.String(), "--shard") {
			t.Errorf("--shard %q: exit = %d, stderr = %q, want 2 naming the flag", spec, code, errb.String())
		}
	}
}

// A shard divides a Go module by file and writes its outcomes to a report;
// on a Cargo repo, or with no --report, it is refused rather than measured.
func TestGateMutantsRun_AShardOfACargoRepoOrWithNoReportIsRefused(t *testing.T) {
	gateConfigDir(t)
	root, base := cargoLaneRepo(t)
	inDir(t, root)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "run", "--base", base, "--shard", "0/2", "--report", "r.json"},
		strings.NewReader(""), &out, &errb)

	if code != 1 || !strings.Contains(errb.String(), "--shard needs a Go repo and --report") {
		t.Errorf("exit = %d, stderr = %q, want 1 saying a shard needs a Go repo and a report", code, errb.String())
	}
}

// verdictRepo is a committed repo and the tree id shard reports must carry.
func verdictRepo(t *testing.T) (root, tree string) {
	t.Helper()
	root, _ = cargoLaneRepo(t)
	return root, strings.TrimSpace(gitOutIn(t, root, "write-tree"))
}

// shardJSON is a shard's report as `run --shard` writes it.
type shardJSON struct {
	Tree    string           `json:"tree"`
	Base    string           `json:"base"`
	Shard   int              `json:"shard,omitempty"`
	Shards  int              `json:"shards,omitempty"`
	Mutants []map[string]any `json:"mutants"`
}

func writeShardReportFile(t *testing.T, dir, name string, r shardJSON) string {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	writeIn(t, dir, name, string(data))
	return path
}

func TestGateMutantsVerdict_JudgesTheMergedShardsAndExitsOnTheirVerdict(t *testing.T) {
	gateConfigDir(t)
	root, tree := verdictRepo(t)
	inDir(t, root)
	dir := t.TempDir()
	caught := map[string]any{"file": "a.go", "line": 1, "col": 2, "mutation": "ARITHMETIC_BASE", "status": "caught"}
	missed := map[string]any{"file": "b.go", "line": 3, "col": 4, "mutation": "CONDITIONALS_BOUNDARY", "status": "missed"}
	first := writeShardReportFile(t, dir, "s0.json", shardJSON{Tree: tree, Base: "b", Shard: 0, Shards: 2, Mutants: []map[string]any{caught}})
	clean := writeShardReportFile(t, dir, "s1.json", shardJSON{Tree: tree, Base: "b", Shard: 1, Shards: 2})
	dirty := writeShardReportFile(t, dir, "s1-missed.json", shardJSON{Tree: tree, Base: "b", Shard: 1, Shards: 2, Mutants: []map[string]any{missed}})
	merged := filepath.Join(dir, "merged.json")

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "verdict", "--shards", "2", "--report", merged, first, clean},
		strings.NewReader(""), &out, &errb)
	if code != 0 || !strings.Contains(out.String(), "1 tested, 1 caught") {
		t.Errorf("clean shards: exit = %d, stdout = %q, stderr = %q, want 0 and one caught mutant", code, out.String(), errb.String())
	}

	out.Reset()
	errb.Reset()
	code = Run([]string{"gate", "mutants", "verdict", "--shards", "2", first, dirty}, strings.NewReader(""), &out, &errb)
	if code != 1 || !strings.HasPrefix(out.String(), "b.go:3:4: CONDITIONALS_BOUNDARY\n") {
		t.Errorf("a survivor in shard 1: exit = %d, stdout = %q, want 1 leading with the mutant", code, out.String())
	}

	out.Reset()
	errb.Reset()
	code = Run([]string{"gate", "mutants", "verdict", "--shards", "3", first, clean}, strings.NewReader(""), &out, &errb)
	if code != 1 || !strings.Contains(out.String(), "2 shard report(s) arrived and 3 were expected") {
		t.Errorf("a missing shard: exit = %d, stdout = %q, want 1 naming the missing report", code, out.String())
	}
}

func TestGateMutantsVerdict_NeedsShardsAndReportsAndARepo(t *testing.T) {
	gateConfigDir(t)
	for _, args := range [][]string{
		{"gate", "mutants", "verdict"},
		{"gate", "mutants", "verdict", "--shards", "2"},
		{"gate", "mutants", "verdict", "--shards", "0", "r.json"},
		{"gate", "mutants", "verdict", "--shards", "-1", "r.json"},
		{"gate", "mutants", "verdict", "--bogus"},
	} {
		var out, errb bytes.Buffer
		if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
			t.Errorf("%v: exit = %d, want 2 (stderr %q)", args, code, errb.String())
		}
	}

	inDir(t, t.TempDir())
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "mutants", "verdict", "--shards", "1", "r.json"}, strings.NewReader(""), &out, &errb); code != 1 ||
		!strings.Contains(errb.String(), "git repository") {
		t.Errorf("outside a repo: exit = %d, stderr = %q, want 1 saying there is no repository", code, errb.String())
	}
}

func TestNoteMeasuredInCI_SaysSoOnAHandRunOfARepoThatMeasuresInCI(t *testing.T) {
	cases := []struct {
		name string
		cfg  tdd.MutantsConfig
		env  string
		want bool
	}{
		{"neither key is ci", tdd.MutantsConfig{AtMerge: true}, "", false},
		{"at merge is ci", tdd.MutantsConfig{AtMerge: true, AtMergeCI: true}, "", true},
		{"before the PR is ci", tdd.MutantsConfig{BeforePR: true, BeforePRCI: true}, "", true},
		{"in CI itself", tdd.MutantsConfig{AtMerge: true, AtMergeCI: true}, "true", false},
		{"some other value of the variable", tdd.MutantsConfig{AtMerge: true, AtMergeCI: true}, "false", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GITHUB_ACTIONS", tc.env)
			var errb bytes.Buffer

			noteMeasuredInCI(tc.cfg, &errb)

			if got := strings.Contains(errb.String(), "this repo measures in CI (mutants-verdict)"); got != tc.want {
				t.Errorf("note printed = %v, want %v (stderr %q)", got, tc.want, errb.String())
			}
		})
	}
}

func TestGateMutantsProve_SaysTheRepoMeasuresInCI(t *testing.T) {
	gateConfigDir(t)
	t.Setenv("GITHUB_ACTIONS", "")
	root, _ := cargoLaneRepo(t)
	writeIn(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/a\"]\n\n[workspace.metadata.aphrollo]\nmutants-at-merge = \"ci\"\n")
	inDir(t, root)

	var out, errb bytes.Buffer
	Run([]string{"gate", "mutants", "prove"}, strings.NewReader(""), &out, &errb)

	if !strings.Contains(errb.String(), "this repo measures in CI (mutants-verdict)") {
		t.Errorf("stderr = %q, want the hand run told the repo measures in CI", errb.String())
	}
}
