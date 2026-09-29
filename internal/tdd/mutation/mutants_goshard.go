package mutation

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Sharding the Go measurement across CI runners (issue #1015). gremlins has
// no shard flag, so a shard is a slice of the lane's changed files: every
// shard's gremlins run excludes the files another shard owns, and the shards'
// reports are merged and judged once (mutants_shardmerge.go).

// ParseShardSpec reads a `--shard` value, "<index>/<count>", with index
// counted from 0 and always below count, the way cargo-mutants numbers its
// own shards.
func ParseShardSpec(spec string) (shard, shards int, err error) {
	i, n, found := strings.Cut(spec, "/")
	if !found {
		return 0, 0, fmt.Errorf("--shard %q is not <index>/<count>", spec)
	}
	shard, err = strconv.Atoi(strings.TrimSpace(i))
	if err != nil {
		return 0, 0, fmt.Errorf("--shard %q: the index is not a whole number", spec)
	}
	shards, err = strconv.Atoi(strings.TrimSpace(n))
	if err != nil {
		return 0, 0, fmt.Errorf("--shard %q: the count is not a whole number", spec)
	}
	if shards < 1 {
		return 0, 0, fmt.Errorf("--shard %q: the count must be at least 1", spec)
	}
	if shard < 0 || shard >= shards {
		return 0, 0, fmt.Errorf("--shard %q: the index must be from 0 to %d", spec, shards-1)
	}
	return shard, shards, nil
}

// assignFilesToShards divides files among shards by cost, heaviest first,
// each to the shard with the least so far (the lowest index on a tie), so the
// shards finish together rather than in file order. A file with no weight
// counts as 1. Every shard computes the same division from the same inputs,
// which is what makes their union the whole lane and no file measured twice.
// The answer holds one sorted list per shard, empty for a shard with nothing.
func assignFilesToShards(files []string, weight map[string]int, shards int) [][]string {
	out := make([][]string, max(shards, 1))
	order := append([]string(nil), files...)
	cost := func(f string) int { return max(weight[f], 1) }
	sort.Slice(order, func(a, b int) bool {
		if ca, cb := cost(order[a]), cost(order[b]); ca != cb {
			return ca > cb
		}
		return order[a] < order[b]
	})
	load := make([]int, len(out))
	for _, f := range order {
		lightest := 0
		for s := range load {
			if load[s] < load[lightest] {
				lightest = s
			}
		}
		load[lightest] += cost(f)
		out[lightest] = append(out[lightest], f)
	}
	for _, list := range out {
		sort.Strings(list)
	}
	return out
}

// filesOutsideShard is every file of the lane that shard does not own: what
// this shard's gremlins run must not walk.
func filesOutsideShard(files []string, weight map[string]int, shard, shards int) []string {
	var outside []string
	for s, list := range assignFilesToShards(files, weight, shards) {
		if s != shard {
			outside = append(outside, list...)
		}
	}
	sort.Strings(outside)
	return outside
}

// addedLineWeights is how many lines the diff adds per file, the cost a
// shard is balanced on.
func addedLineWeights(added map[string]map[int]bool) map[string]int {
	weight := make(map[string]int, len(added))
	for file, lines := range added {
		weight[file] = len(lines)
	}
	return weight
}
