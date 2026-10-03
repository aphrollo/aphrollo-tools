package release

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
)

func TestNextVersion_BumpsTheNumberTheLevelNamesAndResetsWhatIsBelowIt(t *testing.T) {
	cases := []struct {
		newest compat.Version
		level  compat.Bump
		want   compat.Version
	}{
		{compat.Version{Major: 1, Minor: 6, Patch: 6}, compat.BumpPatch, compat.Version{Major: 1, Minor: 6, Patch: 7}},
		{compat.Version{Major: 1, Minor: 6, Patch: 6}, compat.BumpMinor, compat.Version{Major: 1, Minor: 7, Patch: 0}},
		{compat.Version{Major: 1, Minor: 6, Patch: 6}, compat.BumpMajor, compat.Version{Major: 2, Minor: 0, Patch: 0}},
		{compat.Version{Major: 0, Minor: 9, Patch: 9}, compat.BumpMinor, compat.Version{Major: 0, Minor: 10, Patch: 0}},
	}
	for _, c := range cases {
		got, err := NextVersion(c.newest, c.level)
		if err != nil || got != c.want {
			t.Errorf("NextVersion(%v, %s) = %v, %v; want %v", c.newest, c.level, got, err, c.want)
		}
	}
}

func TestNextVersion_RefusesALevelThatIsNotABump(t *testing.T) {
	for _, level := range []compat.Bump{compat.BumpNone, "", "huge"} {
		if got, err := NextVersion(compat.Version{Major: 1}, level); err == nil {
			t.Errorf("NextVersion(1.0.0, %q) = %v, want an error", level, got)
		}
	}
}

func TestHighestLevel_IsTheBiggestBumpAmongTheFragments(t *testing.T) {
	frag := func(l compat.Bump) Fragment { return Fragment{Level: l} }
	cases := []struct {
		in   []Fragment
		want compat.Bump
	}{
		{nil, compat.BumpNone},
		{[]Fragment{frag(compat.BumpPatch)}, compat.BumpPatch},
		{[]Fragment{frag(compat.BumpPatch), frag(compat.BumpMinor), frag(compat.BumpPatch)}, compat.BumpMinor},
		{[]Fragment{frag(compat.BumpMinor), frag(compat.BumpMajor), frag(compat.BumpPatch)}, compat.BumpMajor},
		{[]Fragment{frag(compat.BumpMajor), frag(compat.BumpMinor)}, compat.BumpMajor},
	}
	for _, c := range cases {
		if got := HighestLevel(c.in); got != c.want {
			t.Errorf("HighestLevel(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNewestRelease_IsTheHighestSemverAndIgnoresWhatIsNotAReleaseTag(t *testing.T) {
	tag, v, ok := NewestRelease([]string{"v1.9.0", "v1.10.0", "salvage/x", "v2.0.0-rc1", "1.99.0", "v1.2.3", "v3"})
	if !ok || tag != "v1.10.0" || v != (compat.Version{Major: 1, Minor: 10}) {
		t.Fatalf("NewestRelease = %q, %v, %v; want v1.10.0", tag, v, ok)
	}
	if _, _, ok := NewestRelease([]string{"salvage/x", "v2"}); ok {
		t.Fatal("NewestRelease found a release among tags that are none")
	}
}

func frags(levels map[string]compat.Bump) []Fragment {
	var out []Fragment
	for name, level := range levels {
		out = append(out, Fragment{Name: name, Level: level, Body: "Words."})
	}
	return out
}

func TestPlanRelease_TagsTheNextVersionFromTheHighestLevelOfTheNewFragments(t *testing.T) {
	plan, ok, err := PlanRelease(
		[]string{"v1.6.5", "v1.6.6"},
		[]string{"old-one"},
		frags(map[string]compat.Bump{"old-one": compat.BumpMajor, "a-fix": compat.BumpPatch, "b-feature": compat.BumpMinor}),
	)
	if err != nil || !ok {
		t.Fatalf("PlanRelease = %+v, %v, %v; want a release", plan, ok, err)
	}
	if plan.Tag != "v1.7.0" || plan.Version != (compat.Version{Major: 1, Minor: 7}) || plan.Level != compat.BumpMinor {
		t.Errorf("plan = %+v, want v1.7.0 at level minor: the already released major must not count", plan)
	}
	var names []string
	for _, f := range plan.Fragments {
		names = append(names, f.Name)
	}
	if want := []string{"a-fix", "b-feature"}; !reflect.DeepEqual(names, want) {
		t.Errorf("plan fragments = %v, want %v, the new ones sorted by name", names, want)
	}
}

// The workflow runs again for the same push, or for the next one: with every
// fragment already in the newest tag there is nothing to tag, and so no second
// tag for the same fragments, however often it runs.
func TestPlanRelease_IsIdempotent_NothingNewMeansNoTag(t *testing.T) {
	head := frags(map[string]compat.Bump{"a": compat.BumpMinor, "b": compat.BumpPatch})
	plan, ok, err := PlanRelease([]string{"v1.7.0", "v1.6.6"}, []string{"a", "b"}, head)
	if err != nil || ok {
		t.Fatalf("PlanRelease = %+v, %v, %v; want nothing to release", plan, ok, err)
	}
}

func TestPlanRelease_AFragmentThatWasInTheNewestTagIsNotReleasedAgainWhenItsLevelIsEdited(t *testing.T) {
	head := frags(map[string]compat.Bump{"a": compat.BumpMajor})
	if _, ok, _ := PlanRelease([]string{"v1.7.0"}, []string{"a"}, head); ok {
		t.Fatal("a fragment already released was planned again")
	}
}

func TestPlanRelease_AFragmentOfLevelNoneMintsNothing(t *testing.T) {
	head := frags(map[string]compat.Bump{"a": compat.BumpNone})
	if _, ok, _ := PlanRelease([]string{"v1.7.0"}, nil, head); ok {
		t.Fatal("a fragment of level none was planned as a release")
	}
}

func TestPlanRelease_NoFragmentsAtAllIsNothingToRelease(t *testing.T) {
	if plan, ok, err := PlanRelease([]string{"v1.7.0"}, nil, nil); ok || err != nil {
		t.Fatalf("PlanRelease = %+v, %v, %v; want nothing to release and no error", plan, ok, err)
	}
}

func TestPlanRelease_NeedsATagToBumpFrom(t *testing.T) {
	_, _, err := PlanRelease([]string{"salvage/x"}, nil, frags(map[string]compat.Bump{"a": compat.BumpMinor}))
	if err == nil || !strings.Contains(err.Error(), "no release tag") {
		t.Fatalf("PlanRelease error = %v, want it to name the missing release tag", err)
	}
}

func TestLaterReleaseTags_AreTheOnesAfterTheFrozenChangelogOldestFirst(t *testing.T) {
	got := LaterReleaseTags([]string{"v1.7.1", "v1.6.6", "v1.7.0", "v1.6.5", "salvage/x", "v2.0.0"}, compat.Version{Major: 1, Minor: 6, Patch: 6})
	var tags []string
	for _, r := range got {
		tags = append(tags, r.Tag)
	}
	if want := []string{"v1.7.0", "v1.7.1", "v2.0.0"}; !reflect.DeepEqual(tags, want) {
		t.Fatalf("LaterReleaseTags = %v, want %v", tags, want)
	}
}

func TestAttribute_AFragmentBelongsToTheFirstTagThatContainsIt(t *testing.T) {
	trees := []TagTree{
		{Tag: "v1.8.0", Version: compat.Version{Major: 1, Minor: 8}, Fragments: []string{"a", "b", "c"}},
		{Tag: "v1.7.0", Version: compat.Version{Major: 1, Minor: 7}, Fragments: []string{"a"}},
		{Tag: "v1.7.1", Version: compat.Version{Major: 1, Minor: 7, Patch: 1}, Fragments: []string{"a", "b"}},
	}
	released, unreleased := Attribute(trees, []string{"a", "b", "c", "d", "e"})
	want := []Release{
		{Tag: "v1.7.0", Version: compat.Version{Major: 1, Minor: 7}, Fragments: []string{"a"}},
		{Tag: "v1.7.1", Version: compat.Version{Major: 1, Minor: 7, Patch: 1}, Fragments: []string{"b"}},
		{Tag: "v1.8.0", Version: compat.Version{Major: 1, Minor: 8}, Fragments: []string{"c"}},
	}
	if !reflect.DeepEqual(released, want) {
		t.Errorf("released = %+v, want %+v", released, want)
	}
	if wantUn := []string{"d", "e"}; !reflect.DeepEqual(unreleased, wantUn) {
		t.Errorf("unreleased = %v, want %v", unreleased, wantUn)
	}
}

func TestAttribute_ATagThatReleasedNothingNewIsNotARelease(t *testing.T) {
	trees := []TagTree{
		{Tag: "v1.7.0", Version: compat.Version{Major: 1, Minor: 7}, Fragments: []string{"a"}},
		{Tag: "v1.7.1", Version: compat.Version{Major: 1, Minor: 7, Patch: 1}, Fragments: []string{"a"}},
	}
	released, unreleased := Attribute(trees, []string{"a"})
	if len(released) != 1 || released[0].Tag != "v1.7.0" || len(unreleased) != 0 {
		t.Fatalf("Attribute = %+v, %v; want only v1.7.0 and nothing unreleased", released, unreleased)
	}
}

func TestAttribute_AFragmentRemovedFromTheTreeIsStillThatTagsHistory(t *testing.T) {
	trees := []TagTree{{Tag: "v1.7.0", Version: compat.Version{Major: 1, Minor: 7}, Fragments: []string{"gone", "kept"}}}
	released, _ := Attribute(trees, []string{"kept"})
	if len(released) != 1 || !reflect.DeepEqual(released[0].Fragments, []string{"gone", "kept"}) {
		t.Fatalf("Attribute = %+v, want the tag's own tree to decide what it released", released)
	}
}
