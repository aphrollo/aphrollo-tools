package smell

import "testing"

// suppressAt reports the action the suppression policies take on content at a
// given phase.
func suppressAt(content string, p phase) Action {
	return evaluate(content, suppressionPolicies, p, defaultLang).Action
}

func TestSuppress_Detected(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	// A directive that lives in a STRING, or ordinary code, must not trip.
	allowed := []string{
		`msg := "remember to add //nolint here"`, // a directive quoted in a string
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

// TestSuppress_ADescribedLintDisableIsAdmitted: the lint-disable directive's
// own ` -- <description>` form is how a disable is justified in code, and the
// linter's own rules can require it. A disable carrying a non-empty
// description is admitted in every comment shape.
func TestSuppress_ADescribedLintDisableIsAdmitted(t *testing.T) {
	t.Parallel()
	described := []string{
		"// eslint-disable-next-line no-console -- the log line is the product here",
		"/* eslint-disable local/no-color-literal -- user-selectable avatar colors: data, not styling */",
		"foo() // eslint-disable-line no-undef --- provided by the page at runtime",
		"/* eslint-disable -- generated file, regenerated on every build */",
	}
	for _, src := range described {
		if got := suppressAt(src, commitPhase); got != Allow {
			t.Errorf("a described disable must be admitted: got %v for %q", got, src)
		}
	}
}

// TestSuppress_AnUndescribedLintDisableStillBlocks: only a real description
// admits a disable. A separator with nothing after it, one not set off by
// whitespace, a second bare disable on the same line, another linter's
// suppression riding along, or a separator on the NEXT line all still block.
func TestSuppress_AnUndescribedLintDisableStillBlocks(t *testing.T) {
	t.Parallel()
	bare := []string{
		"/* eslint-disable local/no-color-literal -- */",
		"// eslint-disable-next-line no-console --",
		"// eslint-disable-next-line no-console --because",
		"// eslint-disable-next-line no-console-- because",
		"/* eslint-disable a -- why */ /* eslint-disable b */",
		"x := f() // eslint-disable-line a -- why //nolint",
		"// eslint-disable-next-line no-plusplus\nwhile (i -- > 0) {}",
	}
	for _, src := range bare {
		if got := suppressAt(src, commitPhase); got != Block {
			t.Errorf("an undescribed disable must block: got %v for %q", got, src)
		}
	}
}
