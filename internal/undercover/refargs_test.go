package undercover

import (
	"strings"
	"testing"
)

// The classification alone, for the shapes a real repo cannot cheaply stage:
// a bare verb creates nothing and must not index past its arguments.
func TestUndercoverRefNames_ReadsEachVerbsNewNameOnly(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		rest    []string
		names   []string
		current bool
	}{
		{nil, nil, false},
		{[]string{"worktree"}, nil, false},
		{[]string{"worktree", "list"}, nil, false},
		{[]string{"worktree", "add", "-B", "lane/x", "../x"}, []string{"lane/x"}, false},
		{[]string{"checkout", "-b"}, nil, false},
		{[]string{"checkout", "--", "-b", "x"}, nil, false},
		{[]string{"checkout", "--orphan=lane/o"}, []string{"lane/o"}, false},
		{[]string{"branch"}, nil, false},
		{[]string{"branch", "-m", "lane/new"}, []string{"lane/new"}, false},
		{[]string{"branch", "-m", "lane/old", "lane/new"}, []string{"lane/new"}, false},
		{[]string{"branch", "lane/a", "main"}, []string{"lane/a"}, false},
		{[]string{"branch", "--set-upstream-to=origin/x"}, nil, false},
		{[]string{"push"}, nil, true},
		{[]string{"push", "origin"}, nil, true},
		{[]string{"push", "origin", "HEAD"}, nil, true},
		{[]string{"push", "-o", "ci.skip", "origin", "lane/a"}, []string{"lane/a"}, false},
		{[]string{"push", "--repo", "origin", "lane/a", "lane/b"}, []string{"lane/b"}, false},
		{[]string{"push", "origin", ":lane/gone", "+lane/a:lane/b"}, []string{"lane/b"}, false},
		{[]string{"push", "--all", "origin"}, nil, false},
		{[]string{"status"}, nil, false},
	} {
		_, names, current := RefArgs(c.rest)
		if strings.Join(names, ",") != strings.Join(c.names, ",") || current != c.current {
			t.Errorf("%v: got (%q, %v), want (%q, %v)", c.rest, names, current, c.names, c.current)
		}
	}
}
