package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

// failingCommit is a store whose Commit fails, so a fold that did not finish
// can be shown to have saved nothing.
type failingCommit struct{ *MemStore }

func (failingCommit) Commit(context.Context, string, uint64, Record, []kernel.Event) (uint64, error) {
	return 0, errors.New("disk full")
}

func handleAllFacts(lane string) []kernel.Event {
	return []kernel.Event{
		{Kind: kernel.KindEdit, Lane: lane, Unit: "u", File: kernel.ClassTest, Tree: "k1"},
		{Kind: kernel.KindRunResult, Lane: lane, Unit: "u", Tree: "k1", Job: "j1", Verdict: kernel.VerdictRed},
	}
}

func TestHandleAll_FoldsEveryFactInOneCommitSoTheSecondSeesTheFirst(t *testing.T) {
	ms := NewMemStore()
	eng := &Engine{Store: ms}
	if err := eng.HandleAll(context.Background(), "l", handleAllFacts("l")); err != nil {
		t.Fatal(err)
	}
	rec, version, _ := ms.Load(context.Background(), "l")
	if rec.Units["u"].Phase != kernel.PhaseOpen {
		t.Errorf("phase = %q, want open: the test edit made it pending and the red opened it", rec.Units["u"].Phase)
	}
	if version != 1 {
		t.Errorf("version = %d, want 1: one commit for both facts", version)
	}
	if got := len(ms.Events("l")); got != 2 {
		t.Errorf("%d events logged, want 2", got)
	}
}

func TestHandleAll_AFailedSaveLeavesTheRecordAsItWas(t *testing.T) {
	ms := NewMemStore()
	eng := &Engine{Store: failingCommit{ms}}
	if err := eng.HandleAll(context.Background(), "l", handleAllFacts("l")); err == nil {
		t.Fatal("a failed commit reported success")
	}
	rec, version, _ := ms.Load(context.Background(), "l")
	if len(rec.Units) != 0 || version != 0 || len(ms.Events("l")) != 0 {
		t.Errorf("a failed fold left units %v, version %d, %d events: it must save nothing", rec.Units, version, len(ms.Events("l")))
	}
}

func TestHandleAll_AnEndedContextSavesNothingAndAQuestionOrForeignLaneIsRefused(t *testing.T) {
	ms := NewMemStore()
	eng := &Engine{Store: ms}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := eng.HandleAll(ctx, "l", handleAllFacts("l")); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the context's", err)
	}
	if err := eng.HandleAll(context.Background(), "l", handleAllFacts("other")); err == nil {
		t.Error("facts of another lane were accepted")
	}
	if err := eng.HandleAll(context.Background(), "l", []kernel.Event{{Kind: kernel.KindStop, Lane: "l"}}); err == nil {
		t.Error("a question was accepted")
	}
	if _, version, _ := ms.Load(context.Background(), "l"); version != 0 {
		t.Errorf("version = %d, want nothing saved", version)
	}
}
