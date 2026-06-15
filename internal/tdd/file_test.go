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
