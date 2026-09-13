package ratchet

import "testing"

// TestMarkerHits_trailingCommentDoesNotHideTheTrigger is issue #676: the
// offence half of the trailing-comment hole whose ownership half #672 closed
// in markerInOwnBlock. markerHits tested the trigger against the line AS
// WRITTEN, so for an end-anchored trigger (subprocess_stderr_dropped's
// `\.Output\(\)\s*$`) ANY trailing comment — not only the law's escape —
// removed the call site from the law's scope entirely: no hit, no finding, no
// baseline row, and an escape comment on such a line was decorative, reading
// as a deliberate exception where the law in fact never looked. A comment
// suppresses a hit only through the ESCAPE path, which carries a reason and
// is recorded; a bare note never does.
func TestMarkerHits_trailingCommentDoesNotHideTheTrigger(t *testing.T) {
	l := stderrLaw()
	cases := map[string]struct {
		src  string
		want []string
	}{
		"an ordinary trailing comment leaves the site judged": {
			"\tout, err := cmd.Output() // the checks list\n",
			[]string{"a.go:1"},
		},
		"a bare site is judged, as it always was": {
			"\tout, err := cmd.Output()\n",
			[]string{"a.go:1"},
		},
		"the escape still suppresses, with its reason": {
			"\tout, err := cmd.Output() // stderr-ok: exit code is the whole signal\n",
			nil,
		},
		"a nearby marker still excuses a commented site": {
			"\tcmd.Stderr = &stderr\n" +
				"\tout, err := cmd.Output() // folded into the error below\n",
			nil,
		},
		"a commented site inside a longer expression is still not a trigger": {
			"\tif out := cmd.Output(); out != nil { // still mid-expression\n",
			nil,
		},
	}
	for name, c := range cases {
		got := lineKeys(l.HitsIn("a.go", c.src))
		if !sameStrings(got, c.want) {
			t.Errorf("%s: hits = %v, want %v", name, got, c.want)
		}
	}
}
