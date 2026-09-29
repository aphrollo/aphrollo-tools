package suite

import (
	"reflect"
	"sort"
	"testing"
)

// These are suite's own tests of runscope.go's scope arithmetic, reached today
// only through internal/tdd/postedit's rerun-block tests.

// pkgList is a scope's package names, sorted.
func pkgList(s runScope) []string {
	var out []string
	for p := range s.pkgs {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// TestCargoRunScope_ANeutralValueFlagDoesNotSwallowTheNextFlagsAsPackages pins
// the neutral flags: `--jobs 4` consumes its value so 4 is not read as a name
// filter, and `--jobs=4` consumes nothing further.
func TestCargoRunScope_ANeutralValueFlagDoesNotSwallowTheNextFlagsAsPackages(t *testing.T) {
	t.Parallel()
	s := cargoRunScope([]string{"--jobs", "4", "-p", "alpha"})
	if got := pkgList(s); !reflect.DeepEqual(got, []string{"alpha"}) || len(s.filters) != 0 {
		t.Fatalf("scope = %+v, want only package alpha and no filter", s)
	}
	s = cargoRunScope([]string{"--jobs=4", "-p", "alpha"})
	if got := pkgList(s); !reflect.DeepEqual(got, []string{"alpha"}) || len(s.filters) != 0 {
		t.Fatalf("inline scope = %+v, want only package alpha and no filter", s)
	}
}

// TestCargoRunScope_ReadsPackagesFiltersAndWholeMarkers pins the rest of the
// reading: -p names a package, a positional word is a name filter, --workspace
// is the whole tree, and a value flag's value is taken.
func TestCargoRunScope_ReadsPackagesFiltersAndWholeMarkers(t *testing.T) {
	t.Parallel()
	s := cargoRunScope([]string{"-p", "alpha", "--package=beta", "widget", "--test", "it"})
	if got := pkgList(s); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Errorf("pkgs = %v, want alpha and beta", got)
	}
	if want := []string{"--test it", "widget"}; !reflect.DeepEqual(s.filters, want) {
		t.Errorf("filters = %v, want %v", s.filters, want)
	}
	if !cargoRunScope([]string{"--workspace"}).whole {
		t.Error("--workspace is the whole tree")
	}
	if !cargoRunScope(nil).whole {
		t.Error("a run naming no package is whole")
	}
}

// TestScopeCovers_TheLawByCase pins the whole coverage rule, case by case: a
// filtered run answers only its own scope; a whole run answers everything; a
// package run answers a subset of its packages and not a whole or
// unnamed-package ask.
func TestScopeCovers_TheLawByCase(t *testing.T) {
	t.Parallel()
	pk := func(names ...string) runScope {
		s := runScope{pkgs: map[string]bool{}}
		for _, n := range names {
			s.pkgs[n] = true
		}
		return s
	}
	filtered := pk("a")
	filtered.filters = []string{"-run X"}
	cases := []struct {
		name       string
		have, want runScope
		covers     bool
	}{
		{"whole covers any", wholeRunScope(), pk("a"), true},
		{"filtered covers exactly itself", filtered, filtered, true},
		{"filtered does not cover the unfiltered", filtered, pk("a"), false},
		{"packages cover a subset", pk("a", "b"), pk("a"), true},
		{"packages do not cover a missing one", pk("a"), pk("a", "b"), false},
		{"packages do not cover whole", pk("a"), wholeRunScope(), false},
		{"packages do not cover an unnamed ask", pk("a"), runScope{pkgs: map[string]bool{}}, false},
	}
	for _, c := range cases {
		if got := scopeCovers(c.have, c.want); got != c.covers {
			t.Errorf("%s: scopeCovers = %v, want %v", c.name, got, c.covers)
		}
	}
}

// TestScopeEqual_ComparesWidthPackagesAndFilters pins each axis of equality.
func TestScopeEqual_ComparesWidthPackagesAndFilters(t *testing.T) {
	t.Parallel()
	a := runScope{pkgs: map[string]bool{"x": true}, filters: []string{"f"}}
	same := runScope{pkgs: map[string]bool{"x": true}, filters: []string{"f"}}
	if !scopeEqual(a, same) {
		t.Error("identical scopes are equal")
	}
	if scopeEqual(a, runScope{whole: true, pkgs: map[string]bool{"x": true}, filters: []string{"f"}}) {
		t.Error("width differs")
	}
	if scopeEqual(a, runScope{pkgs: map[string]bool{"y": true}, filters: []string{"f"}}) {
		t.Error("package differs")
	}
	if scopeEqual(a, runScope{pkgs: map[string]bool{"x": true}, filters: []string{"g"}}) {
		t.Error("filter differs")
	}
	if scopeEqual(a, runScope{pkgs: map[string]bool{"x": true, "z": true}, filters: []string{"f"}}) {
		t.Error("package count differs")
	}
}

// TestVerdictCoversRun_AnUnreadableCommandNeverRefuses pins the fail-open
// direction: a logged command this classifier cannot read covers nothing.
func TestVerdictCoversRun_AnUnreadableCommandNeverRefuses(t *testing.T) {
	t.Parallel()
	if verdictCoversRun(gateEntry{Cmd: "make check"}, wholeRunScope()) {
		t.Fatal("a wrapper command cannot be shown wide enough, so it covers nothing")
	}
	if verdictCoversRun(gateEntry{Cmd: ""}, wholeRunScope()) {
		t.Fatal("an entry that logged no command covers nothing")
	}
}

// TestVerdictCoversRun_AWholeTreeVerdictCoversAnyAttempt pins the positive
// case through the entry's own command text.
func TestVerdictCoversRun_AWholeTreeVerdictCoversAnyAttempt(t *testing.T) {
	t.Parallel()
	if !verdictCoversRun(gateEntry{Cmd: "go test ./..."}, attemptedScope("go test ./internal/x")) {
		t.Fatal("a whole-tree go test covers a package run")
	}
	if verdictCoversRun(gateEntry{Cmd: "go test ./internal/x"}, wholeRunScope()) {
		t.Fatal("a package run does not cover a whole-tree attempt")
	}
}

// TestAttemptedScope_TheWidestSegmentWins pins the compound reading: two
// segments of different scope merge into their union with filters dropped.
func TestAttemptedScope_TheWidestSegmentWins(t *testing.T) {
	t.Parallel()
	s := attemptedScope("go test ./a && go test ./b")
	if got := pkgList(s); !reflect.DeepEqual(got, []string{"./a", "./b"}) || s.whole || len(s.filters) != 0 {
		t.Fatalf("scope = %+v, want the union of ./a and ./b", s)
	}
}

// TestAttemptedScope_EqualSegmentsStayAsTheyAre pins that two identical
// segments are not merged (which would drop their filters).
func TestAttemptedScope_EqualSegmentsStayAsTheyAre(t *testing.T) {
	t.Parallel()
	s := attemptedScope("go test ./a -run X && go test ./a -run X")
	if len(s.filters) != 1 || s.filters[0] != "-run X" {
		t.Fatalf("scope = %+v, want the shared filter kept", s)
	}
}

// TestAttemptedScope_NoSuiteInvocationComesBackWhole pins the fallback: only a
// whole-tree verdict could refuse a command that names no suite run.
func TestAttemptedScope_NoSuiteInvocationComesBackWhole(t *testing.T) {
	t.Parallel()
	s := attemptedScope("ls -la && echo done")
	if !s.whole || len(s.pkgs) != 0 {
		t.Fatalf("scope = %+v, want whole", s)
	}
}

// TestMergeScopes_IsTheUnionWithNoFilters pins the merge: packages of both,
// whole if either was, and filters dropped.
func TestMergeScopes_IsTheUnionWithNoFilters(t *testing.T) {
	t.Parallel()
	a := runScope{pkgs: map[string]bool{"a": true}, filters: []string{"-run A"}}
	b := runScope{pkgs: map[string]bool{"b": true}}
	m := mergeScopes(a, b)
	if got := pkgList(m); !reflect.DeepEqual(got, []string{"a", "b"}) || len(m.filters) != 0 || m.whole {
		t.Fatalf("merge = %+v, want packages a and b, no filters, not whole", m)
	}
	if !mergeScopes(runScope{whole: true, pkgs: map[string]bool{}}, b).whole {
		t.Fatal("a whole side makes the union whole")
	}
}

// TestWholeRunScope_IsWholeWithNoPackagesOrFilters pins the widest scope.
func TestWholeRunScope_IsWholeWithNoPackagesOrFilters(t *testing.T) {
	t.Parallel()
	s := wholeRunScope()
	if !s.whole || len(s.pkgs) != 0 || len(s.filters) != 0 {
		t.Fatalf("wholeRunScope = %+v", s)
	}
}
