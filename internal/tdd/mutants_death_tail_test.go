package tdd

import (
	"path/filepath"
	"testing"
)

// A run that dies leaves a death record, and the record is the only account
// of why. Observed on a real death:
//
//	{"tree":"b2b09500…","exit":1,"at":"…","err_log":"…\\b2b09500ba3e.err.log","tail":null}
//
// with the file err_log names zero bytes long. Nothing was lost: the job
// writes every diagnostic it has — `could not prepare <worktree>: <err>`,
// `nothing mutable in this lane's diff`, the producer's own output — to
// STDOUT, and the record names and tails only stderr. A lane cannot merge
// without a receipt, so a death here blocked the merge with an exit code and
// no way to find out what to fix.
//
// The record now carries both logs and tails whichever actually has content,
// stderr first.
func TestRecordMutantsDeath_TailsTheStdoutLogWhenStderrIsEmpty(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	j := MutantsJob{
		TipTree: "b2b09500ba3e31b8",
		Log:     filepath.Join(dir, "run.log"),
		ErrLog:  filepath.Join(dir, "run.err.log"),
	}
	mustWrite(t, j.ErrLog, "")
	mustWrite(t, j.Log, "aphrollo: could not prepare D:\\wt\\mutants: The file exists. (os error 80)\n")

	recordMutantsDeath(j, 1, mutantsDeathTail(j))

	d, ok := loadMutantsDeath(j.TipTree)
	if !ok {
		t.Fatal("no death record was written")
	}
	if len(d.Tail) == 0 {
		t.Fatal("tail is empty although the job wrote its reason to stdout — the record is an exit code and nothing else, which is what blocked the merge with no way to diagnose it")
	}
	if d.Log != j.Log {
		t.Errorf("Log = %q, want %q — the record must name the log its tail came from", d.Log, j.Log)
	}
}

// stderr still wins when it has something to say: it is where a producer's
// own crash lands, and that is closer to the cause than the wrapper's
// narration on stdout.
func TestRecordMutantsDeath_PrefersStderrWhenItHasContent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	j := MutantsJob{
		TipTree: "a1d6c360b0b5c0de",
		Log:     filepath.Join(dir, "run.log"),
		ErrLog:  filepath.Join(dir, "run.err.log"),
	}
	mustWrite(t, j.ErrLog, "ERROR Worker thread failed: The file exists. (os error 80)\n")
	mustWrite(t, j.Log, "aphrollo: 3 file(s) to measure\n")

	recordMutantsDeath(j, 1, mutantsDeathTail(j))

	d, _ := loadMutantsDeath(j.TipTree)
	if len(d.Tail) != 1 || d.Tail[0] != "ERROR Worker thread failed: The file exists. (os error 80)" {
		t.Fatalf("Tail = %q, want the stderr line", d.Tail)
	}
	if d.ErrLog != j.ErrLog {
		t.Errorf("ErrLog = %q, want %q", d.ErrLog, j.ErrLog)
	}
}
