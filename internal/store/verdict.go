package store

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// # Verdict files
//
// A verdict is a fact about a tree (architecture §3 "Tree keys", "Verdict"): one
// file, verdicts/<key>.json, holds everything measured about the tree one key
// names (a worktree key, see git.Client.WorktreeKey): the runs per (runner,
// unit), the law results, and CI's verdicts. A pending result is not a verdict
// and is never stored.
//
// # Merge
//
// A key is a tree, so a result recorded for it is a fact about that tree and is
// never overwritten or dropped. RecordVerdict reads the file under the store's
// one verdicts lock, adds what the writer measured, and replaces the file by
// rename. Observations are identified by what they say:
//
//	run  (runner, unit, result, cause)
//	law  (law, result)
//	CI   every field
//
// An observation already recorded is not added again (the first record wins: its
// test name, time and duration stay), and a different one is added beside it.
// Two runs of one (runner, unit) that disagree, a red and a green on the same
// tree, are both kept: that is a flake fact, and Verdict.Flaky reads it. Writers
// never replace each other's results, in whatever order they come, and the file
// lists its observations sorted so the same set is the same bytes.
//
// # Damage
//
// A file that cannot be read or does not parse, or that is another key's, is
// treated as absent: ReadVerdict says so (and warns once per key) and the next
// RecordVerdict replaces it with what that writer measured. A file of a newer
// format is refused by name, read or write, and left alone.
//
// # Retention
//
// SweepVerdicts removes a file unused for 14 days (VerdictMaxAge), a file all of
// whose writing lanes have closed, and past VerdictCap (2,000) files the least
// recently used. Use is the file's modification time: a write sets it, and so
// does a read of a file not touched within the hour.

const (
	// VerdictFormat is the verdict file format this binary reads and writes.
	// A field is added only with a new format: a newer file's unknown fields
	// would not survive a rewrite by this one, so it is refused instead.
	VerdictFormat = 1
	// VerdictMaxAge is how long an unused verdict file is kept.
	VerdictMaxAge = 14 * 24 * time.Hour
	// VerdictCap is the most verdict files kept.
	VerdictCap = 2000

	maxKeyLen = 128
	// touchAfter is how stale a file's time must be before a read renews it, so
	// a hot key is not rewritten on every read.
	touchAfter = time.Hour
	// tempMaxAge is when a temp file of an interrupted write is abandoned.
	tempMaxAge = time.Hour
)

// Law results: a stage's verdict (architecture §3 "Verdict").
const (
	LawPass       = "pass"
	LawRefuse     = "refuse"
	LawUnmeasured = "unmeasured"
)

var (
	// ErrPending is what a write of a pending result is refused with: a run
	// still in flight, a CI run without a conclusion. Pending is not a verdict.
	ErrPending = errors.New("store: a pending result is not a verdict")
	// ErrBadKey is a tree key that cannot name a verdict file.
	ErrBadKey = errors.New("store: not a tree key: want 1 to 128 lower-case hex digits")
)

// RunVerdict is one run's result for one (runner, unit) on the tree.
type RunVerdict struct {
	Runner string         `json:"runner"`
	Unit   string         `json:"unit"`
	Result kernel.Verdict `json:"result"`
	Cause  string         `json:"cause,omitempty"` // why a not-tested run was not (kernel.Cause*)
	Test   string         `json:"test,omitempty"`
	MS     int64          `json:"ms,omitempty"`
	At     time.Time      `json:"at,omitzero"`
}

// LawVerdict is one law's result on the tree: pass, refuse or unmeasured.
type LawVerdict struct {
	Law    string `json:"law"`
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
}

// CIVerdict is CI's verdict on the tree: the pipeline run that gave it, on
// which OS, by whom, for which head, and the conclusion and mutation outcome.
type CIVerdict struct {
	OS         string `json:"os,omitempty"`
	By         string `json:"by,omitempty"`
	Run        string `json:"run"`
	Head       string `json:"head,omitempty"`
	Tree       string `json:"tree,omitempty"`
	Conclusion string `json:"conclusion"`
	Mutation   string `json:"mutation,omitempty"`
}

// Verdict is what is known of one tree key. Lanes names the lanes that wrote
// to it, sorted, for the sweep; a write supplies its own lane, never a Lanes.
type Verdict struct {
	Runs  []RunVerdict `json:"runs,omitempty"`
	Laws  []LawVerdict `json:"laws,omitempty"`
	CI    []CIVerdict  `json:"ci,omitempty"`
	Lanes []string     `json:"lanes,omitempty"`
}

// RunsOf is every result recorded for the runner and unit, in file order.
func (v Verdict) RunsOf(runner, unit string) []RunVerdict {
	var out []RunVerdict
	for _, r := range v.Runs {
		if r.Runner == runner && r.Unit == unit {
			out = append(out, r)
		}
	}
	return out
}

// Flaky reports a (runner, unit) that gave different results on this one tree.
func (v Verdict) Flaky(runner, unit string) bool {
	runs := v.RunsOf(runner, unit)
	return len(runs) > 0 && slices.ContainsFunc(runs, func(r RunVerdict) bool {
		return r.Result != runs[0].Result || r.Cause != runs[0].Cause
	})
}

// verdictFile is the file: Verdict's fields flattened beside the format and the key.
type verdictFile struct {
	F   int    `json:"f"`
	Key string `json:"key"`
	Verdict
}

func (s *Store) verdictDir() string { return filepath.Join(s.dir, "verdicts") }

func (s *Store) verdictPath(key string) string {
	return filepath.Join(s.verdictDir(), key+".json")
}

// verdictLockPath is the one lock every verdict write and sweep takes: a
// write holds it for a read, a merge and a rename, so one lock for all keys
// costs nothing and leaves no per-key file behind to sweep.
func (s *Store) verdictLockPath() string { return filepath.Join(s.verdictDir(), ".lock") }

func validKey(key string) bool {
	if key == "" || len(key) > maxKeyLen {
		return false
	}
	for i := range len(key) {
		if c := key[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// RecordVerdict adds what a writer measured to the verdict file of key, creating
// it, and answers the file as it now stands (see the merge rules above). lane,
// when not empty, is recorded as a lane that wrote to the key. An add with a
// pending or unknown result, or no runner, unit, law or run to name it, is
// refused whole: nothing of it is stored. A lock held past Options.LockWait is
// ErrConflict, which the caller retries.
func (s *Store) RecordVerdict(ctx context.Context, key, lane string, add Verdict) (Verdict, error) {
	if !validKey(key) {
		return Verdict{}, fmt.Errorf("%w: %q", ErrBadKey, key)
	}
	if err := ctx.Err(); err != nil {
		return Verdict{}, err
	}
	if err := checkAdd(add); err != nil {
		return Verdict{}, err
	}
	if len(add.Runs)+len(add.Laws)+len(add.CI) == 0 {
		cur, _, err := s.ReadVerdict(key)
		return cur, err
	}
	if err := os.MkdirAll(s.verdictDir(), 0o700); err != nil {
		return Verdict{}, fmt.Errorf("store: %w", err)
	}
	release, err := s.lockAt(ctx, s.verdictLockPath())
	if err != nil {
		return Verdict{}, fmt.Errorf("store: verdict %s: %w", key, err)
	}
	defer release()
	cur, _, err := s.readVerdict(key, false)
	if err != nil {
		return Verdict{}, err
	}
	merged := mergeVerdict(cur, add, lane)
	if sameVerdict(cur, merged) {
		s.touchVerdict(s.verdictPath(key)) // a write that adds nothing still used the key
		return merged, nil
	}
	data, err := json.Marshal(verdictFile{F: VerdictFormat, Key: key, Verdict: merged})
	if err != nil {
		return Verdict{}, err
	}
	if err := core.WriteFileAtomic(s.verdictPath(key), data); err != nil {
		return Verdict{}, fmt.Errorf("store: write verdict %s: %w", key, err)
	}
	return merged, nil
}

// ReadVerdict is the verdict file of key. An absent, torn, unparsable or
// another key's file is absent (false, nil), warned once per key; a file of a
// newer format is an error. It takes no lock: a file is replaced whole.
func (s *Store) ReadVerdict(key string) (Verdict, bool, error) { return s.readVerdict(key, true) }

// readVerdict is ReadVerdict; touch says whether the read counts as a use of
// the file. A merge and a sweep read without it: only a stage reusing a
// verdict keeps it alive.
func (s *Store) readVerdict(key string, touch bool) (Verdict, bool, error) {
	if !validKey(key) {
		return Verdict{}, false, fmt.Errorf("%w: %q", ErrBadKey, key)
	}
	path := s.verdictPath(key)
	data, err := core.ReadFileShared(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Verdict{}, false, nil
	case err != nil:
		s.sayVerdict(key, err)
		return Verdict{}, false, nil
	}
	var head struct {
		F int `json:"f"`
	}
	if json.Unmarshal(data, &head) == nil && head.F > VerdictFormat {
		return Verdict{}, false, fmt.Errorf("store: verdict %s: state format %d needs a newer binary: run update", key, head.F)
	}
	var f verdictFile
	if err := json.Unmarshal(data, &f); err != nil {
		s.sayVerdict(key, err)
		return Verdict{}, false, nil
	}
	if f.F != VerdictFormat || f.Key != key {
		s.sayVerdict(key, fmt.Errorf("not a format %d verdict of this key", VerdictFormat))
		return Verdict{}, false, nil
	}
	if touch {
		s.touchVerdict(path)
	}
	return f.Verdict, true, nil
}

// touchVerdict dates a file from now when its last use is more than touchAfter
// old. Failing to is no failure of the read: the file would only age sooner.
func (s *Store) touchVerdict(path string) {
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > touchAfter {
		now := time.Now()
		_ = os.Chtimes(path, now, now)
	}
}

func (s *Store) sayVerdict(key string, why error) {
	name := "verdict:" + key
	s.mu.Lock()
	first := !s.warned[name]
	s.warned[name] = true
	s.mu.Unlock()
	if first {
		s.warn(fmt.Sprintf("store: the verdict file of key %s cannot be read (%v): treating it as absent", key, why))
	}
}

// pendingWord reports a result that says the measurement is not done.
func pendingWord(result string) bool {
	switch result {
	case "", "pending", "deferred", "queued", "running", "waiting", "requested", "in_progress":
		return true
	}
	return false
}

// checkAdd refuses a write that holds a pending result or one no verdict can
// be named by. It looks at all of it before anything is stored.
func checkAdd(add Verdict) error {
	for _, r := range add.Runs {
		switch {
		case r.Runner == "" || r.Unit == "":
			return fmt.Errorf("store: a run verdict names its runner and unit: %+v", r)
		case pendingWord(string(r.Result)) || r.Result == kernel.VerdictNotTested && r.Cause == kernel.CauseDeferred:
			return fmt.Errorf("%w: run %s of %s is %q", ErrPending, r.Runner, r.Unit, r.Result)
		}
		switch r.Result {
		case kernel.VerdictGreen, kernel.VerdictRed, kernel.VerdictRedMissingImpl, kernel.VerdictRedBogus, kernel.VerdictNotTested:
		default:
			return fmt.Errorf("store: run %s of %s has the unknown result %q", r.Runner, r.Unit, r.Result)
		}
	}
	for _, l := range add.Laws {
		switch {
		case l.Law == "":
			return fmt.Errorf("store: a law verdict names its law: %+v", l)
		case pendingWord(l.Result):
			return fmt.Errorf("%w: law %s is %q", ErrPending, l.Law, l.Result)
		case l.Result != LawPass && l.Result != LawRefuse && l.Result != LawUnmeasured:
			return fmt.Errorf("store: law %s has the unknown result %q", l.Law, l.Result)
		}
	}
	for _, c := range add.CI {
		switch {
		case c.Run == "":
			return fmt.Errorf("store: a CI verdict names its run: %+v", c)
		case pendingWord(c.Conclusion):
			return fmt.Errorf("%w: CI run %s is %q", ErrPending, c.Run, c.Conclusion)
		}
	}
	return nil
}

type runID struct {
	runner, unit string
	result       kernel.Verdict
	cause        string
}

type lawID struct{ law, result string }

// mergeVerdict is cur with add's observations beside it: one already recorded
// is not added again, none is replaced or removed. Lists come back sorted.
func mergeVerdict(cur, add Verdict, lane string) Verdict {
	out := Verdict{Lanes: slices.Clone(cur.Lanes)}
	seenRun := map[runID]bool{}
	for _, r := range append(slices.Clone(cur.Runs), add.Runs...) {
		if id := (runID{r.Runner, r.Unit, r.Result, r.Cause}); !seenRun[id] {
			seenRun[id] = true
			out.Runs = append(out.Runs, r)
		}
	}
	seenLaw := map[lawID]bool{}
	for _, l := range append(slices.Clone(cur.Laws), add.Laws...) {
		if id := (lawID{l.Law, l.Result}); !seenLaw[id] {
			seenLaw[id] = true
			out.Laws = append(out.Laws, l)
		}
	}
	seenCI := map[CIVerdict]bool{}
	for _, c := range append(slices.Clone(cur.CI), add.CI...) {
		if !seenCI[c] {
			seenCI[c] = true
			out.CI = append(out.CI, c)
		}
	}
	if lane != "" {
		out.Lanes = append(out.Lanes, lane)
	}
	slices.SortStableFunc(out.Runs, func(a, b RunVerdict) int {
		return cmp.Or(cmp.Compare(a.Runner, b.Runner), cmp.Compare(a.Unit, b.Unit), cmp.Compare(a.Result, b.Result), cmp.Compare(a.Cause, b.Cause))
	})
	slices.SortStableFunc(out.Laws, func(a, b LawVerdict) int {
		return cmp.Or(cmp.Compare(a.Law, b.Law), cmp.Compare(a.Result, b.Result))
	})
	slices.SortStableFunc(out.CI, func(a, b CIVerdict) int {
		return cmp.Or(cmp.Compare(a.OS, b.OS), cmp.Compare(a.By, b.By), cmp.Compare(a.Run, b.Run), cmp.Compare(a.Head, b.Head),
			cmp.Compare(a.Tree, b.Tree), cmp.Compare(a.Conclusion, b.Conclusion), cmp.Compare(a.Mutation, b.Mutation))
	})
	slices.Sort(out.Lanes)
	out.Lanes = slices.Compact(out.Lanes)
	return out
}

// sameVerdict reports whether a merge added nothing: the same observations and lanes.
func sameVerdict(a, b Verdict) bool {
	return slices.Equal(a.Runs, b.Runs) && slices.Equal(a.Laws, b.Laws) && slices.Equal(a.CI, b.CI) && slices.Equal(a.Lanes, b.Lanes)
}
