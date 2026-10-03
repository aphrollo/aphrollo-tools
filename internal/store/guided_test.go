package store

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

// flagged is a lane whose checkpoint holds a guided-once flag for a unit no
// fact has named, saved the way the engine saves a question: no event.
func flagged(t *testing.T, s *Store) {
	t.Helper()
	mustFact(t, s, entered("fix", "s1/a"))
	rec, ver, err := s.Load(bounded(t), "fix")
	if err != nil {
		t.Fatal(err)
	}
	rec.Guided = map[string]Flags{"pkg/a": {Untested: true}}
	if _, err := s.Commit(bounded(t), "fix", ver, rec, nil); err != nil {
		t.Fatal(err)
	}
}

func codeEdit(unit string) kernel.Event {
	return kernel.Event{Kind: kernel.KindEdit, Lane: "fix", Actor: "s1/a", At: t0, Unit: unit, File: kernel.ClassCode, Tree: "t1"}
}

func TestCommit_guidedFlagsComeBackFromTheCheckpoint(t *testing.T) {
	dir := t.TempDir()
	flagged(t, open(t, dir))
	got, _, err := open(t, dir).Load(bounded(t), "fix")
	if want := map[string]Flags{"pkg/a": {Untested: true}}; err != nil || !reflect.DeepEqual(got.Guided, want) {
		t.Errorf("Load from a second store = guided %v, %v; want %v", got.Guided, err, want)
	}
	if len(got.Units) != 0 {
		t.Errorf("units = %+v, want none: the flag is not a unit", got.Units)
	}
}

func TestRebuild_keepsGuidedFlagsOfAReadableCheckpointAndALostOneLosesThem(t *testing.T) {
	s := open(t, t.TempDir())
	flagged(t, s)
	ck := readCk(t, s, "fix")
	ck.Fold = FoldVersion - 1
	writeCk(t, s, "fix", ck)
	got, _, err := s.Rebuild(bounded(t), "fix")
	if want := map[string]Flags{"pkg/a": {Untested: true}}; err != nil || !reflect.DeepEqual(got.Guided, want) {
		t.Errorf("Rebuild = guided %v, %v; want %v: a question is never logged, the old checkpoint can give it back", got.Guided, err, want)
	}

	if err := os.Remove(s.checkpointPath("fix")); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.Load(bounded(t), "fix")
	if err != nil || len(got.Guided) != 0 || len(got.Lane.Actors) != 1 {
		t.Errorf("Load = guided %v, actors %v, %v; want the lane from the log and no flags: a guide may show once more, nothing blocks", got.Guided, actors(got), err)
	}
}

func TestLoad_aFactNamingAFlaggedUnitSettlesItsFlagWhenTheLogIsReplayed(t *testing.T) {
	dir := t.TempDir()
	flagged(t, open(t, dir))
	crashing := open(t, dir)
	errCrash := errors.New("killed")
	crashing.afterAppend = func() error { return errCrash }
	if _, err := fact(t, crashing, codeEdit("pkg/a")); !errors.Is(err, errCrash) {
		t.Fatalf("err = %v, want the crash", err)
	}
	for name, load := range map[string]func(*Store) (Record, error){
		"Load": func(s *Store) (Record, error) { r, _, err := s.Load(bounded(t), "fix"); return r, err },
		"Rebuild": func(s *Store) (Record, error) {
			r, _, err := s.Rebuild(bounded(t), "fix")
			return r, err
		},
	} {
		got, err := load(open(t, dir))
		if err != nil || !got.Units["pkg/a"].GuidedUntested || len(got.Guided) != 0 {
			t.Errorf("%s = units %+v, guided %v, %v; want the flag on the unit the logged edit made and none left aside", name, got.Units, got.Guided, err)
		}
	}
}

func TestFold_aUnitlessGatedCommitSeedsNoUnitThatOnlyAQuestionFlagged(t *testing.T) {
	s := open(t, t.TempDir())
	flagged(t, s)
	mustFact(t, s, kernel.Event{Kind: kernel.KindCommitGated, Lane: "fix", At: t0, Worktree: "wt", Base: "main", OS: "linux"})
	got, _, _ := s.Load(bounded(t), "fix")
	if len(got.Units) != 0 || !got.Guided["pkg/a"].Untested {
		t.Errorf("units = %+v, guided = %v; want no unit and the flag kept", got.Units, got.Guided)
	}
}
