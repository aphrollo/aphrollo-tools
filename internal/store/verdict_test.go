package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

const keyA = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
const keyB = "1111111111111111111111111111111111111111111111111111111111111111"

func runOf(runner, unit string, result kernel.Verdict) RunVerdict {
	return RunVerdict{Runner: runner, Unit: unit, Result: result}
}

func record(t *testing.T, s *Store, key string, add Verdict) Verdict {
	t.Helper()
	got, err := s.RecordVerdict(bounded(t), key, "", add)
	if err != nil {
		t.Fatalf("RecordVerdict(%s): %v", key, err)
	}
	return got
}

func read(t *testing.T, s *Store, key string) (Verdict, bool) {
	t.Helper()
	v, ok, err := s.ReadVerdict(key)
	if err != nil {
		t.Fatalf("ReadVerdict(%s): %v", key, err)
	}
	return v, ok
}

// plant writes content as the verdict file of key, bypassing the store.
func plant(t *testing.T, s *Store, key, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(s.verdictPath(key)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.verdictPath(key), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVerdict_aKeyNeverWrittenIsAbsent(t *testing.T) {
	s := open(t, t.TempDir())
	if v, ok := read(t, s, keyA); ok {
		t.Errorf("ReadVerdict of a new key = %+v, want absent", v)
	}
}

func TestVerdict_everythingRecordedComesBackFromASecondStore(t *testing.T) {
	dir := t.TempDir()
	at := t0.Add(time.Second)
	want := Verdict{
		Runs: []RunVerdict{{Runner: "go-test", Unit: "internal/lane", Result: kernel.VerdictRed, Test: "TestOpenOnRed", MS: 840, At: at}},
		Laws: []LawVerdict{{Law: "module_size", Result: LawRefuse, Detail: "store.go: 338 > 300"}},
		CI:   []CIVerdict{{OS: "linux", By: "pipeline", Run: "1234", Head: "h1", Tree: "t1", Conclusion: "success", Mutation: "clean"}},
	}
	record(t, open(t, dir), keyA, want)
	got, ok := read(t, open(t, dir), keyA)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("ReadVerdict = %+v, %v\nwant %+v", got, ok, want)
	}
}

func TestVerdict_aSecondWriterAddsWhatItMeasuredAndKeepsTheFirstsResults(t *testing.T) {
	s := open(t, t.TempDir())
	record(t, s, keyA, Verdict{Runs: []RunVerdict{runOf("go-test", "pkg/a", kernel.VerdictGreen)}})
	got := record(t, s, keyA, Verdict{
		Runs: []RunVerdict{runOf("go-test", "pkg/b", kernel.VerdictRed)},
		Laws: []LawVerdict{{Law: "secrets", Result: LawPass}},
	})
	if len(got.Runs) != 2 || len(got.Laws) != 1 {
		t.Fatalf("after two writers: %+v, want both units' runs and the law", got)
	}
	if r := got.RunsOf("go-test", "pkg/a"); len(r) != 1 || r[0].Result != kernel.VerdictGreen {
		t.Errorf("the first writer's result is %+v, want its one green", r)
	}
}

func TestVerdict_theSameResultTwiceKeepsTheFirstRecord(t *testing.T) {
	s := open(t, t.TempDir())
	first := RunVerdict{Runner: "go-test", Unit: "pkg/a", Result: kernel.VerdictGreen, Test: "TestFirst", MS: 100}
	second := RunVerdict{Runner: "go-test", Unit: "pkg/a", Result: kernel.VerdictGreen, Test: "TestSecond", MS: 900}
	record(t, s, keyA, Verdict{Runs: []RunVerdict{first}})
	got := record(t, s, keyA, Verdict{Runs: []RunVerdict{second}})
	if !reflect.DeepEqual(got.Runs, []RunVerdict{first}) {
		t.Errorf("runs = %+v, want only the first record %+v: a recorded result is never overwritten", got.Runs, first)
	}
}

func TestVerdict_aDifferingResultOfTheSameRunnerAndUnitIsAFlakeFactKept(t *testing.T) {
	s := open(t, t.TempDir())
	record(t, s, keyA, Verdict{Runs: []RunVerdict{runOf("go-test", "pkg/a", kernel.VerdictRed)}})
	got := record(t, s, keyA, Verdict{Runs: []RunVerdict{runOf("go-test", "pkg/a", kernel.VerdictGreen)}})
	if n := len(got.RunsOf("go-test", "pkg/a")); n != 2 {
		t.Fatalf("%d results for one runner and unit, want the red and the green", n)
	}
	if !got.Flaky("go-test", "pkg/a") {
		t.Error("Flaky = false for a unit that gave red and green on one tree")
	}
	if got.Flaky("go-test", "pkg/other") {
		t.Error("Flaky = true for a unit with no run at all")
	}
}

func TestVerdict_aSteadyUnitIsNotFlaky(t *testing.T) {
	s := open(t, t.TempDir())
	record(t, s, keyA, Verdict{Runs: []RunVerdict{runOf("go-test", "pkg/a", kernel.VerdictGreen)}})
	got := record(t, s, keyA, Verdict{Runs: []RunVerdict{runOf("go-test", "pkg/a", kernel.VerdictGreen)}})
	if got.Flaky("go-test", "pkg/a") {
		t.Error("Flaky = true for two greens")
	}
}

func TestVerdict_differentRunnersOfOneUnitAreNotEachOthersFlake(t *testing.T) {
	s := open(t, t.TempDir())
	record(t, s, keyA, Verdict{Runs: []RunVerdict{runOf("go-test", "pkg/a", kernel.VerdictRed)}})
	got := record(t, s, keyA, Verdict{Runs: []RunVerdict{runOf("ci", "pkg/a", kernel.VerdictGreen)}})
	if got.Flaky("go-test", "pkg/a") || got.Flaky("ci", "pkg/a") {
		t.Errorf("a red under one runner and a green under another read as flaky: %+v", got.Runs)
	}
}

func TestVerdict_aLawAndACIRunKeepEveryDifferingResult(t *testing.T) {
	s := open(t, t.TempDir())
	ci := CIVerdict{OS: "linux", By: "pipeline", Run: "7", Head: "h", Tree: "t", Conclusion: "failure"}
	rerun := ci
	rerun.Conclusion = "success"
	record(t, s, keyA, Verdict{Laws: []LawVerdict{{Law: "secrets", Result: LawRefuse}}, CI: []CIVerdict{ci}})
	got := record(t, s, keyA, Verdict{Laws: []LawVerdict{{Law: "secrets", Result: LawPass}}, CI: []CIVerdict{rerun, ci}})
	if len(got.Laws) != 2 || len(got.CI) != 2 {
		t.Errorf("laws %+v and ci %+v, want two of each: the refuse and the pass, the failure and the success once", got.Laws, got.CI)
	}
}

func TestVerdict_aPendingResultIsNeverStored(t *testing.T) {
	for name, add := range map[string]Verdict{
		"no result":      {Runs: []RunVerdict{{Runner: "go-test", Unit: "pkg/a"}}},
		"deferred run":   {Runs: []RunVerdict{{Runner: "go-test", Unit: "pkg/a", Result: kernel.VerdictNotTested, Cause: kernel.CauseDeferred}}},
		"pending law":    {Laws: []LawVerdict{{Law: "secrets", Result: "pending"}}},
		"CI in progress": {CI: []CIVerdict{{Run: "7", Conclusion: "in_progress"}}},
		"CI no verdict":  {CI: []CIVerdict{{Run: "7"}}},
	} {
		t.Run(name, func(t *testing.T) {
			s := open(t, t.TempDir())
			if _, err := s.RecordVerdict(bounded(t), keyA, "", add); !errors.Is(err, ErrPending) {
				t.Errorf("err = %v, want ErrPending", err)
			}
			if _, err := os.Stat(s.verdictPath(keyA)); !os.IsNotExist(err) {
				t.Errorf("a verdict file exists after a refused write: %v", err)
			}
		})
	}
}

func TestVerdict_aPendingResultAmongRealOnesStoresNone(t *testing.T) {
	s := open(t, t.TempDir())
	_, err := s.RecordVerdict(bounded(t), keyA, "", Verdict{Runs: []RunVerdict{
		runOf("go-test", "pkg/a", kernel.VerdictGreen),
		{Runner: "go-test", Unit: "pkg/b"},
	}})
	if !errors.Is(err, ErrPending) {
		t.Fatalf("err = %v, want ErrPending", err)
	}
	if v, ok := read(t, s, keyA); ok {
		t.Errorf("half a write was kept: %+v", v)
	}
}

func TestVerdict_aNotTestedRunWithACauseIsAVerdict(t *testing.T) {
	s := open(t, t.TempDir())
	got := record(t, s, keyA, Verdict{Runs: []RunVerdict{{Runner: "go-test", Unit: "pkg/a", Result: kernel.VerdictNotTested, Cause: kernel.CauseTimeout}}})
	if len(got.Runs) != 1 {
		t.Errorf("runs = %+v, want the timed-out run kept: not-tested is a result, pending is not", got.Runs)
	}
}

func TestVerdict_anUnknownResultIsRefusedOnWrite(t *testing.T) {
	s := open(t, t.TempDir())
	_, err := s.RecordVerdict(bounded(t), keyA, "", Verdict{Runs: []RunVerdict{runOf("go-test", "pkg/a", "greenish")}})
	if err == nil || errors.Is(err, ErrPending) {
		t.Errorf("err = %v, want a refusal that is not ErrPending", err)
	}
}

func TestVerdict_aRunNamesItsRunnerAndUnit(t *testing.T) {
	s := open(t, t.TempDir())
	for _, r := range []RunVerdict{runOf("", "pkg/a", kernel.VerdictGreen), runOf("go-test", "", kernel.VerdictGreen)} {
		if _, err := s.RecordVerdict(bounded(t), keyA, "", Verdict{Runs: []RunVerdict{r}}); err == nil {
			t.Errorf("a run %+v with no runner or unit was stored", r)
		}
	}
}

func TestVerdict_aKeyThatIsNotAHashIsRefusedBeforeItNamesAFile(t *testing.T) {
	s := open(t, t.TempDir())
	for _, key := range []string{"", "../x", "a/b", "UPPER", strings.Repeat("a", 129), "ab cd"} {
		if _, err := s.RecordVerdict(bounded(t), key, "", Verdict{Laws: []LawVerdict{{Law: "l", Result: LawPass}}}); !errors.Is(err, ErrBadKey) {
			t.Errorf("RecordVerdict(%q) err = %v, want ErrBadKey", key, err)
		}
		if _, _, err := s.ReadVerdict(key); !errors.Is(err, ErrBadKey) {
			t.Errorf("ReadVerdict(%q) err = %v, want ErrBadKey", key, err)
		}
	}
}

func TestVerdict_anEmptyWriteCreatesNothing(t *testing.T) {
	s := open(t, t.TempDir())
	record(t, s, keyA, Verdict{})
	if _, err := os.Stat(s.verdictPath(keyA)); !os.IsNotExist(err) {
		t.Errorf("a write of nothing made a file: %v", err)
	}
}

func TestVerdict_lanesThatWroteAreKeptSortedAndOnce(t *testing.T) {
	s := open(t, t.TempDir())
	add := Verdict{Laws: []LawVerdict{{Law: "l", Result: LawPass}}}
	for _, lane := range []string{"lane/b", "lane/a", "lane/b", ""} {
		if _, err := s.RecordVerdict(bounded(t), keyA, lane, add); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := read(t, s, keyA)
	if want := []string{"lane/a", "lane/b"}; !reflect.DeepEqual(got.Lanes, want) {
		t.Errorf("Lanes = %v, want %v", got.Lanes, want)
	}
}

func TestVerdict_aTornOrUnparsableFileIsAbsentAndTheNextWriteReplacesIt(t *testing.T) {
	for name, content := range map[string]string{
		"torn":       `{"f":1,"key":"` + keyA + `","runs":[{"runner":"go-te`,
		"not json":   "\x00\x01garbage",
		"empty":      "",
		"other key":  `{"f":1,"key":"` + keyB + `","laws":[{"law":"x","result":"pass"}]}`,
		"wrong type": `{"f":1,"key":"` + keyA + `","runs":"green"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var warned []string
			s := open(t, t.TempDir(), func(o *Options) { o.Warn = func(m string) { warned = append(warned, m) } })
			plant(t, s, keyA, content)
			if v, ok := read(t, s, keyA); ok {
				t.Errorf("ReadVerdict of a damaged file = %+v, want absent", v)
			}
			read(t, s, keyA)
			if len(warned) != 1 || !strings.Contains(warned[0], keyA) {
				t.Errorf("warnings = %q, want one line naming the key", warned)
			}
			got := record(t, s, keyA, Verdict{Runs: []RunVerdict{runOf("go-test", "pkg/a", kernel.VerdictGreen)}})
			if len(got.Runs) != 1 || len(got.Laws) != 0 {
				t.Errorf("after the rewrite: %+v, want only the new run", got)
			}
			if v, ok := read(t, open(t, s.dir), keyA); !ok || len(v.Runs) != 1 {
				t.Errorf("rewritten file reads back as %+v, %v", v, ok)
			}
		})
	}
}

func TestVerdict_aFileOfANewerFormatIsRefusedAndLeftAlone(t *testing.T) {
	s := open(t, t.TempDir())
	newer := `{"f":2,"key":"` + keyA + `","future":true}`
	plant(t, s, keyA, newer)
	if _, _, err := s.ReadVerdict(keyA); err == nil || !strings.Contains(err.Error(), "newer binary") {
		t.Errorf("ReadVerdict err = %v, want the newer-binary refusal", err)
	}
	if _, err := s.RecordVerdict(bounded(t), keyA, "", Verdict{Laws: []LawVerdict{{Law: "l", Result: LawPass}}}); err == nil {
		t.Error("RecordVerdict over a newer file succeeded")
	}
	if data, _ := os.ReadFile(s.verdictPath(keyA)); string(data) != newer {
		t.Errorf("the newer file was changed to %q", data)
	}
}

func TestVerdict_aResultAnotherBinaryWroteSurvivesAMerge(t *testing.T) {
	s := open(t, t.TempDir())
	plant(t, s, keyA, `{"f":1,"key":"`+keyA+`","runs":[{"runner":"go-test","unit":"pkg/a","result":"quantum"}]}`)
	got := record(t, s, keyA, Verdict{Runs: []RunVerdict{runOf("go-test", "pkg/b", kernel.VerdictGreen)}})
	if r := got.RunsOf("go-test", "pkg/a"); len(r) != 1 || r[0].Result != "quantum" {
		t.Errorf("the value a newer binary wrote was dropped: %+v", got.Runs)
	}
}

func TestVerdict_aHeldLockIsAConflictNotAStall(t *testing.T) {
	s := open(t, t.TempDir(), func(o *Options) { o.LockWait = 20 * time.Millisecond })
	if err := os.MkdirAll(filepath.Dir(s.verdictLockPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	release, err := s.lockAt(bounded(t), s.verdictLockPath())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, err = s.RecordVerdict(bounded(t), keyA, "", Verdict{Laws: []LawVerdict{{Law: "l", Result: LawPass}}})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

func TestVerdict_aCanceledContextWritesNothing(t *testing.T) {
	s := open(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.RecordVerdict(ctx, keyA, "", Verdict{Laws: []LawVerdict{{Law: "l", Result: LawPass}}}); err == nil {
		t.Error("RecordVerdict with a canceled context succeeded")
	}
	if _, err := os.Stat(s.verdictPath(keyA)); !os.IsNotExist(err) {
		t.Errorf("file exists: %v", err)
	}
}

func TestVerdict_aRepeatedWriteCountsAsUseOfAStaleFile(t *testing.T) {
	s := open(t, t.TempDir())
	add := Verdict{Laws: []LawVerdict{{Law: "l", Result: LawPass}}}
	record(t, s, keyA, add)
	old := time.Now().Add(-5 * 24 * time.Hour)
	if err := os.Chtimes(s.verdictPath(keyA), old, old); err != nil {
		t.Fatal(err)
	}
	record(t, s, keyA, add) // nothing new to add
	info, err := os.Stat(s.verdictPath(keyA))
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Hour {
		t.Errorf("file still dated %v after a write that added nothing: the key was used and must not age out as unused", info.ModTime())
	}
}
