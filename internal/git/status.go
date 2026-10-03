package git

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// Kind is the record type a porcelain v2 status line opens with.
type Kind byte

const (
	// Ordinary is a changed path (`1`).
	Ordinary Kind = '1'
	// Renamed is a renamed or copied path (`2`); Score tells which.
	Renamed Kind = '2'
	// Unmerged is a path in conflict (`u`).
	Unmerged Kind = 'u'
	// Untracked is a path git does not track (`?`).
	Untracked Kind = '?'
	// Ignored is a path a gitignore rule hides (`!`), listed only under --ignored.
	Ignored Kind = '!'
)

// Branch is the `# branch.*` header of a status. OID is "" in a repository with
// no commit, Head is "" on a detached HEAD, Upstream is "" with none set, and
// Tracked says Ahead and Behind were printed: git leaves them out when the
// upstream branch is gone.
type Branch struct {
	OID, Head, Upstream string
	Ahead, Behind       int
	Tracked             bool
	Initial, Detached   bool
}

// Entry is one path of a status. XY is the index and worktree state, `.` for
// unchanged; Sub is the four-character submodule field (`N...` when the path
// is no submodule); Score ("R100", "C75") and From (the source) are set for a
// rename or copy. An untracked or ignored path has only Kind and Path.
type Entry struct {
	Kind              Kind
	XY, Sub           string
	Score, Path, From string
}

// side is the state letter of one side of XY, `.` when the entry has none.
func (e Entry) side(i int) byte {
	if len(e.XY) != 2 {
		return '.'
	}
	return e.XY[i]
}

// Staged reports a change recorded in the index. A conflicted path is neither
// staged nor unstaged.
func (e Entry) Staged() bool {
	return (e.Kind == Ordinary || e.Kind == Renamed) && e.side(0) != '.'
}

// Unstaged reports a change in the worktree not yet in the index.
func (e Entry) Unstaged() bool {
	return (e.Kind == Ordinary || e.Kind == Renamed) && e.side(1) != '.'
}

// Copied reports a copy rather than a rename.
func (e Entry) Copied() bool { return e.Kind == Renamed && strings.HasPrefix(e.Score, "C") }

// IsSubmodule reports a path that is a submodule.
func (e Entry) IsSubmodule() bool { return strings.HasPrefix(e.Sub, "S") }

// Status is one `git status --porcelain=v2 -z --branch` answer, entries in the
// order git printed them, which is by path.
type Status struct {
	Branch  Branch
	Entries []Entry
}

// Dirty reports any entry at all.
func (s *Status) Dirty() bool { return len(s.Entries) > 0 }

// Entry is the entry for path, a rename's destination included; a rename's
// source is no entry of its own.
func (s *Status) Entry(path string) (Entry, bool) {
	for _, e := range s.Entries {
		// A repo-relative path git printed is compared as the bytes it is.
		if cmp.Compare(e.Path, path) == 0 {
			return e, true
		}
	}
	return Entry{}, false
}

// paths lists the paths of the entries keep accepts.
func (s *Status) paths(keep func(Entry) bool) []string {
	var out []string
	for _, e := range s.Entries {
		if keep(e) {
			out = append(out, e.Path)
		}
	}
	return out
}

// StagedPaths lists the paths with a change in the index, a rename by its
// destination.
func (s *Status) StagedPaths() []string { return s.paths(Entry.Staged) }

// UnstagedPaths lists the paths with a change in the worktree.
func (s *Status) UnstagedPaths() []string { return s.paths(Entry.Unstaged) }

// UntrackedPaths lists the paths git does not track.
func (s *Status) UntrackedPaths() []string {
	return s.paths(func(e Entry) bool { return e.Kind == Untracked })
}

// UnmergedPaths lists the paths in conflict.
func (s *Status) UnmergedPaths() []string {
	return s.paths(func(e Entry) bool { return e.Kind == Unmerged })
}

// Fields before the path of each record type, which the path (it may hold a
// space) follows as the last field.
const (
	ordinaryFields = 9
	renamedFields  = 10
	unmergedFields = 11
)

// ParseStatus reads the output of `git status --porcelain=v2 -z --branch`. Git
// ends every record with a NUL and prints a path raw, so a path with a space
// or a newline arrives as it is; a rename's source is the record after it.
// Output it cannot read is an error, never a guess.
func ParseStatus(out string) (*Status, error) {
	st := &Status{}
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		rec := records[i]
		if rec == "" {
			continue
		}
		if header, ok := strings.CutPrefix(rec, "# "); ok {
			if err := st.parseHeader(header); err != nil {
				return nil, err
			}
			continue
		}
		e, err := parseEntry(rec)
		if err != nil {
			return nil, err
		}
		if e.Kind == Renamed {
			i++
			if i >= len(records) || records[i] == "" {
				return nil, fmt.Errorf("git status: the rename of %q has no source record", e.Path)
			}
			e.From = records[i]
		}
		st.Entries = append(st.Entries, e)
	}
	return st, nil
}

func (s *Status) parseHeader(h string) error {
	key, val, _ := strings.Cut(h, " ")
	b := &s.Branch
	switch key {
	case "branch.oid":
		b.OID = val
		if val == "(initial)" {
			b.OID, b.Initial = "", true
		}
	case "branch.head":
		b.Head = val
		if val == "(detached)" {
			b.Head, b.Detached = "", true
		}
	case "branch.upstream":
		b.Upstream = val
	case "branch.ab":
		var err error
		ahead, behind, _ := strings.Cut(val, " ")
		if b.Ahead, err = strconv.Atoi(strings.TrimPrefix(ahead, "+")); err != nil {
			return fmt.Errorf("git status: branch.ab %q: %w", val, err)
		}
		if b.Behind, err = strconv.Atoi(strings.TrimPrefix(behind, "-")); err != nil {
			return fmt.Errorf("git status: branch.ab %q: %w", val, err)
		}
		b.Tracked = true
	}
	return nil
}

func parseEntry(rec string) (Entry, error) {
	kind := Kind(rec[0])
	switch kind {
	case Untracked, Ignored:
		if len(rec) < 3 {
			return Entry{}, fmt.Errorf("git status: record %q has no path", rec)
		}
		return Entry{Kind: kind, Path: rec[2:]}, nil
	case Ordinary:
		f := strings.SplitN(rec, " ", ordinaryFields)
		if len(f) < ordinaryFields {
			return Entry{}, fmt.Errorf("git status: record %q has %d fields, want %d", rec, len(f), ordinaryFields)
		}
		return Entry{Kind: kind, XY: f[1], Sub: f[2], Path: f[8]}, nil
	case Renamed:
		f := strings.SplitN(rec, " ", renamedFields)
		if len(f) < renamedFields {
			return Entry{}, fmt.Errorf("git status: record %q has %d fields, want %d", rec, len(f), renamedFields)
		}
		return Entry{Kind: kind, XY: f[1], Sub: f[2], Score: f[8], Path: f[9]}, nil
	case Unmerged:
		f := strings.SplitN(rec, " ", unmergedFields)
		if len(f) < unmergedFields {
			return Entry{}, fmt.Errorf("git status: record %q has %d fields, want %d", rec, len(f), unmergedFields)
		}
		return Entry{Kind: kind, XY: f[1], Sub: f[2], Path: f[10]}, nil
	}
	return Entry{}, fmt.Errorf("git status: unrecognised record %q", rec)
}
