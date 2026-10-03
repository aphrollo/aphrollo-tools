package git

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixture reads output `git status --porcelain=v2 -z --branch` really printed,
// captured into testdata/status.
func fixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "status", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

const noSub = "N..."

func TestParseStatus_ReadsEveryRecordShapeGitPrints(t *testing.T) {
	tests := []struct {
		fixture string
		branch  Branch
		entries []Entry
	}{
		{
			fixture: "initial.z",
			branch:  Branch{Head: "main", Initial: true},
		},
		{
			fixture: "detached.z",
			branch:  Branch{OID: "0acbca6fd46debe9a02601865cb50453faeee6fa", Detached: true},
		},
		{
			// The upstream is named, but its branch is gone: git prints no branch.ab line.
			fixture: "upstream_gone.z",
			branch:  Branch{OID: "2d59e0308347acfe8f5dd92c1c1a938db7a9f6d1", Head: "main", Upstream: "origin/vanished"},
		},
		{
			fixture: "ordinary.z",
			branch:  Branch{OID: "0acbca6fd46debe9a02601865cb50453faeee6fa", Head: "main", Upstream: "origin/main", Ahead: 1, Behind: 2, Tracked: true},
			entries: []Entry{
				{Kind: Ordinary, XY: "A.", Sub: noSub, Path: "added.txt"},
				{Kind: Ordinary, XY: "MM", Sub: noSub, Path: "both.txt"},
				{Kind: Ordinary, XY: "D.", Sub: noSub, Path: "del.txt"},
				{Kind: Ordinary, XY: ".M", Sub: noSub, Path: "gone.txt"},
				{Kind: Ordinary, XY: "M.", Sub: noSub, Path: "mod.txt"},
				{Kind: Ordinary, XY: ".D", Sub: noSub, Path: "mode.sh"},
				{Kind: Untracked, Path: ".gitignore"},
				{Kind: Untracked, Path: "del.txt"},
				{Kind: Untracked, Path: "newdir/inside.txt"},
				{Kind: Untracked, Path: "untracked.txt"},
				{Kind: Untracked, Path: "with space.txt"},
			},
		},
		{
			fixture: "ignored.z",
			branch:  Branch{OID: "0acbca6fd46debe9a02601865cb50453faeee6fa", Head: "main", Upstream: "origin/main", Ahead: 1, Behind: 2, Tracked: true},
			entries: []Entry{
				{Kind: Ordinary, XY: "A.", Sub: noSub, Path: "added.txt"},
				{Kind: Ordinary, XY: "MM", Sub: noSub, Path: "both.txt"},
				{Kind: Ordinary, XY: "D.", Sub: noSub, Path: "del.txt"},
				{Kind: Ordinary, XY: ".M", Sub: noSub, Path: "gone.txt"},
				{Kind: Ordinary, XY: "M.", Sub: noSub, Path: "mod.txt"},
				{Kind: Ordinary, XY: ".D", Sub: noSub, Path: "mode.sh"},
				{Kind: Untracked, Path: ".gitignore"},
				{Kind: Untracked, Path: "del.txt"},
				{Kind: Untracked, Path: "newdir/inside.txt"},
				{Kind: Untracked, Path: "untracked.txt"},
				{Kind: Untracked, Path: "with space.txt"},
				{Kind: Ignored, Path: "ignored.log"},
			},
		},
		{
			// The rename's source is the record after it, and the record after that is its own.
			fixture: "rename.z",
			branch:  Branch{OID: "9fc05962efabe57a10409a30e2c01846023da5bd", Head: "main"},
			entries: []Entry{{Kind: Renamed, XY: "R.", Sub: noSub, Score: "R98", Path: "new name.txt", From: "old name.txt"}},
		},
		{
			fixture: "rename_unstaged_edit.z",
			branch:  Branch{OID: "9fc05962efabe57a10409a30e2c01846023da5bd", Head: "main", Upstream: "origin/main", Tracked: true},
			entries: []Entry{{Kind: Renamed, XY: "RM", Sub: noSub, Score: "R100", Path: "moved.txt", From: "src.txt"}},
		},
		{
			fixture: "copy.z",
			branch:  Branch{OID: "9fc05962efabe57a10409a30e2c01846023da5bd", Head: "main", Upstream: "origin/main", Tracked: true},
			entries: []Entry{
				{Kind: Renamed, XY: "C.", Sub: noSub, Score: "C98", Path: "copy.txt", From: "src.txt"},
				{Kind: Ordinary, XY: "M.", Sub: noSub, Path: "src.txt"},
			},
		},
		{
			// A path with a newline and one with a quote and a tab: -z prints both raw.
			fixture: "odd_paths.z",
			branch:  Branch{OID: "ad55b14def273664f4b617a97be435d5cdcbf6e3", Head: "main"},
			entries: []Entry{
				{Kind: Ordinary, XY: "AD", Sub: noSub, Path: "line\nbreak.txt"},
				{Kind: Ordinary, XY: "AD", Sub: noSub, Path: "quo\"te\ttab.txt"},
			},
		},
		{
			fixture: "unmerged.z",
			branch:  Branch{OID: "14d611dbca36fad67f492039dfaaab5d5337c61a", Head: "main"},
			entries: []Entry{
				{Kind: Unmerged, XY: "AA", Sub: noSub, Path: "aa.txt"},
				{Kind: Unmerged, XY: "UD", Sub: noSub, Path: "du.txt"},
				{Kind: Unmerged, XY: "UU", Sub: noSub, Path: "uu.txt"},
			},
		},
		{
			fixture: "submodule_clean.z",
			branch:  Branch{OID: "ff93eeda4e0e239a7dbef6067ef44a0841d6127e", Head: "main"},
		},
		{
			fixture: "submodule_dirty.z",
			branch:  Branch{OID: "ff93eeda4e0e239a7dbef6067ef44a0841d6127e", Head: "main"},
			entries: []Entry{{Kind: Ordinary, XY: ".M", Sub: "S.MU", Path: "libs/sub"}},
		},
		{
			fixture: "submodule_moved.z",
			branch:  Branch{OID: "ff93eeda4e0e239a7dbef6067ef44a0841d6127e", Head: "main"},
			entries: []Entry{{Kind: Ordinary, XY: ".M", Sub: "SC..", Path: "libs/sub"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			got, err := ParseStatus(fixture(t, tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			if got.Branch != tc.branch {
				t.Errorf("branch = %+v, want %+v", got.Branch, tc.branch)
			}
			if len(got.Entries) != len(tc.entries) {
				t.Fatalf("%d entries, want %d: %+v", len(got.Entries), len(tc.entries), got.Entries)
			}
			for i, want := range tc.entries {
				// HeadOID has a test of its own, below.
				got.Entries[i].HeadOID = ""
				if got.Entries[i] != want {
					t.Errorf("entry %d = %+v, want %+v", i, got.Entries[i], want)
				}
			}
		})
	}
}

func TestParseStatus_SaysWhatItCannotRead(t *testing.T) {
	good := "1 M. N... 100644 100644 100644 aaaa bbbb f.txt\x00"
	tests := []struct {
		name, in string
	}{
		{"rename without its source record", "2 R. N... 100644 100644 100644 aaaa bbbb R100 new.txt\x00"},
		{"rename that ends the output before its source", "2 R. N... 100644 100644 100644 aaaa bbbb R100 new.txt"},
		{"unknown record type", good + "x what\x00"},
		{"ordinary record with missing fields", "1 M. N... 100644 f.txt\x00"},
		{"unmerged record with missing fields", "u UU N... 100644 100644 f.txt\x00"},
		{"branch.ab that is not numbers", "# branch.ab +x -y\x00"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := ParseStatus(tc.in); err == nil {
				t.Errorf("parsed %q as %+v, want an error", tc.in, got)
			}
		})
	}
}

func TestStatus_AnswersEachPathListFromTheEntriesTheyBelongTo(t *testing.T) {
	st, err := ParseStatus(fixture(t, "ordinary.z"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"staged":    {"added.txt", "both.txt", "del.txt", "mod.txt"},
		"unstaged":  {"both.txt", "gone.txt", "mode.sh"},
		"untracked": {".gitignore", "del.txt", "newdir/inside.txt", "untracked.txt", "with space.txt"},
		"unmerged":  nil,
	}
	got := map[string][]string{
		"staged":    st.StagedPaths(),
		"unstaged":  st.UnstagedPaths(),
		"untracked": st.UntrackedPaths(),
		"unmerged":  st.UnmergedPaths(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("paths = %q\nwant    %q", got, want)
	}
	if !st.Dirty() {
		t.Error("a tree with changes reads clean")
	}
	clean, _ := ParseStatus(fixture(t, "submodule_clean.z"))
	if clean.Dirty() {
		t.Error("a tree with no entries reads dirty")
	}
}

func TestStatus_ListsAStagedRenameByDestinationAndKeepsItsSource(t *testing.T) {
	st, err := ParseStatus(fixture(t, "rename.z"))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.StagedPaths(); !reflect.DeepEqual(got, []string{"new name.txt"}) {
		t.Errorf("staged = %q, want the destination only", got)
	}
	e, ok := st.Entry("new name.txt")
	if !ok || e.From != "old name.txt" {
		t.Errorf("Entry(new name.txt) = %+v, %v; want the rename from old name.txt", e, ok)
	}
	if _, ok := st.Entry("old name.txt"); ok {
		t.Error("the rename's source is found as an entry of its own")
	}
}

func TestStatus_UnmergedPathsAreNeitherStagedNorUnstaged(t *testing.T) {
	st, err := ParseStatus(fixture(t, "unmerged.z"))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.UnmergedPaths(); !reflect.DeepEqual(got, []string{"aa.txt", "du.txt", "uu.txt"}) {
		t.Errorf("unmerged = %q", got)
	}
	if got := append(st.StagedPaths(), st.UnstagedPaths()...); len(got) != 0 {
		t.Errorf("a conflicted path is also listed as %q", got)
	}
}

func TestEntry_SaysWhichSideChangedAndWhatKindOfEntryItIs(t *testing.T) {
	tests := []struct {
		e                               Entry
		staged, unstaged, copied, isSub bool
	}{
		{e: Entry{Kind: Ordinary, XY: "M."}, staged: true},
		{e: Entry{Kind: Ordinary, XY: ".M"}, unstaged: true},
		{e: Entry{Kind: Ordinary, XY: "MM"}, staged: true, unstaged: true},
		{e: Entry{Kind: Renamed, XY: "C.", Score: "C98"}, staged: true, copied: true},
		{e: Entry{Kind: Renamed, XY: "R.", Score: "R100"}, staged: true},
		{e: Entry{Kind: Unmerged, XY: "UU"}},
		{e: Entry{Kind: Untracked}},
		{e: Entry{Kind: Ordinary, XY: ".M", Sub: "S.M."}, unstaged: true, isSub: true},
	}
	for _, tc := range tests {
		got := [4]bool{tc.e.Staged(), tc.e.Unstaged(), tc.e.Copied(), tc.e.IsSubmodule()}
		if want := [4]bool{tc.staged, tc.unstaged, tc.copied, tc.isSub}; got != want {
			t.Errorf("%+v: staged/unstaged/copied/submodule = %v, want %v", tc.e, got, want)
		}
	}
}

func TestParseStatus_KeepsEveryPathVerbatimWhateverItHolds(t *testing.T) {
	// A path may hold the field separator, and a rename's two paths hold a newline each.
	in := "2 R. N... 100644 100644 100644 aaaa bbbb R90 a b\nc.txt\x00d e\nf.txt\x00? x y  z\x00"
	st, err := ParseStatus(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Kind: Renamed, XY: "R.", Sub: noSub, Score: "R90", Path: "a b\nc.txt", From: "d e\nf.txt", HeadOID: "aaaa"},
		{Kind: Untracked, Path: "x y  z"},
	}
	if !reflect.DeepEqual(st.Entries, want) {
		t.Errorf("entries = %+v\nwant      %+v", st.Entries, want)
	}
	if strings.Contains(strings.Join(st.UntrackedPaths(), ""), "\x00") {
		t.Error("a NUL leaked into a path")
	}
}

func TestParseStatus_NamesTheBlobEachPathHasAtHead(t *testing.T) {
	tests := []struct {
		fixture, path, want string
	}{
		{"ordinary.z", "mod.txt", "78981922613b2afb6025042ff6bd878ac1994e85"},
		{"ordinary.z", "added.txt", ""},
		{"rename.z", "new name.txt", "fcd87345e00673ff10adeb5c83e620d50bb0d62a"},
		{"unmerged.z", "uu.txt", ""},
		{"ordinary.z", "untracked.txt", ""},
	}
	for _, tc := range tests {
		st, err := ParseStatus(fixture(t, tc.fixture))
		if err != nil {
			t.Fatal(err)
		}
		e, ok := st.Entry(tc.path)
		if !ok {
			t.Fatalf("%s: no entry for %s", tc.fixture, tc.path)
		}
		if e.HeadOID != tc.want {
			t.Errorf("%s %s: HeadOID = %q, want %q", tc.fixture, tc.path, e.HeadOID, tc.want)
		}
	}
}
