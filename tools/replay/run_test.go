package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPickPrevious_NewestTagThatIsNotTheCandidate(t *testing.T) {
	shas := map[string]string{"v1.4.6": "bbb", "v1.4.5": "aaa", "v1.4.4": "999"}
	sha := func(tag string) (string, error) {
		if s, ok := shas[tag]; ok {
			return s, nil
		}
		return "", errors.New("no such tag")
	}
	tests := []struct {
		name string
		tags []string
		head string
		want string
	}{
		{name: "newest tag", tags: []string{"v1.4.6", "v1.4.5"}, head: "ccc", want: "v1.4.6"},
		{name: "head is tagged: the release before it", tags: []string{"v1.4.6", "v1.4.5", "v1.4.4"}, head: "bbb", want: "v1.4.5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pickPrevious(tc.tags, sha, tc.head)
			if err != nil || got != tc.want {
				t.Fatalf("pickPrevious = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
	if _, err := pickPrevious(nil, sha, "ccc"); err == nil {
		t.Error("no tag at all: want an error naming the missing release, got none")
	}
	if _, err := pickPrevious([]string{"v1.4.6"}, sha, "bbb"); err == nil {
		t.Error("the only tag is the candidate itself: want an error, got none")
	}
}

// treeWith is a tree on disk holding the named files, each a one-line comment
// naming itself.
func treeWith(t *testing.T, name string, files ...string) tree {
	t.Helper()
	tr := tree{Name: name, Dir: t.TempDir(), Files: files}
	for _, f := range files {
		full := filepath.Join(tr.Dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("// "+f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return tr
}

// call is one command the fake binaries were asked to run.
type call struct {
	Label string
	Args  string
	Stdin string
}

// fakeRun answers every command the way a binary that finds nothing would,
// and records the order in which they arrived.
func fakeRun(calls *[]call, fail func(c call) error) runFunc {
	return func(b bin, dir, stdin string, args ...string) (result, error) {
		c := call{b.Label, strings.Join(args, " "), stdin}
		*calls = append(*calls, c)
		if fail != nil {
			if err := fail(c); err != nil {
				return result{}, err
			}
		}
		if len(args) > 0 && args[0] == "ratchet" {
			return result{Stdout: `{"laws":1,"findings":null}`}, nil
		}
		return result{}, nil
	}
}

func TestCompareTree_RunsTheCandidateThenThePreviousReleaseOnOneStore(t *testing.T) {
	var calls []call
	tr := treeWith(t, "synthetic", "web/a.ts", "svc/b.py")
	tr.PrevMustRead = true
	reps := compareTree(fakeRun(&calls, nil), bin{"previous", "p"}, bin{"candidate", "c"}, tr)

	for _, r := range reps {
		if r.failed() {
			t.Errorf("a binary that finds nothing failed a leg: %+v", r)
		}
	}
	var legs []string
	for _, r := range reps {
		legs = append(legs, r.Leg)
	}
	want := []string{"ratchet", "docs", "gate", "ratchet (warm)", "store"}
	if !slices.Equal(legs, want) {
		t.Fatalf("legs = %v, want %v", legs, want)
	}
	// The previous release reads the store only after the candidate has written
	// to it: every candidate call comes before every previous call.
	firstPrev := slices.IndexFunc(calls, func(c call) bool { return c.Label == "previous" })
	lastCand := -1
	for i, c := range calls {
		if c.Label == "candidate" {
			lastCand = i
		}
	}
	if firstPrev < 0 || lastCand > firstPrev {
		t.Fatalf("calls are not candidate-then-previous: %v", calls)
	}
	cold, warm, status := 0, 0, 0
	for _, c := range calls {
		if c.Label != "previous" {
			continue
		}
		switch c.Args {
		case "gate status":
			status++
		case "ratchet check --dry --no-cache --format json":
			cold++
		case "ratchet check --dry --format json":
			warm++
		}
	}
	if cold != 1 || warm != 1 {
		t.Errorf("previous release ran %d cold and %d warm ratchet reads, want one of each", cold, warm)
	}
	if status != 1 {
		t.Errorf("previous release read the store with `gate status` %d times, want once", status)
	}
}

func TestCompareTree_JudgesEachFileAsANewWrite(t *testing.T) {
	var calls []call
	tr := treeWith(t, "self", "web/a.ts", "svc/b.py")
	compareTree(fakeRun(&calls, nil), bin{"previous", "p"}, bin{"candidate", "c"}, tr)

	var payloads []map[string]any
	for _, c := range calls {
		if c.Label == "candidate" && c.Args == "gate pretooluse" {
			var p map[string]any
			if err := json.Unmarshal([]byte(c.Stdin), &p); err != nil {
				t.Fatalf("payload %q: %v", c.Stdin, err)
			}
			payloads = append(payloads, p)
		}
	}
	if len(payloads) != 2 {
		t.Fatalf("the candidate judged %d files, want 2", len(payloads))
	}
	in := payloads[0]["tool_input"].(map[string]any)
	if got, want := in["file_path"].(string), filepath.Join(tr.Dir, "web", "replaynew_a.ts"); got != want {
		t.Errorf("file_path = %q, want %q: a path that does not exist, so the whole file reads as added", got, want)
	}
	if got := in["content"].(string); got != "// web/a.ts\n" {
		t.Errorf("content = %q, want the file's own bytes", got)
	}
	// A note the gate shows once per session would show for the binary that
	// reads first and not for the one that reads second, though neither differs.
	var prevSession any
	for _, c := range calls {
		if c.Label == "previous" && c.Args == "gate pretooluse" {
			var p map[string]any
			_ = json.Unmarshal([]byte(c.Stdin), &p)
			prevSession = p["session_id"]
			break
		}
	}
	if prevSession == nil || prevSession == payloads[0]["session_id"] {
		t.Errorf("session ids: candidate %v, previous %v, want two different ones", payloads[0]["session_id"], prevSession)
	}
	if payloads[0]["tool_name"] != "Write" || payloads[0]["hook_event_name"] != "PreToolUse" {
		t.Errorf("payload is not a PreToolUse Write: %v", payloads[0])
	}
}

func TestCompareTree_ABinaryThatCannotBeRunIsAFailedLeg(t *testing.T) {
	var calls []call
	boom := func(c call) error {
		if c.Label == "candidate" && c.Args == "docs check" {
			return errors.New("timed out")
		}
		return nil
	}
	tr := treeWith(t, "self")
	reps := compareTree(fakeRun(&calls, boom), bin{"previous", "p"}, bin{"candidate", "c"}, tr)
	failed := 0
	for _, r := range reps {
		if r.failed() {
			failed++
			if r.Leg != "docs" || !strings.Contains(r.Failure, "timed out") {
				t.Errorf("unexpected failed leg %+v", r)
			}
		}
	}
	if failed != 1 {
		t.Fatalf("%d legs failed, want only docs", failed)
	}
}
