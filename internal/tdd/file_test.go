package tdd

import "testing"

func TestClassifyFile(t *testing.T) {
	cases := []struct {
		path string
		want Kind
	}{
		// Go
		{"internal/scan/scan_test.go", Test},
		{"internal/scan/scan.go", Source},
		// Python
		{"pkg/test_widget.py", Test},
		{"pkg/widget_test.py", Test},
		{"pkg/conftest.py", Test},
		{"pkg/widget.py", Source},
		// JS/TS — the forms the original classifier missed
		{"src/widget.test.ts", Test},
		{"src/widget.spec.js", Test},
		{"src/helpers_test.ts", Test}, // _test infix on TS
		{"src/spec.js", Test},         // bare spec file
		{"src/conftest.ts", Test},     // JS conftest
		{"src/__tests__/widget.ts", Test},
		{"src/widget.ts", Source},
		// Zig — tests are `test "..." {}` blocks INLINE in ordinary src/*.zig
		// files, so a .zig file is normally BOTH source and test. At file
		// level it stays Source unless it is an EXPLICIT test file; inline-test
		// coverage is reached block-scoped in the smells gate, not by flipping
		// the whole file to Test.
		{"src/foo.zig", Source},                // inline test blocks → still Source
		{"src/main.zig", Source},               // entry point with inline tests
		{"src/foo_test.zig", Test},             // explicit _test.zig suffix
		{"tests/integration_test.zig", Test},   // any .zig under tests/
		{"tests/helper.zig", Test},             // tests/ rule is dir-based, not suffix
		{"build.zig", Source},                  // build script is real source
		{"build.zig.zon", Ignore},              // manifest (.zon) is not code
		{"vendor/dep/widget.zig", Ignore},      // vendored .zig never gated
		{"vendor/dep/widget_test.zig", Ignore}, // even an explicit test in vendor
		// Vendored / generated trees are never the project's to gate
		{"node_modules/lib/foo.test.ts", Ignore},
		{"vendor/x/y_test.go", Ignore},
		{"internal/scan/testdata/sample_test.go", Ignore},
		{"dist/bundle.js", Ignore},
		// Non-code is ignored entirely
		{"README.md", Ignore},
		{"config.yaml", Ignore},
		{"go.mod", Ignore},
		// Windows separators normalise
		{`src\widget_test.go`, Test},
	}
	for _, c := range cases {
		if got := ClassifyFile(c.path); got != c.want {
			t.Errorf("ClassifyFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
