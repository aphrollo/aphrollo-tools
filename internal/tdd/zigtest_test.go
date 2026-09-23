package tdd

import (
	"reflect"
	"sort"
	"testing"
)

// maskZig masks source the way the .zig Source gate does: strings + comments
// blanked, `#`-is-code (defaultLang). The extractor runs against this view.
func maskZig(src string) string { return newView(src, defaultLang).Code }

// keptLines returns the sorted line numbers zigTestLines keeps for src.
func keptLines(src string) []int {
	got := zigTestLines(maskZig(src))
	out := make([]int, 0, len(got))
	for ln := range got {
		out = append(out, ln)
	}
	sort.Ints(out)
	return out
}

func TestZigTestLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  string
		want []int
	}{
		{
			name: "inline test block keeps its body, not the surrounding source",
			// 1 pub fn add...
			// 2     return a + b;
			// 3 }
			// 4 (blank)
			// 5 test "add sums" {
			// 6     try expectEqual(@as(i32, 3), add(1, 2));
			// 7 }
			src: "pub fn add(a: i32, b: i32) i32 {\n" +
				"    return a + b;\n" +
				"}\n" +
				"\n" +
				"test \"add sums\" {\n" +
				"    try std.testing.expectEqual(@as(i32, 3), add(1, 2));\n" +
				"}\n",
			want: []int{5, 6, 7},
		},
		{
			name: "named (non-string) test form is recognised",
			src: "test add {\n" +
				"    try expect(true);\n" +
				"}\n",
			want: []int{1, 2, 3},
		},
		{
			name: "single-line test block keeps just that line",
			src: "const x = 1;\n" +
				"test \"one\" { try std.testing.expect(x == 1); }\n" +
				"const y = 2;\n",
			want: []int{2},
		},
		{
			name: "nested braces close the correct block",
			// 1 test "nested" {
			// 2     if (cond) {
			// 3         try expect(true);
			// 4     }
			// 5 }
			// 6 pub fn after() void {}
			src: "test \"nested\" {\n" +
				"    if (cond) {\n" +
				"        try std.testing.expect(true);\n" +
				"    }\n" +
				"}\n" +
				"pub fn after() void {}\n",
			want: []int{1, 2, 3, 4, 5},
		},
		{
			name: "two test blocks both captured, gap excluded",
			// 1 test "a" {
			// 2     try expect(a);
			// 3 }
			// 4 const between = 0;
			// 5 test "b" {
			// 6     try expect(b);
			// 7 }
			src: "test \"a\" {\n" +
				"    try expect(a);\n" +
				"}\n" +
				"const between = 0;\n" +
				"test \"b\" {\n" +
				"    try expect(b);\n" +
				"}\n",
			want: []int{1, 2, 3, 5, 6, 7},
		},
		{
			name: "no test block keeps nothing",
			src: "pub fn f() void {\n" +
				"    return;\n" +
				"}\n",
			want: []int{},
		},
		{
			name: "brace inside a string does not open a phantom block",
			// The `{` is inside a string literal (masked away), so `test` here is
			// just a value, not a block — nothing is kept.
			src: "const s = \"test \\\"x\\\" {\";\n" +
				"pub fn f() void {}\n",
			want: []int{},
		},
		{
			name: "identifier containing test is not a test block",
			src: "const mytest = struct {\n" +
				"    x: i32,\n" +
				"};\n",
			want: []int{},
		},
		{
			name: "member access .test is not a test block",
			src:  "foo.test = bar;\n",
			want: []int{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := keptLines(c.src); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("zigTestLines lines = %v, want %v", got, c.want)
			}
		})
	}
}
