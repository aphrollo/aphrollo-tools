package install

import (
	"path/filepath"
	"strings"
	"testing"
)

// premerge-js is an opt-in a repo can only use if it can find it: the table
// that `aphrollo config` prints shows it with its default and the value the
// repo declares.
func TestRenderFeatures_PremergeJsShowsItsDefaultAndTheDeclaredValue(t *testing.T) {
	t.Parallel()
	declared := t.TempDir()
	mustWrite(t, filepath.Join(declared, "aphrollo.toml"), "[aphrollo]\npremerge-js = \"full\"\n")
	for name, tc := range map[string]struct{ root, want string }{
		"default":  {t.TempDir(), "related"},
		"declared": {declared, "full"},
	} {
		f := strings.Fields(featureLine(RenderFeatures(tc.root), "premerge-js"))
		if len(f) < 2 || f[1] != tc.want {
			t.Errorf("%s: row %q, want the value %q", name, f, tc.want)
		}
	}
}
