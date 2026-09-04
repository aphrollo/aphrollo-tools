package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mutantsOut writes the verdict files cargo-mutants keeps as it goes, in the
// shape it really writes them, taken from a 27.1.0 run: one mutant per line,
// "<file>:<line>:<col>: <mutation>".
func mutantsOut(t *testing.T, worktree string, byStatus map[string][]string) {
	t.Helper()
	dir := filepath.Join(worktree, "mutants.out")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for status, lines := range byStatus {
		if err := os.WriteFile(filepath.Join(dir, status+".txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// A run killed by the shell it was started in took 100 of 131 verdicts with
// it, twice (issue #103). cargo-mutants writes each verdict to disk as it
// reaches it, so those answers were on disk the whole time — reading them is
// the difference between losing the run and losing one mutant.
func TestReadMutantsOut_KeepsEveryVerdictAnInterruptedRunReached(t *testing.T) {
	wt := t.TempDir()
	mutantsOut(t, wt, map[string][]string{
		"caught":   {"crates/a/src/lib.rs:12:9: replace + with -", "crates/a/src/lib.rs:20:14: replace * with +"},
		"missed":   {"crates/b/src/lib.rs:3:7: replace / with %"},
		"timeout":  {"crates/b/src/lib.rs:9:22: replace - with +"},
		"unviable": {"crates/c/src/lib.rs:1:5: replace body with ()"},
	})

	got := readMutantsOut(wt)
	if len(got) != 5 {
		t.Fatalf("read %d verdicts, want all 5 the run reached: %+v", len(got), got)
	}
	byStatus := map[string]int{}
	for _, m := range got {
		byStatus[m.Status]++
		if m.File == "" || m.Line == 0 || m.Mutation == "" {
			t.Fatalf("verdict %+v is missing the fields that identify the mutant", m)
		}
	}
	for _, want := range []string{"caught", "missed", "timeout", "unviable"} {
		if byStatus[want] == 0 {
			t.Fatalf("no %s verdicts read, got %v", want, byStatus)
		}
	}
}

// A restart measures what is left, not the whole diff again: 100 verdicts
// already on disk are 100 mutants the second run must not spend hours
// re-measuring.
func TestResumeMutants_MeasuresOnlyTheMutantsWithoutAVerdict(t *testing.T) {
	all := []MutantOutcome{
		{File: "a.rs", Line: 1, Mutation: "replace + with -"},
		{File: "a.rs", Line: 2, Mutation: "replace * with +"},
		{File: "b.rs", Line: 3, Mutation: "replace / with %"},
	}
	judged := []MutantOutcome{
		{File: "a.rs", Line: 1, Mutation: "replace + with -", Status: "caught"},
		{File: "b.rs", Line: 3, Mutation: "replace / with %", Status: "missed"},
	}

	run, carry := ResumeMutants(all, judged)
	if len(run) != 1 || run[0].Line != 2 {
		t.Fatalf("run = %+v, want only the mutant nothing judged", run)
	}
	if len(carry) != 2 {
		t.Fatalf("carry = %+v, want both verdicts kept", carry)
	}
}

// The exclusions the restart hands the tool are the judged mutants, quoted so
// a mutation's own punctuation cannot become a pattern.
func TestMutantsArgv_ExcludesTheMutantsAlreadyJudged(t *testing.T) {
	got := strings.Join(MutantsArgv("lane.diff", false, []string{"a.rs:1:5: replace + with -"}, nil, ""), " ")
	if !strings.Contains(got, "--exclude-re") {
		t.Fatalf("MutantsArgv = %q, want the judged mutants excluded", got)
	}
	if !strings.Contains(got, `\+`) {
		t.Fatalf("MutantsArgv = %q, want the mutation text quoted for a regex", got)
	}
}

// The job's own stdout and stderr are FILES, and they live beside the gate's
// other state — never inside the mutation worktree cargo-mutants mutates in
// place, and never in the lane's own source tree. A log created inside the
// worktree before it exists is a log the worktree's own creation can never
// reach; a stray untracked one written there during a run makes the run's own
// dirty check fail the tree it is describing.
func TestMutantsJobLogs_LiveInGateStateNeverInAWorktree(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	var started []MutantsJob
	fakeSpawn(t, &started)
	root := optedInLane(t)

	j, ok := StartMutantsJob(root)
	if !ok {
		t.Fatal("setup: the lane commit must start a job")
	}
	for _, path := range []string{j.Log, j.ErrLog} {
		if path == "" {
			t.Fatal("a job with nowhere to write its output loses every word of it")
		}
		if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(cfg)) {
			t.Fatalf("log %s does not live under the gate's own state dir %s", path, cfg)
		}
		if strings.HasPrefix(filepath.Clean(path), filepath.Clean(MutantsWorktreeDir(root))) {
			t.Fatalf("log %s sits inside the mutation worktree", path)
		}
		if strings.HasPrefix(filepath.Clean(path), filepath.Clean(root)+string(filepath.Separator)) {
			t.Fatalf("log %s sits in the source tree", path)
		}
	}
}

// A run that ends with no verdict is a fact a merge needs: the receipt is not
// coming, and the reason is in the job's stderr rather than nowhere.
func TestMutationReceipt_MissingReceiptNamesARunThatDied(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	recordMutantsDeath(MutantsJob{Repo: "borld", TipTree: laneTip, ErrLog: "D:/wt/target/aphrollo-mutants/run.err"},
		1, []string{"thread 'main' panicked", "note: run with RUST_BACKTRACE=1"})

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("a died run is not a receipt")
	}
	if !strings.Contains(got.Message, "died (exit 1)") || !strings.Contains(got.Message, "run.err") {
		t.Fatalf("message = %q, want it to name the death and where to read about it", got.Message)
	}
	requireLoggedVerdict(t, cfg, "mutants-died:"+short(laneTip)+":1")
	// The stderr tail is kept with the record, so the reason survives the
	// process that produced it.
	d, ok := loadMutantsDeath(laneTip)
	if !ok || len(d.Tail) != 2 || !strings.Contains(d.Tail[0], "panicked") {
		t.Fatalf("death record = %+v, want the last stderr lines kept", d)
	}
	if time.Since(d.At) > time.Minute {
		t.Fatalf("death recorded at %v, want now", d.At)
	}
}

// A run that DID produce a receipt has nothing to explain: an old death
// record must not keep haunting a later, finished run of the same tree.
func TestMutantsDeath_ClearedWhenTheSameTreeFinishes(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	recordMutantsDeath(MutantsJob{Repo: "borld", TipTree: laneTip}, 1, []string{"boom"})
	clearMutantsDeath(laneTip)
	if _, ok := loadMutantsDeath(laneTip); ok {
		t.Fatal("a finished run must clear the death record for its tree")
	}
}
