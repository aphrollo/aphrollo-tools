package smell

import "testing"

// coveredBy reports whether every suppression on the one line of post is paid
// by the directives pre's changed lines carried.
func coveredBy(pre, post string) bool {
	l := defaultLang
	lines := introducedLines(pre, post, l)
	return len(editCovered(pre, post, l, lines)) == len(lines) && len(lines) > 0
}

// TestEditCovered_ADirectiveKeptWithAReasonIsCovered: appending a reason to a
// suppression the old line already carried introduces no suppression, for
// every directive kind the detectors know.
func TestEditCovered_ADirectiveKeptWithAReasonIsCovered(t *testing.T) {
	t.Parallel()
	cases := []struct{ pre, post string }{
		{"    except Exception as e:  # noqa: BLE001", "    except Exception as e:  # noqa: BLE001  one heal must not stop the rest"},
		{"y = g()  # noqa", "y = g()  # noqa  needed by the loader"},
		{"x := f() //nolint:errcheck", "x := f() //nolint:errcheck // best effort"},
		{"a = b // eslint-disable-next-line no-console", "a = b // eslint-disable-next-line no-console -- the log is the product"},
		{"y = g()  # pylint: disable=invalid-name", "y = g()  # pylint: disable=invalid-name  legacy name"},
		{"z = h()  # rubocop:disable Metrics/MethodLength", "z = h()  # rubocop:disable Metrics/MethodLength because"},
		{"a = b // @ts-ignore", "a = b // @ts-ignore upstream typing"},
		{"y = g()  # type: ignore[attr-defined]", "y = g()  # type: ignore[attr-defined]  stub gap"},
		{"y = g()  # pyright: ignore[reportGeneralTypeIssues]", "y = g()  # pyright: ignore[reportGeneralTypeIssues]  stub gap"},
		{"foo() /* istanbul ignore next */", "foo() /* istanbul ignore next -- platform guard */"},
		{"foo() /* c8 ignore start */", "foo() /* c8 ignore start: generated */"},
		{"y = g()  # pragma: no cover", "y = g()  # pragma: no cover  defensive"},
	}
	for _, c := range cases {
		if !coveredBy(c.pre, c.post) {
			t.Errorf("a documented existing suppression was judged new:\n  pre:  %q\n  post: %q", c.pre, c.post)
		}
	}
}

// TestEditCovered_ANewSuppressionIsNeverCovered: a directive the removed lines
// never carried, a new code beside an old one, a second copy, or a different
// directive is introduced.
func TestEditCovered_ANewSuppressionIsNeverCovered(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, pre, post string }{
		{"no old directive", "y = g()", "y = g()  # noqa: BLE001  because"},
		{"new code added", "y = g()  # noqa: BLE001", "y = g()  # noqa: BLE001, E501"},
		{"other code", "y = g()  # noqa: BLE001", "y = g()  # noqa: E501"},
		{"other directive", "y = g()  # noqa: BLE001", "y = g()  # type: ignore"},
		{"second copy", "y = g()  # noqa: BLE001", "y = g()  # noqa: BLE001  a\nz = g()  # noqa: BLE001  b"},
		{"eslint rule added", "a = b // eslint-disable-next-line no-console", "a = b // eslint-disable-next-line no-console, no-alert"},
		{"nolint linter added", "x := f() //nolint:errcheck", "x := f() //nolint:errcheck,gosec"},
		{"line variant changed", "a = b // eslint-disable-line", "a = b // eslint-disable-next-line"},
	}
	for _, c := range cases {
		if coveredBy(c.pre, c.post) {
			t.Errorf("%s: a new suppression was judged already carried:\n  pre:  %q\n  post: %q", c.name, c.pre, c.post)
		}
	}
}

// TestEditCovered_ABareDirectiveCoversItsNarrowing: an old bare noqa silenced
// every code, so naming one code on it silences less and is not new.
func TestEditCovered_ABareDirectiveCoversItsNarrowing(t *testing.T) {
	t.Parallel()
	if !coveredBy("y = g()  # noqa", "y = g()  # noqa: BLE001") {
		t.Error("narrowing a bare noqa to one code was judged new")
	}
	if coveredBy("y = g()  # noqa: BLE001", "y = g()  # noqa") {
		t.Error("widening a coded noqa to a bare one was judged already carried")
	}
}

// TestEvaluateCovered_OnlySuppressionPoliciesSkipACoveredLine: a covered line
// is exempt from the suppression policies alone; the test-oracle policies
// still read it.
func TestEvaluateCovered_OnlySuppressionPoliciesSkipACoveredLine(t *testing.T) {
	t.Parallel()
	post := "y = g()  # noqa: BLE001  reason\n"
	lines := map[int]bool{1: true}
	if got := evaluateCovered(post, lines, lines, defaultLang, suppressionPolicies, commitPhase).Action; got != Allow {
		t.Errorf("covered line blocked by a suppression policy: %v", got)
	}
	if got := evaluateCovered(post, lines, nil, defaultLang, suppressionPolicies, commitPhase).Action; got != Block {
		t.Errorf("uncovered line not blocked: %v", got)
	}
	errPolicy := []policy{{name: "p", category: smellCat, reason: "r", hit: func(v view) bool { return true }}}
	if got := evaluateCovered(post, lines, lines, defaultLang, errPolicy, commitPhase).Action; got != Block {
		t.Errorf("a non-directive policy skipped a covered line: %v", got)
	}
}
