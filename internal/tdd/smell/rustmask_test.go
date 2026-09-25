package smell

import (
	"strings"
	"testing"
)

// A Rust file is masked as Rust: a lifetime is code, so the lines below it
// stay visible to every detector, while a char literal is still blanked.
// Masked as a generic C-family file, the `'` of `'_` opened a quote that ran
// to the next apostrophe and the detectors never saw what sat between.
func TestNewView_RustFileKeepsTheCodeBelowALifetime(t *testing.T) {
	src := "fn a(e: &Elements<'_>) {}\nfn b() { x.unwrap(); }\nconst C: char = 'z';\n"
	code := newView(src, langOf("crates/solver/src/pair.rs")).Code
	if !strings.Contains(code, "x.unwrap();") {
		t.Errorf("code view lost the line below the lifetime:\n%s", code)
	}
	if strings.Contains(code, "'z'") {
		t.Errorf("code view kept the char literal's contents:\n%s", code)
	}
}
