package suite

import (
	"cmp"
	"slices"
	"sort"
)

// splitItem is one package of a run and what it is expected to cost, in
// seconds, already weighed with the margin the plan keeps over its record.
type splitItem struct {
	Pkg  string
	Cost float64
}

// names is the packages of one run, in the run's order.
func names(run []splitItem) []string {
	out := make([]string, len(run))
	for i, it := range run {
		out[i] = it.Pkg
	}
	return out
}

// packGroups splits items into the fewest runs whose costs each fit capacity,
// then shares the work between those runs so that runs started together finish
// together: first-fit-decreasing finds how many runs are needed, and a
// longest-first fill across that many balances them. A balanced fill that
// would put a run of several items over the capacity is dropped for the
// first-fit one, so the capacity is never broken to even the load out.
//
// An item over the capacity on its own is not dropped and not split: it is a
// run to itself, and what that run is allowed is not this function's to say.
//
// Items keep their input order inside a run. Runs come back heaviest first,
// ties by the earliest item, so the same input is always the same plan.
func packGroups(items []splitItem, capacity float64) [][]splitItem {
	if len(items) == 0 {
		return nil
	}
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return items[order[a]].Cost > items[order[b]].Cost })

	fit := firstFit(items, order, capacity)
	if len(fit) < 2 {
		return [][]splitItem{slices.Clone(items)}
	}
	groups := fit
	if balanced := longestFirst(items, order, len(fit)); withinCapacity(items, balanced, capacity) {
		groups = balanced
	}
	return materialize(items, groups)
}

// firstFit places each item, heaviest first, in the first run it fits, and
// opens a run when none does. A run is a list of item indexes.
func firstFit(items []splitItem, order []int, capacity float64) [][]int {
	var runs [][]int
	var loads []float64
	for _, i := range order {
		placed := false
		for r := range runs {
			if loads[r]+items[i].Cost <= capacity {
				runs[r] = append(runs[r], i)
				loads[r] += items[i].Cost
				placed = true
				break
			}
		}
		if !placed {
			runs = append(runs, []int{i})
			loads = append(loads, items[i].Cost)
		}
	}
	return runs
}

// longestFirst deals the items, heaviest first, into n runs, each to the
// run that is lightest at that moment (the lowest-numbered on a tie).
func longestFirst(items []splitItem, order []int, n int) [][]int {
	runs := make([][]int, n)
	loads := make([]float64, n)
	for _, i := range order {
		lightest := slices.Index(loads, slices.Min(loads))
		runs[lightest] = append(runs[lightest], i)
		loads[lightest] += items[i].Cost
	}
	return runs
}

// withinCapacity is whether every run of runs that holds more than one item
// costs no more than capacity. A run of one item is as small as a run gets,
// whatever that item costs.
func withinCapacity(items []splitItem, runs [][]int, capacity float64) bool {
	for _, r := range runs {
		if len(r) > 1 && loadOf(items, r) > capacity {
			return false
		}
	}
	return true
}

// loadOf is the summed cost of one run.
func loadOf(items []splitItem, run []int) float64 {
	sum := 0.0
	for _, i := range run {
		sum += items[i].Cost
	}
	return sum
}

// materialize turns index runs into item runs: input order inside a run,
// heaviest run first, ties by the run's earliest item.
func materialize(items []splitItem, runs [][]int) [][]splitItem {
	for _, r := range runs {
		slices.Sort(r)
	}
	slices.SortStableFunc(runs, func(a, b []int) int {
		return cmp.Or(cmp.Compare(loadOf(items, b), loadOf(items, a)), cmp.Compare(a[0], b[0]))
	})
	out := make([][]splitItem, len(runs))
	for r, run := range runs {
		for _, i := range run {
			out[r] = append(out[r], items[i])
		}
	}
	return out
}
