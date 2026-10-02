package ghworkflow

import (
	"strings"
	"testing"
)

func testScope() *Scope {
	return &Scope{Status: "success", Root: map[string]any{
		"github": map[string]any{"event_name": "pull_request", "sha": "abc", "event": map[string]any{
			"pull_request": map[string]any{"draft": false, "base": map[string]any{"sha": "base1"}},
		}},
		"env":   map[string]any{"Mode": "fast"},
		"steps": map[string]any{"filter": map[string]any{"outputs": map[string]any{"code": "true", "n": "3"}, "outcome": "failure"}},
		"needs": map[string]any{
			"a": map[string]any{"result": "success", "outputs": map[string]any{"shards": "[1,2,3]"}},
			"b": map[string]any{"result": "failure"},
		},
		"matrix": map[string]any{"os": "linux"},
	}}
}

func TestEval_ReadsContextsAndLiterals(t *testing.T) {
	for _, c := range []struct {
		expr string
		want any
	}{
		{"github.event_name", "pull_request"},
		{"github.event.pull_request.base.sha", "base1"},
		{"GITHUB.EVENT_NAME", "pull_request"},
		{"env['Mode']", "fast"},
		{"steps.filter.outputs.code", "true"},
		{"needs.a.outputs.shards", "[1,2,3]"},
		{"'it''s'", "it's"},
		{"42", 42.0},
		{"true", true},
		{"null", nil},
		{"matrix.os", "linux"},
	} {
		got, _, err := testScope().Eval(c.expr)
		if err != nil || got != c.want {
			t.Errorf("Eval(%s) = %#v, %v; want %#v", c.expr, got, err, c.want)
		}
	}
}

func TestEval_OperatorsAndFunctions(t *testing.T) {
	for _, c := range []struct {
		expr string
		want any
	}{
		{"github.event_name == 'PULL_REQUEST'", true},
		{"github.event_name != 'push'", true},
		{"github.event_name == 'push'", false},
		{"1 < 2 && 2 <= 2 && 3 > 2 && 3 >= 3", true},
		{"2 < 1", false},
		{"2 > 3", false},
		{"'a' < 'B'", true},
		{"'b' >= 'A'", true},
		{"!false", true},
		{"!(1 == 1)", false},
		{"false || 'x'", "x"},
		{"'a' && 'b'", "b"},
		{"'' || 0 || 'z'", "z"},
		{"contains('Hello World', 'lo w')", true},
		{"contains('abc', 'z')", false},
		{"startsWith('Hello', 'he')", true},
		{"startsWith('Hello', 'lo')", false},
		{"endsWith('Hello', 'LO')", true},
		{"endsWith('Hello', 'he')", false},
		{"format('{0}-{1}', 'a', 7)", "a-7"},
		{"join(fromJSON('[1,2,3]'), '+')", "1+2+3"},
		{"join(fromJSON('[1,2]'))", "1,2"},
		{"join('x')", "x"},
		{"fromJSON('[4,5]')[1]", 5.0},
		{"fromJSON('{\"a\": {\"b\": 9}}').a.b", 9.0},
		{"contains(needs.*.result, 'failure')", true},
		{"contains(needs.*.result, 'cancelled')", false},
		{"toJSON(fromJSON('[1]'))", "[\n  1\n]"},
		{"1 == '1'", true},
		{"null == ''", true},
		{"hashFiles('**/go.sum')", ""},
	} {
		got, _, err := testScope().Eval(c.expr)
		if err != nil || got != c.want {
			t.Errorf("Eval(%s) = %#v, %v; want %#v", c.expr, got, err, c.want)
		}
	}
}

func TestEval_StatusFunctionsAnswerFromTheJobStatus(t *testing.T) {
	for _, c := range []struct {
		status, expr string
		want         bool
	}{
		{"success", "success()", true},
		{"failure", "success()", false},
		{"failure", "failure()", true},
		{"success", "failure()", false},
		{"failure", "always()", true},
		{"success", "cancelled()", false},
	} {
		s := testScope()
		s.Status = c.status
		got, used, err := s.Eval(c.expr)
		if err != nil || got != c.want || !used {
			t.Errorf("status %s: Eval(%s) = %v used=%v err=%v, want %v used", c.status, c.expr, got, used, err, c.want)
		}
	}
	if _, used, _ := testScope().Eval("1 == 1"); used {
		t.Error("an expression with no status function reported using one")
	}
}

func TestEval_RefusesWhatItCannotEvaluate(t *testing.T) {
	for _, expr := range []string{
		"nosuchfunc(1)", "'unterminated", "1 +", "(1", "contains('a')", "fromJSON('{bad')",
		"a.", "a[1", "@", "contains('a',", "format()",
	} {
		if _, _, err := testScope().Eval(expr); err == nil {
			t.Errorf("Eval(%s) succeeded, want an error", expr)
		}
	}
}

func TestEval_AnUnknownContextReadsEmptyAndIsNoted(t *testing.T) {
	s := testScope()
	got, _, err := s.Eval("steps.nope.outputs.x")
	if err != nil || got != nil {
		t.Fatalf("got %#v, %v; want nil, nil", got, err)
	}
	if len(s.Notes) != 1 || !strings.Contains(s.Notes[0], "unresolved") {
		t.Errorf("notes = %v, want one unresolved entry", s.Notes)
	}
	s.Eval("steps.nope.outputs.x")
	if len(s.Notes) != 1 {
		t.Errorf("the same unresolved lookup was noted twice: %v", s.Notes)
	}
	s2 := testScope()
	s2.Eval("inputs.x")
	if len(s2.Notes) != 1 {
		t.Errorf("an unknown root context was not noted: %v", s2.Notes)
	}
}

func TestInterpolate_ReplacesEveryExpression(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"no expressions", "no expressions"},
		{"a ${{ env.Mode }} b", "a fast b"},
		{"${{ github.sha }}-${{ matrix.os }}", "abc-linux"},
		{"${{ 'a}}b' }}", "a}}b"},
		{"${{ 1 == 1 }}", "true"},
		{"${{ 2 }}", "2"},
		{"${{ 1.5 }}", "1.5"},
		{"[${{ null }}]", "[]"},
		{"${{ fromJSON('[1,2]') }}", "[1,2]"},
		{`if [ "${{ steps.filter.outcome }}" = "failure" ]`, `if [ "failure" = "failure" ]`},
	} {
		got, err := testScope().Interpolate(c.in)
		if err != nil || got != c.want {
			t.Errorf("Interpolate(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := testScope().Interpolate("${{ unterminated"); err == nil {
		t.Error("an unterminated ${{ was accepted")
	}
	if _, err := testScope().Interpolate("x ${{ nosuchfunc() }}"); err == nil || !strings.Contains(err.Error(), "nosuchfunc") {
		t.Errorf("an unsupported function must name itself, got %v", err)
	}
}

func TestCond_AppliesTheImplicitSuccessAndStatusFunctions(t *testing.T) {
	for _, c := range []struct {
		status, expr string
		want         bool
	}{
		{"success", "", true},
		{"failure", "", false},
		{"success", "github.event_name == 'pull_request'", true},
		{"success", "${{ github.event_name == 'push' }}", false},
		{"failure", "github.event_name == 'pull_request'", false},
		{"failure", "always()", true},
		{"failure", "${{ always() && github.event_name == 'pull_request' }}", true},
		{"failure", "failure()", true},
		{"success", "failure()", false},
		{"success", "steps.filter.outputs.code == 'true'", true},
		{"success", "prefix ${{ github.sha }}", true},
		{"failure", "prefix ${{ github.sha }}", false},
		{"success", "  ${{ false }}  ", false},
	} {
		s := testScope()
		s.Status = c.status
		got, err := s.Cond(c.expr)
		if err != nil || got != c.want {
			t.Errorf("status %s: Cond(%q) = %v, %v; want %v", c.status, c.expr, got, err, c.want)
		}
	}
	if _, err := testScope().Cond("nosuchfunc()"); err == nil {
		t.Error("an unevaluable condition must be an error so the caller can say so")
	}
}

func TestEval_IdentifiersAndNumbersLexAtTheirCharacterBoundaries(t *testing.T) {
	s := &Scope{Status: "success", Root: map[string]any{
		"a": "lower-a", "z": "lower-z", "A": "upper-A", "Z": "upper-Z", "_u": "under", "x09": "digits", "k-9": "hyphen",
	}}
	for expr, want := range map[string]any{
		"a": "lower-a", "z": "lower-z", "A": "upper-A", "Z": "upper-Z", "_u": "under", "x09": "digits", "k-9": "hyphen",
		"0": 0.0, "9": 9.0, "09": 9.0, "1.5": 1.5, "90": 90.0, "0 == 0": true, "9 == 9": true,
	} {
		got, _, err := s.Eval(expr)
		if err != nil || got != want {
			t.Errorf("Eval(%s) = %#v, %v; want %#v", expr, got, err, want)
		}
	}
	for _, bad := range []string{"{", "`x`", "~", "#"} {
		if _, _, err := s.Eval(bad); err == nil {
			t.Errorf("Eval(%s) succeeded, want a lex error", bad)
		}
	}
	// A character just outside each class is not part of an identifier or number.
	for _, bad := range []string{"@a", "[a", "`a", "{a", "a@", "/", ":"} {
		if _, _, err := s.Eval(bad); err == nil {
			t.Errorf("Eval(%s) succeeded, want an error", bad)
		}
	}
}

func TestEval_ComparisonsAreStrictAtTheEdges(t *testing.T) {
	for expr, want := range map[string]bool{
		"1 < 1": false, "1 > 1": false, "1 <= 1": true, "1 >= 1": true,
		"'a' < 'a'": false, "'a' > 'a'": false, "'a' <= 'a'": true, "'a' >= 'a'": true,
		"1 < 2": true, "2 > 1": true, "2 < 1": false, "1 > 2": false,
		"'a' < 'b'": true, "'b' > 'a'": true,
	} {
		got, _, err := testScope().Eval(expr)
		if err != nil || got != want {
			t.Errorf("Eval(%s) = %#v, %v; want %v", expr, got, err, want)
		}
	}
	// A value that is not a number never orders.
	for _, expr := range []string{"'x' < 1", "'x' >= 1", "1 > 'x'", "1 <= 'x'"} {
		if got, _, err := testScope().Eval(expr); err != nil || got != false {
			t.Errorf("Eval(%s) = %#v, %v; want false", expr, got, err)
		}
	}
}

func TestEval_ListIndexesStayInRange(t *testing.T) {
	for expr, want := range map[string]any{
		"fromJSON('[4,5]')[0]": 4.0, "fromJSON('[4,5]')[1]": 5.0, "fromJSON('[4,5]')[2]": nil, "fromJSON('[]')[0]": nil,
	} {
		got, _, err := testScope().Eval(expr)
		if err != nil || got != want {
			t.Errorf("Eval(%s) = %#v, %v; want %#v", expr, got, err, want)
		}
	}
}

func TestInterpolate_UnterminatedAndEmptyExpressionsAreDistinctErrors(t *testing.T) {
	for _, src := range []string{"${{ x", "${{ x }", "a ${{", "${{ 'q }}"} {
		if _, err := testScope().Interpolate(src); err == nil || !strings.Contains(err.Error(), "unterminated") {
			t.Errorf("Interpolate(%q) err = %v, want unterminated", src, err)
		}
	}
	_, err := testScope().Interpolate("${{}}")
	if err == nil || strings.Contains(err.Error(), "unterminated") {
		t.Errorf("an empty expression is an evaluation error, not an unterminated one: %v", err)
	}
	if _, err := testScope().Cond("a ${{"); err == nil || !strings.Contains(err.Error(), "unterminated") {
		t.Errorf("Cond with an open ${{ must say unterminated: %v", err)
	}
	if _, err := testScope().Cond("${{}}"); err == nil || strings.Contains(err.Error(), "unterminated") || strings.HasPrefix(err.Error(), "${{") {
		t.Errorf("Cond with only an empty expression evaluates it directly, not through Interpolate: %v", err)
	}
}
