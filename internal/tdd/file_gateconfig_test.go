package tdd

import "testing"

// aphrollo.toml is not prose. It carries the keys the gate reads to decide
// whether a lane owes a mutation receipt at all, and the suite pins those keys
// through the functions that read them
// (TestAphrolloToml_RequiresTheProofAndMeasuresItLocally). Classified as
// Ignore, a commit that changed nothing else took the docs-only fast path --
// no suite, by design -- so the change sailed past the very test that pins it
// and main went red on a commit whose gate was green (issue #212).
func TestClassifyFile_TreatsTheGateConfigAsSourceSoItCannotTakeTheDocsOnlyPath(t *testing.T) {
	for _, p := range []string{"aphrollo.toml", "sub/aphrollo.toml", `sub\aphrollo.toml`} {
		if got := ClassifyFile(p); got != Source {
			t.Errorf("ClassifyFile(%q) = %v, want %v — the gate's own config is judged by the suite, so it must never be waived as prose", p, got, Source)
		}
	}
}

// TestDocsOnly_IsFalseWhenTheGateConfigIsStaged is the consequence the
// classification exists for, asserted where it actually bites: the fast path
// must not swallow a commit that changes how the gate itself behaves.
func TestDocsOnly_IsFalseWhenTheGateConfigIsStaged(t *testing.T) {
	tests, srcs := splitKinds([]string{"README.md", "aphrollo.toml"})
	if len(tests) != 0 {
		t.Errorf("tests = %q, want none", tests)
	}
	if len(srcs) != 1 || srcs[0] != "aphrollo.toml" {
		t.Errorf("srcs = %q, want [aphrollo.toml] — otherwise docsOnly reports true and the suite never runs", srcs)
	}
}
