package core

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// The events a binary before the one that moved the readers wrote carry no
// command and no root, so a reader that matches on them starts from nothing
// after the upgrade. gate.log is still written (the merge readers use it), so
// the history from before the first complete event is read from there, once:
// the two never overlap, and the fallback goes with the gate.log writer.

// legacyTailBytes bounds how much of the end of gate.log is read.
const legacyTailBytes = 4 << 20

// readLegacyGateEntries is the stage lines in the end of gate.log at or after
// since, oldest first; none when the file is absent.
func readLegacyGateEntries(since time.Time) []gateEntry {
	path := GateLogPath()
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > legacyTailBytes {
		if _, err := f.Seek(st.Size()-legacyTailBytes, io.SeekStart); err != nil {
			return nil
		}
	}
	var out []gateEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		if e, ok := parseGateLine(sc.Text()); ok && !e.At.Before(since) {
			out = append(out, e)
		}
	}
	return out
}

// completeFrom is when the first event that carries its own root or command was
// written, the zero time when there is none.
func completeFrom(events []Event) time.Time {
	var first time.Time
	for _, ev := range events {
		if ev.Stage == "" || ev.Kind == "push" || (ev.Root == "" && ev.Cmd == "") {
			continue
		}
		at, err := time.Parse(time.RFC3339, ev.At)
		if err != nil {
			continue
		}
		if first.IsZero() || at.Before(first) {
			first = at
		}
	}
	return first.Truncate(time.Second)
}

// entriesWithHistory is the stage lines of events at or after since, with the
// lines of gate.log that keeps stands for before the first complete event in
// place of the incomplete events written until then. With no gate.log history
// the events are used as they are.
func entriesWithHistory(events []Event, since time.Time, keeps func(gateEntry) bool) []gateEntry {
	legacy := readLegacyGateEntries(since)
	var older []gateEntry
	from := completeFrom(events)
	for _, e := range legacy {
		if (from.IsZero() || e.At.Before(from)) && (keeps == nil || keeps(e)) {
			older = append(older, e)
		}
	}
	if len(older) == 0 {
		return entriesOf(events, since)
	}
	var out []gateEntry
	out = append(out, older...)
	for _, e := range entriesOf(events, since) {
		if !from.IsZero() && !e.At.Before(from) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// sameRepoAs keeps the entries whose root belongs to the repository root does.
func sameRepoAs(root string) func(gateEntry) bool {
	want, _, _ := repoIdentity(root)
	seen := map[string]string{}
	return func(e gateEntry) bool {
		repo, ok := seen[e.Root]
		if !ok {
			repo, _, _ = repoIdentity(e.Root)
			seen[e.Root] = repo
		}
		return repo == want
	}
}

// GateHistoryStart is when the earliest stage line the readers can see was
// written, and whether there is one; `gate stats` states it when a window it
// was asked for opens before it.
func GateHistoryStart() (time.Time, bool) {
	entries := readAllGateEntries(time.Time{})
	if len(entries) == 0 {
		return time.Time{}, false
	}
	return entries[0].At, true
}

// EventsNewerSchema reports the highest version a record of the newest month of
// the event logs carries when it EXCEEDS this binary's, so a reader says so
// instead of tallying records whose shape it cannot vouch for (they are
// otherwise skipped unread).
func EventsNewerSchema() (int, bool) {
	root := StateRoot()
	if root == "" {
		return 0, false
	}
	dirs, _ := filepath.Glob(filepath.Join(root, "state", "*"))
	highest := 0
	for _, d := range dirs {
		names, _ := filepath.Glob(filepath.Join(d, "events-*.jsonl"))
		if len(names) == 0 {
			continue
		}
		sort.Strings(names)
		if v := highestVersion(names[len(names)-1]); v > highest {
			highest = v
		}
	}
	return highest, highest > EventSchema
}

func highestVersion(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	highest := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var v struct {
			V int `json:"v"`
		}
		if json.Unmarshal(sc.Bytes(), &v) == nil && v.V > highest {
			highest = v.V
		}
	}
	return highest
}
