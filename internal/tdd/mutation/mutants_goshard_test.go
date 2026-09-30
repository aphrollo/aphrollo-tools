package mutation

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseShardSpec_ReadsIndexOverCount(t *testing.T) {
	t.Parallel()
	cases := []struct {
		spec          string
		shard, shards int
		wantErr       string
	}{
		{"0/1", 0, 1, ""},
		{"0/4", 0, 4, ""},
		{"3/4", 3, 4, ""},
		{" 2 / 5 ", 2, 5, ""},
		{"4/4", 0, 0, "the index must be from 0 to 3"},
		{"5/4", 0, 0, "the index must be from 0 to 3"},
		{"-1/4", 0, 0, "the index must be from 0 to 3"},
		{"0/0", 0, 0, "the count must be at least 1"},
		{"0/-2", 0, 0, "the count must be at least 1"},
		{"", 0, 0, "is not <index>/<count>"},
		{"3", 0, 0, "is not <index>/<count>"},
		{"a/4", 0, 0, "the index is not a whole number"},
		{"1/b", 0, 0, "the count is not a whole number"},
	}
	for _, tc := range cases {
		shard, shards, err := ParseShardSpec(tc.spec)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ParseShardSpec(%q) error = %v, want one containing %q", tc.spec, err, tc.wantErr)
			}
			continue
		}
		if err != nil || shard != tc.shard || shards != tc.shards {
			t.Errorf("ParseShardSpec(%q) = (%d, %d, %v), want (%d, %d)", tc.spec, shard, shards, err, tc.shard, tc.shards)
		}
	}
}

func TestAssignFilesToShards_BalancesByWeightAndCoversEveryFileOnce(t *testing.T) {
	t.Parallel()
	weight := map[string]int{"a.go": 10, "b.go": 6, "c.go": 5, "d.go": 4, "e.go": 1}
	cases := []struct {
		name   string
		files  []string
		shards int
		want   [][]string
	}{
		{"no files", nil, 3, [][]string{nil, nil, nil}},
		{"one file", []string{"a.go"}, 3, [][]string{{"a.go"}, nil, nil}},
		{"one shard takes all", []string{"b.go", "a.go"}, 1, [][]string{{"a.go", "b.go"}}},
		{"fewer files than shards", []string{"a.go", "b.go"}, 3, [][]string{{"a.go"}, {"b.go"}, nil}},
		// 10 -> s0; 6 -> s1; 5 -> s1 (6 < 10); 4 -> s0 (10 vs 11: s0 lighter); 1 -> s0 or s1 (14 vs 11: s1)
		{"heaviest first, each to the lightest shard", []string{"e.go", "d.go", "c.go", "b.go", "a.go"}, 2,
			[][]string{{"a.go", "d.go"}, {"b.go", "c.go", "e.go"}}},
		{"a weight of zero counts as one", []string{"x.go", "y.go", "z.go"}, 2, [][]string{{"x.go", "z.go"}, {"y.go"}}},
		{"a count below one is one shard", []string{"a.go", "b.go"}, 0, [][]string{{"a.go", "b.go"}}},
	}
	for _, tc := range cases {
		got := assignFilesToShards(tc.files, weight, tc.shards)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Equal weights tie to the lowest shard, then by name, so every shard
// computes the same division.
func TestAssignFilesToShards_TiesGoToTheLowestShardInNameOrder(t *testing.T) {
	t.Parallel()
	got := assignFilesToShards([]string{"d.go", "b.go", "c.go", "a.go"}, nil, 3)

	if want := [][]string{{"a.go", "d.go"}, {"b.go"}, {"c.go"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAssignFilesToShards_DoesNotReorderItsInput(t *testing.T) {
	t.Parallel()
	files := []string{"z.go", "a.go"}

	_ = assignFilesToShards(files, nil, 2)

	if !reflect.DeepEqual(files, []string{"z.go", "a.go"}) {
		t.Errorf("input became %v", files)
	}
}

// Across every shard, the files a shard owns and the files it excludes are
// the whole lane, and no file is owned twice.
func TestFilesOutsideShard_IsTheComplementOfTheShardsOwnFiles(t *testing.T) {
	t.Parallel()
	files := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	weight := map[string]int{"a.go": 9, "b.go": 3, "c.go": 3, "d.go": 2, "e.go": 1}
	owned := assignFilesToShards(files, weight, 3)

	for s := range 3 {
		got := filesOutsideShard(files, weight, s, 3)
		var want []string
		for o, list := range owned {
			if o != s {
				want = append(want, list...)
			}
		}
		if len(got)+len(owned[s]) != len(files) {
			t.Errorf("shard %d: excludes %v and owns %v, want them to add up to the %d files", s, got, owned[s], len(files))
		}
		if len(got) != len(want) {
			t.Errorf("shard %d: excludes %v, want %d files", s, got, len(want))
		}
		for _, f := range owned[s] {
			for _, x := range got {
				if f == x {
					t.Errorf("shard %d both owns and excludes %s", s, f)
				}
			}
		}
	}
	if got := filesOutsideShard(nil, nil, 0, 2); len(got) != 0 {
		t.Errorf("no files: excludes %v, want none", got)
	}
	if got := filesOutsideShard([]string{"a.go"}, nil, 0, 1); len(got) != 0 {
		t.Errorf("one shard: excludes %v, want none", got)
	}
	if got := filesOutsideShard([]string{"a.go"}, nil, 1, 2); !reflect.DeepEqual(got, []string{"a.go"}) {
		t.Errorf("the shard that owns nothing: excludes %v, want the one file", got)
	}
}

func TestAddedLineWeights_CountsLinesPerFile(t *testing.T) {
	t.Parallel()
	got := addedLineWeights(map[string]map[int]bool{
		"a.go": {1: true, 5: true, 9: true},
		"b.go": {2: true},
		"c.go": {},
	})

	if want := map[string]int{"a.go": 3, "b.go": 1, "c.go": 0}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := addedLineWeights(nil); len(got) != 0 {
		t.Errorf("no diff: got %v, want no weights", got)
	}
}
