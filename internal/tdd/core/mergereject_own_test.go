package core

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestRepoStateKey_StableAndUniquePerRepo(t *testing.T) {
	a1 := repoStateKey("/repo/a")
	a2 := repoStateKey("/repo/a")
	b := repoStateKey("/repo/b")

	if a1 != a2 {
		t.Fatalf("repoStateKey must be stable for the same root: %q vs %q", a1, a2)
	}
	if a1 == b {
		t.Fatalf("two different repos must not share a key: %q", a1)
	}
	if len(a1) != 16 { // 8 bytes, hex-encoded
		t.Fatalf("repoStateKey length = %d, want 16", len(a1))
	}
}

func TestRepoStateKey_RelativeAndCleanedSpellingsMatch(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	abs := repoStateKey(dir)
	relative := repoStateKey(".")
	if abs != relative {
		t.Fatalf("absolute and relative spellings of the same dir must key the same: %q vs %q", abs, relative)
	}
}

func TestMergeRejectedMarkerPath_EmptyWithoutAStateDirOrRepoRoot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if got := MergeRejectedMarkerPath(""); got != "" {
		t.Fatalf("MergeRejectedMarkerPath(\"\") = %q, want \"\"", got)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	if got := MergeRejectedMarkerPath("/repo"); got != "" {
		t.Fatalf("MergeRejectedMarkerPath with no state dir = %q, want \"\"", got)
	}
}

func TestMergeRejectedMarkerPath_KeyedByRepoUnderTheStateDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	got := MergeRejectedMarkerPath("/repo/a")
	if !strings.HasPrefix(got, cfg) {
		t.Fatalf("marker path %q should live under the state dir %q", got, cfg)
	}
	if !strings.Contains(got, mergeRejectedPrefix) {
		t.Fatalf("marker path %q should carry the merge-rejected prefix", got)
	}
	if got2 := MergeRejectedMarkerPath("/repo/b"); got2 == got {
		t.Fatal("two different repos must not share a marker path")
	}
}

func TestWriteMergeRejectedMarker_RecordsATimestampAndTheFirstLine(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	WriteMergeRejectedMarker("/repo/a", "Not committing merge; use 'git commit'.\nCONFLICT (content): x")

	path := MergeRejectedMarkerPath("/repo/a")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("marker was not written: %v", err)
	}
	lines := strings.SplitN(string(data), "\n", 2)
	if len(lines) < 2 {
		t.Fatalf("marker body malformed: %q", data)
	}
	if _, err := strconv.ParseInt(lines[0], 10, 64); err != nil {
		t.Fatalf("first line %q is not a unix timestamp: %v", lines[0], err)
	}
	if !strings.HasPrefix(lines[1], "Not committing merge") {
		t.Fatalf("second line should be the message's FIRST line only, got %q", lines[1])
	}
	if strings.Contains(lines[1], "CONFLICT") {
		t.Fatalf("only the first line of the message should be recorded, got %q", lines[1])
	}
}

func TestWriteMergeRejectedMarker_NoStateDirIsANoOp(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	// Must not panic when it has nowhere to write.
	WriteMergeRejectedMarker("/repo/a", "message")
}

func TestFirstLine_StopsAtTheFirstNewline(t *testing.T) {
	cases := []struct{ in, want string }{
		{"one line only", "one line only"},
		{"first\nsecond\nthird", "first"},
		{"\nleading newline", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := firstLine(c.in); got != c.want {
			t.Errorf("firstLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
