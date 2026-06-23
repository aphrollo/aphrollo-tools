package workspace

import "testing"

func TestPathWithin(t *testing.T) {
	cases := []struct {
		child, parent string
		want          bool
	}{
		{"/a/b/c", "/a/b", true},
		{"/a/b", "/a/b", true},
		{"/a/bc", "/a/b", false}, // sibling prefix, not a descendant
		{"/x", "/a/b", false},
	}
	for _, c := range cases {
		if got := pathWithin(c.child, c.parent); got != c.want {
			t.Errorf("pathWithin(%q,%q) = %v, want %v", c.child, c.parent, got, c.want)
		}
	}
}
