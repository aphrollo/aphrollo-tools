package tdd

import "testing"

// suppressAt reports the action the suppression policies take on content at a
// given phase.
func suppressAt(content string, p phase) Action {
	return evaluate(content, suppressionPolicies, p, defaultLang).Action
}

func TestSuppress_Detected(t *testing.T) {
	// Each value carries a suppression directive in a comment. At edit phase it
	// is advisory (Warn); at commit phase it is a hard block.
	directives := []string{
		"x := f() //nolint:errcheck",
		"x := f() // nolint",
		"const a = b // eslint-disable-next-line",
		"y = g()  # noqa: E501",
		"y = g()  # pylint: disable=invalid-name",
		"z = h()  # rubocop:disable Metrics/MethodLength",
		"const a = b // @ts-ignore",
		"const a = b // @ts-nocheck",
		"y = g()  # type: ignore",
		"y = g()  # pyright: ignore",
		"foo() /* istanbul ignore next */",
		"foo() /* c8 ignore start */",
		"y = g()  # pragma: no cover",
	}
	for _, src := range directives {
		if got := suppressAt(src, editPhase); got != Warn {
			t.Errorf("edit phase: got %v for %q, want Warn", got, src)
		}
		if got := suppressAt(src, commitPhase); got != Block {
			t.Errorf("commit phase: got %v for %q, want Block", got, src)
		}
	}
}

func TestSuppress_Allowed(t *testing.T) {
	// A directive that lives in a STRING, or ordinary code, must not trip.
	allowed := []string{
		`msg := "remember to add //nolint here"`, // directive quoted in a string
		`label = "eslint-disable in docs"`,       // ditto
		"x := f()",                               // ordinary code
		"// a plain explanatory comment",         // a comment with no directive
		"const ratio = c8 / 100",                 // c8 as an identifier, not a marker
	}
	for _, src := range allowed {
		if got := suppressAt(src, commitPhase); got != Allow {
			t.Errorf("false suppression block for %q: got %v", src, got)
		}
	}
}
