package suite

import "testing"

// Issue #991: git C-quotes a name holding a quote, a backslash, a control
// character or a non-ASCII byte, in both halves of the header.
func TestDiffHeaderPath_ReadsPlainAndQuotedHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, line, want string
		ok               bool
	}{
		{"plain", "diff --git a/x/y.go b/x/y.go", "x/y.go", true},
		{"space unquoted", "diff --git a/my dir/y.go b/my dir/y.go", "my dir/y.go", true},
		{"quoted space and tab", `diff --git "a/my dir/t\tab.go" "b/my dir/t\tab.go"`, "my dir/t\tab.go", true},
		{"octal non-ascii", `diff --git "a/caf\303\251.go" "b/caf\303\251.go"`, "café.go", true},
		{"escaped quote and backslash", `diff --git "a/q\"\\.go" "b/q\"\\.go"`, `q"\.go`, true},
		{"rename keeps the post-image", `diff --git "a/old\303\251.go" "b/new\303\251.go"`, "newé.go", true},
		{"plain a, quoted b", `diff --git a/x.go "b/y\303\251.go"`, "yé.go", true},
		{"quoted name containing ` b/`", `diff --git "a/d b/e.go" "b/d b/e.go"`, "d b/e.go", true},
		{"bad escape", `diff --git "a/x\q" "b/x\q"`, "", false},
		{"short octal", `diff --git "a/x\30" "b/x\30"`, "", false},
		{"dangling backslash", `diff --git "a/x" "b/x\"`, "", false},
		{"quoted but not a b/ name", `diff --git "a/x" "c/x"`, "", false},
		{"not a diff line", "index 1..2", "", false},
		{"no b/ marker", "diff --git a/x", "", false},
	} {
		got, ok := diffHeaderPath(tc.line)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: diffHeaderPath(%q) = %q, %v; want %q, %v", tc.name, tc.line, got, ok, tc.want, tc.ok)
		}
	}
}
