package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/run"
)

const (
	verdictDirEnv = "STORE_TEST_VERDICT_DIR"
	verdictIDEnv  = "STORE_TEST_VERDICT_ID"
	verdictUnits  = 25
)

// verdictWriterMain is one of two processes recording to the same key. Both
// run the same units: process 1 sees every unit green, process 2 sees the odd
// ones red. Each also records a law of its own per unit. It returns the exit code.
func verdictWriterMain() int {
	id := os.Getenv(verdictIDEnv)
	s, err := Open(os.Getenv(verdictDirEnv), Options{LockWait: 2 * time.Second})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for i := range verdictUnits {
		result := kernel.VerdictGreen
		if id == "2" && i%2 == 1 {
			result = kernel.VerdictRed
		}
		add := Verdict{
			Runs: []RunVerdict{{Runner: "go-test", Unit: fmt.Sprintf("u%02d", i), Result: result, Test: "T" + id}},
			Laws: []LawVerdict{{Law: fmt.Sprintf("law-p%s-%02d", id, i), Result: LawPass}},
		}
		for {
			_, err := s.RecordVerdict(ctx, keyA, "lane/p"+id, add)
			if err == nil {
				break
			}
			if !errors.Is(err, ErrConflict) {
				fmt.Fprintf(os.Stderr, "unit %d of writer %s: %v\n", i, id, err)
				return 1
			}
		}
	}
	return 0
}

func TestRecordVerdict_twoProcessesLoseNoResultAndKeepTheFlake(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type child struct {
		c   *run.Child
		out *bytes.Buffer
	}
	children := map[string]child{}
	for _, id := range []string{"1", "2"} {
		out := &bytes.Buffer{}
		env := append(os.Environ(), verdictDirEnv+"="+dir, verdictIDEnv+"="+id)
		c, err := run.StartLight(run.Spec{Name: exe, Env: env, Stdout: out, Stderr: out, Timeout: 2 * time.Minute})
		if err != nil {
			t.Fatalf("start writer %s: %v", id, err)
		}
		children[id] = child{c, out}
		t.Cleanup(c.Close)
	}
	for id, ch := range children {
		if err := ch.c.Wait(); err != nil {
			t.Errorf("writer %s failed: %v\n%s", id, err, ch.out)
		}
	}

	got, ok := read(t, open(t, dir), keyA)
	if !ok {
		t.Fatal("no verdict file after both writers finished")
	}
	flaky := 0
	for i := range verdictUnits {
		unit := fmt.Sprintf("u%02d", i)
		runs := got.RunsOf("go-test", unit)
		wantRuns := 1
		if i%2 == 1 {
			wantRuns = 2
		}
		if len(runs) != wantRuns {
			t.Errorf("%s has %d results %+v, want %d", unit, len(runs), runs, wantRuns)
		}
		if got.Flaky("go-test", unit) {
			flaky++
		}
	}
	if want := verdictUnits / 2; flaky != want {
		t.Errorf("%d flaky units, want the %d odd ones both processes saw differently", flaky, want)
	}
	if want := 2 * verdictUnits; len(got.Laws) != want {
		t.Errorf("%d laws, want %d: a write of one process replaced the other's", len(got.Laws), want)
	}
	if want := []string{"lane/p1", "lane/p2"}; len(got.Lanes) != 2 || got.Lanes[0] != want[0] || got.Lanes[1] != want[1] {
		t.Errorf("lanes = %v, want %v", got.Lanes, want)
	}
}
