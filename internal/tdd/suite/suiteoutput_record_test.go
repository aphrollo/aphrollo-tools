package suite

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The stored record is the header a reader judges the run by, then the run's
// own bytes unchanged; parsing it back recovers the root, time, stage and
// verdict it was written with.
func TestSuiteOutputRecord_RoundTripsTheHeaderAndKeepsTheBytesVerbatim(t *testing.T) {
	at := time.Date(2026, 9, 24, 8, 30, 0, 0, time.UTC)
	rec := suiteOutputRecord{
		At: at, Stage: "posttooluse", Root: "/w/forge", Dir: "/w",
		Cmd: "go test ./...", Verdict: "red", Duration: 1500 * time.Millisecond,
		Output: "--- FAIL: TestWear\nFAIL\n",
	}

	text := renderSuiteOutputRecord(rec)

	for _, want := range []string{
		"at: 2026-09-24T08:30:00Z\n", "stage: posttooluse\n", "root: /w/forge\n", "dir: /w\n",
		"command: go test ./...\n", "verdict: red\n", "duration: 1.5s\n", "bytes: 24 (complete)\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("header missing %q:\n%s", want, text)
		}
	}
	if !strings.HasSuffix(text, suiteOutputSeparator+"\n--- FAIL: TestWear\nFAIL\n") {
		t.Errorf("the run's bytes do not follow the separator verbatim:\n%s", text)
	}
	got, ok := parseSuiteOutputHeader(text)
	if !ok || got.Root != rec.Root || !got.At.Equal(at) || got.Stage != rec.Stage || got.Verdict != rec.Verdict {
		t.Fatalf("parseSuiteOutputHeader = (%+v, %v), want root/at/stage/verdict of %+v", got, ok, rec)
	}
}

// A record with no dir leaves the dir line out rather than writing an empty
// one.
func TestRenderSuiteOutputRecord_OmitsAnEmptyDir(t *testing.T) {
	text := renderSuiteOutputRecord(suiteOutputRecord{At: time.Unix(0, 0), Root: "/w", Output: "ok\n"})
	if strings.Contains(text, "dir:") {
		t.Errorf("an empty dir was written:\n%s", text)
	}
}

// An output over the cap keeps its tail from the first whole line, and the
// header says it was cut and by how much.
func TestRenderSuiteOutputRecord_StatesTruncationAndKeepsTheTail(t *testing.T) {
	line := strings.Repeat("x", 1023) + "\n" // 1 KiB per line
	output := strings.Repeat(line, 300) + "FAIL: TestLast\n"

	text := renderSuiteOutputRecord(suiteOutputRecord{At: time.Unix(0, 0), Root: "/w", Output: output})

	_, body, _ := strings.Cut(text, suiteOutputSeparator+"\n")
	if len(body) > suiteOutputCap || !strings.HasSuffix(body, "FAIL: TestLast\n") || !strings.HasPrefix(body, "x") {
		t.Fatalf("body is %d bytes (cap %d), want the tail from a line start ending in the last line", len(body), suiteOutputCap)
	}
	want := "TRUNCATED (" + strconv.Itoa(len(output)-len(body)) + " leading bytes dropped"
	if !strings.Contains(text, "bytes: "+strconv.Itoa(len(body))+" kept, "+want) {
		t.Errorf("header does not state the cut (%d kept, %d dropped):\n%s", len(body), len(output)-len(body), text[:strings.Index(text, suiteOutputSeparator)])
	}
}

func TestTailWithinCap_CutsAtTheFirstLineBoundaryInsideTheWindow(t *testing.T) {
	cases := []struct {
		s           string
		limit       int
		want        string
		wantDropped int
	}{
		{"abc\n", 4, "abc\n", 0},           // exactly at the cap: untouched
		{"aaa\nbbb\nccc\n", 6, "ccc\n", 8}, // window "b\nccc\n" opens mid-line
		{"aaaaaaaaaa", 4, "aaaa", 6},       // no newline in the window
		{"aaaaaa\nbbbb\n", 5, "bbbb\n", 7}, // window starts right on a boundary
		{"aaaaaaa\n", 3, "aa\n", 5},        // only newline is the last byte
		{"aaaa\nbbbbbbb", 6, "bbbbbb", 6},  // window has no boundary before its end
	}
	for _, tc := range cases {
		got, dropped := tailWithinCap(tc.s, tc.limit)
		if got != tc.want || dropped != tc.wantDropped {
			t.Errorf("tailWithinCap(%q, %d) = (%q, %d), want (%q, %d)", tc.s, tc.limit, got, dropped, tc.want, tc.wantDropped)
		}
	}
}

// A header the reader cannot vouch for — no separator, no root, no date, or
// a date it cannot parse — reads as no record at all.
func TestParseSuiteOutputHeader_RefusesARecordItCannotVouchFor(t *testing.T) {
	sep := suiteOutputSeparator + "\n"
	cases := map[string]string{
		"no separator": "root: /w\nat: 2026-09-24T08:30:00Z\nok\n",
		"no root":      "at: 2026-09-24T08:30:00Z\n" + sep,
		"no date":      "root: /w\n" + sep,
		"bad date":     "root: /w\nat: yesterday\n" + sep,
	}
	for name, text := range cases {
		if rec, ok := parseSuiteOutputHeader(text); ok {
			t.Errorf("%s: parsed as %+v, want refused", name, rec)
		}
	}
	if _, ok := parseSuiteOutputHeader("root: /w\r\nat: 2026-09-24T08:30:00Z\r\n" + sep); !ok {
		t.Error("a CRLF header was refused")
	}
}

// The record answering for a root is the newest of its own and those of
// roots nested inside it; a sibling checkout's record, a file outside the
// family and an unreadable header never answer.
func TestNewestSuiteOutputFor_PicksTheNewestOwnOrNestedRecord(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(t.TempDir(), "forge")
	base := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	put := func(name, recRoot string, at time.Time, out string) {
		t.Helper()
		text := renderSuiteOutputRecord(suiteOutputRecord{At: at, Root: recRoot, Output: out})
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put(suiteOutputPrefix+"own", root, base, "own\n")
	put(suiteOutputPrefix+"nested", filepath.Join(root, "crates", "a"), base.Add(time.Minute), "nested\n")
	put(suiteOutputPrefix+"sibling", root+"-other", base.Add(time.Hour), "sibling\n")
	put("unrelated", root, base.Add(2*time.Hour), "unrelated\n")
	if err := os.WriteFile(filepath.Join(dir, suiteOutputPrefix+"torn"), []byte("root: "+root+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, suiteOutputPrefix+"adir"), 0o700); err != nil {
		t.Fatal(err)
	}

	rec, text, found := newestSuiteOutputFor(dir, root)

	if !found || !rec.At.Equal(base.Add(time.Minute)) || !strings.HasSuffix(text, "nested\n") {
		t.Fatalf("newestSuiteOutputFor = (%v, found %v, %q), want the nested record", rec.At, found, text)
	}
	if _, _, found := newestSuiteOutputFor(filepath.Join(dir, "missing"), root); found {
		t.Error("a missing state dir produced a record")
	}
}

// RetainedSuiteOutput serves a fresh record for the cwd's root, and refuses
// with a reason both when there is none and when the one there is too old to
// describe this tree.
func TestRetainedSuiteOutput_ServesOnlyAFreshRecordForThisRoot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()

	if _, err := RetainedSuiteOutput(root); err == nil || !strings.Contains(err.Error(), "no gate run output recorded") {
		t.Fatalf("no record: err = %v, want the no-record reason", err)
	}

	retainSuiteOutput("posttooluse", root, "go test ./...", "green", SuiteResult{Output: "ok  m 0.1s\n"})
	text, err := RetainedSuiteOutput(root)
	if err != nil || !strings.HasSuffix(text, suiteOutputSeparator+"\nok  m 0.1s\n") {
		t.Fatalf("fresh record: (%q, %v), want it served", text, err)
	}

	retainSuiteOutput("posttooluse", root, "go test ./...", "green", SuiteResult{})
	if again, _ := RetainedSuiteOutput(root); again != text {
		t.Error("a run with no output overwrote the retained record")
	}

	stale := renderSuiteOutputRecord(suiteOutputRecord{At: time.Now().Add(-bashSuiteVerdictFreshFor - time.Minute), Root: root, Output: "old\n"})
	if err := os.WriteFile(suiteOutputPath(root), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RetainedSuiteOutput(root); err == nil || !strings.Contains(err.Error(), "freshness window") {
		t.Fatalf("stale record: err = %v, want the freshness reason", err)
	}
}
