package suite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The gate runs a project's suite on nearly every edit and at every commit,
// captures the whole run (SuiteResult.Output), classifies it into one word,
// logs that word, and drops the bytes. So the one artefact a session cannot
// reproduce without spending a full suite again — the actual text of the run
// the gate just made — is the one artefact the gate throws away, while the
// narrowing rule (bashsuite.go) refuses the hand rerun that would print it
// again. A refusal with no route out is answered by rewording the command;
// this file is the route out. It keeps the LAST settled run per repo root,
// beside gate.log in stateDir, and `aphrollo gate output` serves it.
//
// A cache, not a journal: one record per root, overwritten by the next
// settled run, because the question it answers is always "what did the run
// the gate JUST made actually print".

// suiteOutputCap bounds ONE record's stored output; the store holds one
// record per repo root, so the machine-wide floor is this times the number
// of roots a box has gated recently, not the size of any one run. A run can print
// megabytes (a workspace build with warnings, a nextest run over hundreds of
// tests) and this store is machine-wide, so it is capped; the TAIL is what
// survives, since a failure prints at the END and the head is scrollback.
const suiteOutputCap = 256 << 10

// suiteOutputPrefix names the record family under stateDir: one file per
// repo root, key-first so a directory listing groups them together — the
// same shape mergeRejectedPrefix already uses.
const suiteOutputPrefix = "suite-output."

// suiteOutputBanner and suiteOutputSeparator bracket the header. The header
// is what lets a reader TRUST the bytes below it (which run, which stage,
// which command, how old, judged as what); the separator is where the run's
// own output starts, byte for byte.
const (
	suiteOutputBanner    = "# aphrollo gate output — the run the gate itself made"
	suiteOutputSeparator = "--- output ---"
)

// suiteOutputRecord is one retained run. Duration and Verdict come from the
// same values the gate.log line carries, so the two cannot describe
// different runs.
type suiteOutputRecord struct {
	At       time.Time
	Stage    string
	Root     string
	Dir      string
	Cmd      string
	Verdict  string
	Duration time.Duration
	Output   string
}

// logSuiteVerdict is the one place a SuiteResult becomes a logged verdict:
// the gate.log line AND the run's retained output, written together so a
// stage cannot record one without the other. Every stage that runs a suite
// and reaches a settled verdict calls this instead of appendGateLog
// directly — post-edit (immediate and deferred), the commit gate's
// mechanical stage, and every stage that blocks through verdictFor.
func logSuiteVerdict(stage, root, cmd, verdict string, res SuiteResult) {
	AppendGateLog(stage, root, cmd, verdict, res.Duration)
	retainSuiteOutput(stage, root, cmd, verdict, res)
}

// retainSuiteOutput stores the bytes of a settled run for root. Best effort,
// exactly like appendGateLog: a store this cannot write must never change a
// gate decision — but it does not fail silently either (see
// warnSuiteOutputUnwritable), because a store that quietly stopped recording
// looks identical to a gate that never ran.
//
// A result with NO output is not retained: a verdict that never spawned a
// runner (a ratchet or docs block) would otherwise overwrite the real run a
// session is about to ask for with nothing at all.
func retainSuiteOutput(stage, root, cmd, verdict string, res SuiteResult) {
	if root == "" || res.Output == "" {
		return
	}
	err := writeSuiteOutputRecord(suiteOutputRecord{
		At: time.Now().UTC(), Stage: stage, Root: root, Dir: res.Dir, Cmd: cmd,
		Verdict: verdict, Duration: res.Duration, Output: res.Output,
	})
	if err != nil {
		warnSuiteOutputUnwritable(err.Error())
	}
}

// suiteOutputWarnOnce keeps a failed store to one line per process, same
// reasoning as appendGateLogWarnOnce: the fact that matters is "output is no
// longer being kept", said once, not once per edit.
var suiteOutputWarnOnce sync.Once

// warnSuiteOutputUnwritable is the one place this store's best-effort write
// becomes visible. It follows warnGateLogUnwritable's precedent exactly: the
// decision is untouched, the failure is not silent.
func warnSuiteOutputUnwritable(reason string) {
	suiteOutputWarnOnce.Do(func() {
		fmt.Fprintf(os.Stderr, "aphrollo gate: run output is not being kept: %s\n", reason)
	})
}

// suiteOutputPath names the record for one repo root. repoStateKey is the
// convention already used for a per-repo state file name (mergereject.go,
// buildslots.go, deferred.go): a path is not a filename — roots carry
// spaces, drive letters and separators — so it is hashed, and the record's
// own header carries the root itself for a reader to match on.
func suiteOutputPath(root string) string {
	dir := StateDir()
	if dir == "" || root == "" {
		return ""
	}
	return filepath.Join(dir, suiteOutputPrefix+repoStateKey(root))
}

// errNoSuiteOutputDir is what every caller gets when there is nowhere to
// keep or find a record at all — the same condition appendGateLog reports.
var errNoSuiteOutputDir = errors.New("no state directory (CLAUDE_CONFIG_DIR unset and no resolvable home), so no run output is kept")

// writeSuiteOutputRecord renders and stores one record, atomically: a reader
// is a separate process and must see either the previous run whole or this
// one whole, never a torn mix of the two.
func writeSuiteOutputRecord(rec suiteOutputRecord) error {
	path := suiteOutputPath(rec.Root)
	if path == "" {
		return errNoSuiteOutputDir
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("could not create %s: %w", filepath.Dir(path), err)
	}
	if err := writeFileAtomic(path, []byte(renderSuiteOutputRecord(rec))); err != nil {
		return fmt.Errorf("could not write %s: %w", path, err)
	}
	return nil
}

// renderSuiteOutputRecord is the stored form: header, separator, then the
// run's own bytes unchanged. Truncation is stated in the header rather than
// implied by a short file — a reader shown a fragment with nothing said
// draws conclusions from a run whose start they never saw.
func renderSuiteOutputRecord(rec suiteOutputRecord) string {
	out, dropped := tailWithinCap(rec.Output, suiteOutputCap)
	var b strings.Builder
	b.WriteString(suiteOutputBanner + "\n")
	fmt.Fprintf(&b, "at: %s\n", rec.At.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "stage: %s\n", rec.Stage)
	fmt.Fprintf(&b, "root: %s\n", rec.Root)
	if rec.Dir != "" {
		fmt.Fprintf(&b, "dir: %s\n", rec.Dir)
	}
	fmt.Fprintf(&b, "command: %s\n", rec.Cmd)
	fmt.Fprintf(&b, "verdict: %s\n", rec.Verdict)
	fmt.Fprintf(&b, "duration: %.1fs\n", rec.Duration.Seconds())
	if dropped > 0 {
		fmt.Fprintf(&b, "bytes: %d kept, TRUNCATED (%d leading bytes dropped; this is the TAIL of the run)\n",
			len(out), dropped)
	} else {
		fmt.Fprintf(&b, "bytes: %d (complete)\n", len(out))
	}
	b.WriteString(suiteOutputSeparator + "\n")
	b.WriteString(out)
	return b.String()
}

// tailWithinCap returns the last limit bytes of s and how many were dropped,
// cut at the first line boundary inside the kept window so a record never
// opens mid-line.
func tailWithinCap(s string, limit int) (string, int) {
	if len(s) <= limit {
		return s, 0
	}
	tail := s[len(s)-limit:]
	if nl := strings.IndexByte(tail, '\n'); nl >= 0 && nl+1 < len(tail) {
		tail = tail[nl+1:]
	}
	return tail, len(s) - len(tail)
}

// RetainedSuiteOutput serves the retained run for the repo root cwd stands
// in: the stored bytes, header and all, unfiltered — this is the lossless
// half of the design contract, and a filtered rendering of a test run is
// exactly the thing a session would then re-run the suite to get around.
//
// The error says WHY there is nothing to serve, in one line: no record for
// this root, or a record too old to describe the tree the session is
// standing in now.
func RetainedSuiteOutput(cwd string) (string, error) {
	root := findRootFrom(cwd)
	if root == "" {
		root = cwd
	}
	dir := StateDir()
	if dir == "" {
		return "", errNoSuiteOutputDir
	}
	rec, text, found := newestSuiteOutputFor(dir, root)
	if !found {
		return "", fmt.Errorf("no gate run output recorded for %s — the gate keeps the last settled run per repo root, and this root has none yet", root)
	}
	if age := time.Since(rec.At); age > bashSuiteVerdictFreshFor {
		return "", fmt.Errorf(
			"the last gate run output for %s is %s old, older than the %s freshness window — it describes a tree this one has moved on from",
			root, age.Round(time.Second), bashSuiteVerdictFreshFor)
	}
	return text, nil
}

// newestSuiteOutputFor picks the record that speaks for root: its own, or
// one recorded for a root NESTED inside it. The gate keys on the nearest
// marker directory — in a Cargo workspace, the member crate — while a
// session types the command wherever it happens to be standing, so an exact
// match alone answers "nothing recorded" for every crate-scoped run. Nesting
// is the same relation sameProject already defines, so a sibling checkout
// still never answers for this one.
func newestSuiteOutputFor(dir, root string) (suiteOutputRecord, string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return suiteOutputRecord{}, "", false
	}
	var best suiteOutputRecord
	bestText := ""
	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), suiteOutputPrefix) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		text := string(raw)
		rec, ok := parseSuiteOutputHeader(text)
		if !ok || !sameProject(rec.Root, root) {
			continue
		}
		if !found || rec.At.After(best.At) {
			best, bestText, found = rec, text, true
		}
	}
	return best, bestText, found
}

// parseSuiteOutputHeader reads back the two header fields a reader has to
// judge on — which root the run was made for, and when. Everything else in
// the header is for the human reading the file, not for this matcher. A
// record whose header it cannot read is reported as no record at all, never
// as a fresh one: showing output the reader cannot vouch for is the failure
// this whole file exists to prevent.
func parseSuiteOutputHeader(text string) (suiteOutputRecord, bool) {
	head, _, cut := strings.Cut(text, suiteOutputSeparator+"\n")
	if !cut {
		return suiteOutputRecord{}, false
	}
	var rec suiteOutputRecord
	for _, line := range strings.Split(head, "\n") {
		key, val, ok := strings.Cut(strings.TrimRight(line, "\r"), ": ")
		if !ok {
			continue
		}
		switch key {
		case "root":
			rec.Root = val
		case "at":
			at, err := time.Parse(time.RFC3339, val)
			if err != nil {
				// absence-ok: a record this reader cannot DATE is one it
				// cannot judge fresh, which is indistinguishable from no
				// record at all; the caller has no use for the parse error,
				// only for "do not serve these bytes".
				return suiteOutputRecord{}, false
			}
			rec.At = at
		case "stage":
			rec.Stage = val
		case "verdict":
			rec.Verdict = val
		}
	}
	if rec.Root == "" || rec.At.IsZero() {
		return suiteOutputRecord{}, false
	}
	return rec, true
}
