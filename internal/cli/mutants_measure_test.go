package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// `gate mutants run` is now the whole mutation surface: it measures THIS
// checkout against its base in the foreground and prints the report. There is
// no document to sign, no detached job to address and nothing to watch, so
// what these prove is the wiring — the verb reads the repo's config, calls
// the measurement, and turns its verdict into an exit code and a report a
// session can act on.

// gitIn runs one git command in dir, failing the test if git does.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// writeIn writes one repo file, making its directory first.
func writeIn(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// cargoLaneRepo builds a one-crate cargo workspace on `main`, then a lane
// branch with one changed crate source on top of it. It answers the root and
// the trunk commit the lane branched from — which is exactly what
// merge-base(HEAD, main) resolves to, so a run given no --base must find it.
func cargoLaneRepo(t *testing.T) (root, base string) {
	t.Helper()
	root = t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	gitIn(t, root, "config", "user.email", "t@t")
	gitIn(t, root, "config", "user.name", "t")
	writeIn(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/a\"]\n")
	writeIn(t, root, "crates/a/Cargo.toml", "[package]\nname = \"a\"\nversion = \"0.1.0\"\n")
	writeIn(t, root, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a + b }\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-qm", "trunk")
	base = strings.TrimSpace(gitOutIn(t, root, "rev-parse", "HEAD"))
	gitIn(t, root, "checkout", "-q", "-b", "lane")
	writeIn(t, root, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a + b + 0 }\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-qm", "lane")
	return root, base
}

// gitOutIn reads one git command's stdout.
func gitOutIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// shardOutputDir is where the shard this call belongs to was told to write
// its mutants.out. A measurement is N processes with N output directories, so
// a stand-in writes into the one it was given rather than into the checkout.
func shardOutputDir(t *testing.T, argv []string) string {
	t.Helper()
	for i, a := range argv {
		if a == "--output" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	t.Fatalf("argv carries no --output: %v", argv)
	return ""
}

// missedOutcomes is a cargo-mutants outcomes.json naming one surviving
// mutant, written by the stubbed runner where a real one would leave it.
const missedOutcomes = `{"outcomes":[
  {"scenario":"Baseline","summary":"Success"},
  {"scenario":{"Mutant":{"name":"crates/a/src/lib.rs:1:36: replace + with -","package":"a","file":"crates/a/src/lib.rs","span":{"start":{"line":1,"column":36}}}},"summary":"MissedMutant"}
]}`

// caughtOutcomes is the same file for a run where the suite killed the mutant.
const caughtOutcomes = `{"outcomes":[
  {"scenario":"Baseline","summary":"Success"},
  {"scenario":{"Mutant":{"name":"crates/a/src/lib.rs:1:36: replace + with -","package":"a","file":"crates/a/src/lib.rs","span":{"start":{"line":1,"column":36}}}},"summary":"CaughtMutant"}
]}`

// A survivor nobody accepted is the finding the whole stage exists for, and
// the report has to lead with it: over three weeks the document this replaces
// refused 150 merges and named a surviving mutant in none of them. The verb
// exits 1 and the mutant is the FIRST line on stdout, not a detail after a
// paragraph of counts.
func TestGateMutantsRun_RefusesOnUnacceptedMissedNamingIt(t *testing.T) {
	gateConfigDir(t)
	t.Cleanup(tdd.SetFreeSpaceForTest(999, true))
	// One shard, so this test is about what the VERB prints rather than
	// about how many cores the box running it has.
	t.Cleanup(tdd.SetMutantsShardsForTest(1))
	root, base := cargoLaneRepo(t)
	t.Cleanup(tdd.SetMutantsExecForTest(func(_ context.Context, dir string, env, argv []string, log io.Writer) (int, error) {
		writeIn(t, shardOutputDir(t, argv), "mutants.out/outcomes.json", missedOutcomes)
		return 2, nil
	}))
	inDir(t, root)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "run", "--base", base}, strings.NewReader(""), &out, &errb)

	if code != 1 {
		t.Fatalf("exit = %d, want 1 for an unaccepted survivor\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	first, _, _ := strings.Cut(out.String(), "\n")
	if first != "crates/a/src/lib.rs:1:36: replace + with -" {
		t.Fatalf("first stdout line = %q, want the surviving mutant named first\nstdout: %s", first, out.String())
	}
}

// With no --base, a lane measures what the merge will: the newest trunk
// commit it already contains. A base taken from the trunk TIP instead would
// hand the runner main's own later commits as if the lane had written them —
// one real run measured 40 files and two crates the lane never opened.
func TestGateMutantsRun_BaseDefaultsToMergeBaseWithDefaultBranch(t *testing.T) {
	gateConfigDir(t)
	t.Cleanup(tdd.SetFreeSpaceForTest(999, true))
	t.Cleanup(tdd.SetMutantsShardsForTest(1))
	root, _ := cargoLaneRepo(t)
	// Trunk moves on after the lane branched. The lane never touched this
	// file, so a correct base leaves it out of the measured diff entirely.
	gitIn(t, root, "checkout", "-q", "main")
	writeIn(t, root, "crates/a/src/trunk_only.rs", "pub fn trunk_only() -> i32 { 7 }\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-qm", "trunk moves on")
	gitIn(t, root, "checkout", "-q", "lane")

	var measured []string
	t.Cleanup(tdd.SetMutantsExecForTest(func(_ context.Context, dir string, env, argv []string, log io.Writer) (int, error) {
		for i, a := range argv {
			if a == "--in-diff" && i+1 < len(argv) {
				data, err := os.ReadFile(argv[i+1])
				if err != nil {
					t.Errorf("no diff file for the runner: %v", err)
					continue
				}
				measured = append(measured, string(data))
			}
		}
		writeIn(t, shardOutputDir(t, argv), "mutants.out/outcomes.json", caughtOutcomes)
		return 0, nil
	}))
	inDir(t, root)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "run"}, strings.NewReader(""), &out, &errb)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 when every mutant was caught\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if len(measured) == 0 {
		t.Fatalf("the runner was handed no diff at all\nstderr: %s", errb.String())
	}
	// Every invocation the run makes — the mutant listing that sizes the
	// shard count, then the shards themselves — is handed the SAME diff, and
	// what this test is about is what is in it.
	for i, diff := range measured {
		if diff != measured[0] {
			t.Fatalf("invocation %d was handed a different diff:\n%s\nvs\n%s", i, diff, measured[0])
		}
		if !strings.Contains(diff, "a + b + 0") {
			t.Errorf("diff =\n%s\nwant the lane's own hunk", diff)
		}
		if strings.Contains(diff, "trunk_only") {
			t.Errorf("diff =\n%s\nwant nothing trunk wrote after the lane branched", diff)
		}
	}
}

// The verbs that existed to produce, address or watch a detached run and its
// receipt are gone, and a caller who types one has to be told so: a verb that
// quietly does nothing is how a script keeps believing it is gated.
func TestGateVerbs_ReceiptStatusWatchGoAreUnknown(t *testing.T) {
	gateConfigDir(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"gate", "receipt", "sign", "receipt.json"}, `unknown subcommand "receipt"`},
		{[]string{"gate", "mutants", "status"}, `unknown verb "status"`},
		{[]string{"gate", "mutants", "watch"}, `unknown verb "watch"`},
		{[]string{"gate", "mutants", "go"}, `unknown verb "go"`},
	} {
		var out, errb bytes.Buffer
		code := Run(tc.args, strings.NewReader(""), &out, &errb)
		if code != 2 {
			t.Errorf("`aphrollo %s` exit = %d, want 2\nstderr: %s", strings.Join(tc.args, " "), code, errb.String())
		}
		if !strings.Contains(errb.String(), tc.want) {
			t.Errorf("`aphrollo %s` stderr = %q, want it to carry %q", strings.Join(tc.args, " "), errb.String(), tc.want)
		}
	}
}
