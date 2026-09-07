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
	t.Parallel()
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
	t.Parallel()
	tests, srcs := splitKinds([]string{"README.md", "aphrollo.toml"})
	if len(tests) != 0 {
		t.Errorf("tests = %q, want none", tests)
	}
	if len(srcs) != 1 || srcs[0] != "aphrollo.toml" {
		t.Errorf("srcs = %q, want [aphrollo.toml] — otherwise docsOnly reports true and the suite never runs", srcs)
	}
}

// A manifest or lockfile carries the identical weight aphrollo.toml does — a
// dependency bump or a workspace member edit changes what builds — and #212
// never extended past the gate's own config file. Left Ignore, a commit
// staging only one of these took the docs-only fast path: no build, no
// suite, and a broken bump landed green (issue #278).
func TestClassifyFile_TreatsManifestsAndLockfilesAsSourceSoADependencyBumpCannotTakeTheDocsOnlyPath(t *testing.T) {
	t.Parallel()
	paths := []string{
		"Cargo.toml", "sub/Cargo.toml", `sub\Cargo.toml`,
		"Cargo.lock", "go.mod", "go.sum", "package.json", "pyproject.toml",
		".cargo/config.toml", `.cargo\config.toml`,
	}
	for _, p := range paths {
		if got := ClassifyFile(p); got != Source {
			t.Errorf("ClassifyFile(%q) = %v, want %v — a manifest/lockfile edit changes what builds, so it must never be waived as prose", p, got, Source)
		}
	}
}

// A config.toml that is not under a .cargo/ directory is an ordinary config
// file with no special build-behaviour meaning to this gate, and must stay
// Ignore — the manifest match is by path shape, not by the generic basename
// alone.
func TestClassifyFile_DoesNotTreatUnrelatedConfigTomlAsSource(t *testing.T) {
	t.Parallel()
	if got := ClassifyFile("app/config.toml"); got != Ignore {
		t.Errorf("ClassifyFile(%q) = %v, want %v — only .cargo/config.toml is special", "app/config.toml", got, Ignore)
	}
}

// TestDocsOnly_IsFalseWhenAManifestIsStaged is the consequence asserted where
// it actually bites, mirroring TestDocsOnly_IsFalseWhenTheGateConfigIsStaged
// for a dependency manifest instead of the gate's own config.
func TestDocsOnly_IsFalseWhenAManifestIsStaged(t *testing.T) {
	t.Parallel()
	tests, srcs := splitKinds([]string{"README.md", "Cargo.toml"})
	if len(tests) != 0 {
		t.Errorf("tests = %q, want none", tests)
	}
	if len(srcs) != 1 || srcs[0] != "Cargo.toml" {
		t.Errorf("srcs = %q, want [Cargo.toml] — otherwise docsOnly reports true and the suite never runs", srcs)
	}
}
