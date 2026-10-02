package suite

import (
	"slices"
	"testing"

	"pgregory.net/rapid"
)

// costs builds items named p0, p1, ... with the given costs, in order.
func costs(cs ...float64) []splitItem {
	items := make([]splitItem, len(cs))
	for i, c := range cs {
		items[i] = splitItem{Pkg: "p" + string(rune('0'+i)), Cost: c}
	}
	return items
}

// TestPackGroups_ARunThatFitsStaysOneGroup pins the no-change case: packages
// whose costs together fit the capacity come back as the one run they were,
// in their own order.
func TestPackGroups_ARunThatFitsStaysOneGroup(t *testing.T) {
	got := packGroups(costs(100, 200, 300), 600)

	if len(got) != 1 || !slices.Equal(names(got[0]), []string{"p0", "p1", "p2"}) {
		t.Fatalf("groups = %v, want one group p0 p1 p2", got)
	}
}

// TestPackGroups_SplitsPastTheCapacityAndBalancesTheRuns pins the split: 1000
// seconds of work under a 600-second capacity is two runs, and the work is
// shared out (550 and 450) rather than packed 600 and 400, so that two runs
// side by side finish together.
func TestPackGroups_SplitsPastTheCapacityAndBalancesTheRuns(t *testing.T) {
	got := packGroups(costs(300, 250, 200, 150, 100), 600)

	if len(got) != 2 {
		t.Fatalf("groups = %v, want 2", got)
	}
	if want := []string{"p0", "p3", "p4"}; !slices.Equal(names(got[0]), want) {
		t.Errorf("first group = %v, want %v (550s: the longest run starts first)", names(got[0]), want)
	}
	if want := []string{"p1", "p2"}; !slices.Equal(names(got[1]), want) {
		t.Errorf("second group = %v, want %v (450s)", names(got[1]), want)
	}
}

// TestPackGroups_BalancesWhenTheSumJustPassesTheCapacity pins why balancing
// exists: 650 seconds is two runs, and first-fit would leave one 600 and one
// 50. Shared out they are 325 each.
func TestPackGroups_BalancesWhenTheSumJustPassesTheCapacity(t *testing.T) {
	got := packGroups(costs(300, 300, 50), 600)

	if len(got) != 2 {
		t.Fatalf("groups = %v, want 2", got)
	}
	var loads []float64
	for _, g := range got {
		sum := 0.0
		for _, it := range g {
			sum += it.Cost
		}
		loads = append(loads, sum)
	}
	slices.Sort(loads)
	if !slices.Equal(loads, []float64{300, 350}) {
		t.Fatalf("loads = %v, want [300 350]", loads)
	}
}

// TestPackGroups_AnItemOverTheCapacityRunsAlone pins that one package too
// big for any run is not dropped and does not drag others into a run that
// cannot fit them: it gets a run to itself.
func TestPackGroups_AnItemOverTheCapacityRunsAlone(t *testing.T) {
	got := packGroups(costs(900, 100, 100), 600)

	if len(got) != 2 {
		t.Fatalf("groups = %v, want 2", got)
	}
	if want := []string{"p0"}; !slices.Equal(names(got[0]), want) {
		t.Errorf("first group = %v, want the oversize package alone", names(got[0]))
	}
	if want := []string{"p1", "p2"}; !slices.Equal(names(got[1]), want) {
		t.Errorf("second group = %v, want %v", names(got[1]), want)
	}
}

// TestPackGroups_AnOversizeItemDoesNotLetOtherRunsPastTheCapacity pins a case
// the property below found: with one item far over the capacity, the balanced
// fill was accepted on the strength of that item's own cost and put 114s of
// small packages in a run with 113s to give. Every run of several items must
// fit, whatever else is in the list.
func TestPackGroups_AnOversizeItemDoesNotLetOtherRunsPastTheCapacity(t *testing.T) {
	got := packGroups(costs(2, 2, 2, 3, 4, 4, 5, 6, 7, 7, 8, 15, 27, 32, 102, 900), 113)

	placed := 0
	for _, g := range got {
		sum := 0.0
		for _, it := range g {
			sum += it.Cost
		}
		placed += len(g)
		if len(g) > 1 && sum > 113 {
			t.Errorf("run %v costs %v, over the capacity 113", names(g), sum)
		}
	}
	if placed != 16 {
		t.Errorf("%d items placed, want all 16 once each", placed)
	}
}

// TestPackGroups_ExactlyTheCapacityFitsOneRun pins the boundary: costs that
// add up to the capacity are one run, and a hair over is two.
func TestPackGroups_ExactlyTheCapacityFitsOneRun(t *testing.T) {
	if got := packGroups(costs(300, 300), 600); len(got) != 1 {
		t.Errorf("300 + 300 in a 600 capacity = %d runs, want 1", len(got))
	}
	if got := packGroups(costs(300, 301), 600); len(got) != 2 {
		t.Errorf("300 + 301 in a 600 capacity = %d runs, want 2", len(got))
	}
}

// TestPackGroups_ABalancedRunExactlyAtTheCapacityIsAccepted pins that the
// balanced fill may fill a run to the capacity and no further: here it makes
// {p0 p2 p4} (600) and {p1 p3} (500) where first-fit would make {p0 p1} (600)
// and {p2 p3 p4} (500), and a run at exactly the capacity is not over it.
func TestPackGroups_ABalancedRunExactlyAtTheCapacityIsAccepted(t *testing.T) {
	got := packGroups(costs(300, 300, 200, 200, 100), 600)

	if len(got) != 2 || !slices.Equal(names(got[0]), []string{"p0", "p2", "p4"}) || !slices.Equal(names(got[1]), []string{"p1", "p3"}) {
		t.Fatalf("groups = %v, want [p0 p2 p4] then [p1 p3]", got)
	}
}

// TestPackGroups_NoItemsIsNoGroup pins the empty list.
func TestPackGroups_NoItemsIsNoGroup(t *testing.T) {
	if got := packGroups(nil, 600); len(got) != 0 {
		t.Fatalf("groups = %v, want none", got)
	}
}

// TestPackGroups_EveryItemLandsInExactlyOneGroupWithinCapacity is the
// property under every case above: whatever the costs, each item is in one
// group once, a group of several items fits the capacity, a group keeps its
// items in input order, and the same input always packs the same way.
func TestPackGroups_EveryItemLandsInExactlyOneGroupWithinCapacity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 30).Draw(t, "n")
		items := make([]splitItem, n)
		for i := range items {
			items[i] = splitItem{Pkg: "p" + string(rune('a'+i)), Cost: float64(rapid.IntRange(1, 900).Draw(t, "cost"))}
		}
		capacity := float64(rapid.IntRange(100, 1200).Draw(t, "capacity"))

		got := packGroups(items, capacity)

		seen := map[string]int{}
		for _, g := range got {
			sum := 0.0
			last := -1
			for _, it := range g {
				seen[it.Pkg]++
				sum += it.Cost
				at := slices.IndexFunc(items, func(x splitItem) bool { return x.Pkg == it.Pkg })
				if at < last {
					t.Fatalf("group %v is out of input order", names(g))
				}
				last = at
			}
			if len(g) > 1 && sum > capacity {
				t.Fatalf("group %v costs %v, over the capacity %v", names(g), sum, capacity)
			}
		}
		for _, it := range items {
			if seen[it.Pkg] != 1 {
				t.Fatalf("%s lands in %d groups, want exactly 1 (groups %v)", it.Pkg, seen[it.Pkg], got)
			}
		}
		if again := packGroups(items, capacity); !slices.EqualFunc(again, got, func(a, b []splitItem) bool { return slices.Equal(a, b) }) {
			t.Fatalf("packing is not deterministic: %v then %v", got, again)
		}
	})
}
