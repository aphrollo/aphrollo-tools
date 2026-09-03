package tdd

import "testing"

// FuzzDocsOnlyClassifier feeds arbitrary bytes as a repo-relative path to
// ClassifyFile, the per-path half of the docs-only fast path (splitKinds
// calls it over every staged path, and docsOnly is "no path classified
// Source or Test"). A path in a real diff can carry anything a filesystem
// tolerates — embedded NULs on some platforms' raw bytes, backslashes from a
// Windows-authored commit read on Linux, non-UTF8 — and ClassifyFile must
// resolve every one of them to a Kind (Ignore is the safe default it already
// documents) rather than panic on a path.Dir/path.Ext edge case.
func FuzzDocsOnlyClassifier(f *testing.F) {
	seeds := []string{
		"",
		"README.md",
		"crates/shared/src/lib.rs",
		"crates\\shared\\src\\lib.rs",
		"internal/tdd/file_test.go",
		"node_modules/pkg/index.test.js",
		"__tests__/x.js",
		"a.spec.ts",
		"assets/foo.ron",
		".git/HEAD",
		"/////",
		"...",
		"a/b/../../../etc/passwd",
		string([]byte{0x00, 0x01, 0xff}),
		"a" + string(rune(0)) + "b.go",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, path string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ClassifyFile panicked on path=%q: %v", path, r)
			}
		}()
		k := ClassifyFile(path)
		if k != Ignore && k != Source && k != Test {
			t.Fatalf("ClassifyFile(%q) returned an out-of-range Kind %v", path, k)
		}
	})
}
