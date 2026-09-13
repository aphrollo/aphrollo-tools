package ratchet

import (
	"fmt"
	"regexp"
	"testing"
)

// fieldLaw is the shape issue #652 was observed in: a struct of physics
// fields, each excused by a `// det-ok:` marker in its own doc comment, with
// a four-line window. Kept in its own file rather than growing
// matcher_test.go past module_size's ceiling.
func fieldLaw() Law {
	return lawWith(Matcher{
		Kind:    KindMarkerWithinLines,
		Trigger: regexp.MustCompile(`^\s+\w+: f32,`),
		Marker:  regexp.MustCompile(`// det-ok:`),
		Lines:   4,
		Key:     KeyLineContent,
	})
}

// TestMarkerWithinLines_vouchesForExactlyOneDeclaration is #652's cascade: a
// marker that reaches past the declaration it belongs to silently excuses the
// NEXT one, and each such excuse deletes that field's baseline row on the
// following tightening — a baseline moving DOWN, the one direction the
// ratchet never questions, while nothing is actually marked. The cases are
// the three steps of the reported cascade: marking a field must not cover its
// neighbour, and fixing each step must not open the next hole.
func TestMarkerWithinLines_vouchesForExactlyOneDeclaration(t *testing.T) {
	l := fieldLaw()
	cases := map[string]struct {
		src  string
		want []string
	}{
		"one step: a marked field does not cover the next": {
			"    /// relaxed slip angle\n" +
				"    // det-ok: measured against the rig\n" +
				"    relaxed_slip_angle_rad: f32,\n" +
				"    omega_rad_s: f32,\n",
			[]string{"a.rs:4"},
		},
		"two steps: marking the covered field does not cover the one after it": {
			"    // det-ok: measured against the rig\n" +
				"    relaxed_slip_angle_rad: f32,\n" +
				"    // det-ok: integrated, never sampled\n" +
				"    omega_rad_s: f32,\n" +
				"    spin_rad: f32,\n",
			[]string{"a.rs:5"},
		},
		"three steps: the cascade ends, every unmarked field is still a hit": {
			"    // det-ok: measured against the rig\n" +
				"    relaxed_slip_angle_rad: f32,\n" +
				"    // det-ok: integrated, never sampled\n" +
				"    omega_rad_s: f32,\n" +
				"    // det-ok: read off the wheel\n" +
				"    spin_rad: f32,\n" +
				"    control: f32,\n",
			[]string{"a.rs:7"},
		},
		"a run of unmarked fields under one marker is a hit each": {
			"    // det-ok: measured against the rig\n" +
				"    relaxed_slip_angle_rad: f32,\n" +
				"    omega_rad_s: f32,\n" +
				"    spin_rad: f32,\n" +
				"    control: f32,\n",
			[]string{"a.rs:3", "a.rs:4", "a.rs:5"},
		},
		"a marker on the previous field's own line does not reach the next": {
			"    relaxed_slip_angle_rad: f32, // det-ok: measured against the rig\n" +
				"    omega_rad_s: f32,\n",
			[]string{"a.rs:2"},
		},
	}
	for name, c := range cases {
		got := lineKeys(l.HitsIn("a.rs", c.src))
		if !sameStrings(got, c.want) {
			t.Errorf("%s: hits = %v, want %v", name, got, c.want)
		}
	}
}

// TestMarkerWithinLines_stillExcusedByItsOwnCommentBlock is the feature the
// fix must not regress: a marker anywhere in the declaration's own contiguous
// comment block — or on its own line — still vouches for it, and `lines` is
// the CAP on that block rather than a reach across whatever sits above.
func TestMarkerWithinLines_stillExcusedByItsOwnCommentBlock(t *testing.T) {
	l := fieldLaw()
	cases := map[string]struct {
		src  string
		want []string
	}{
		"marker directly above": {
			"    // det-ok: measured\n    omega_rad_s: f32,\n",
			nil,
		},
		"marker in a multi-line comment block": {
			"    /// the yaw rate, in radians per second\n" +
				"    // det-ok: integrated, never sampled\n" +
				"    /// clamped by the solver downstream\n" +
				"    omega_rad_s: f32,\n",
			nil,
		},
		"marker above an attribute in the same run": {
			"    // det-ok: integrated, never sampled\n" +
				"    #[serde(default)]\n" +
				"    omega_rad_s: f32,\n",
			nil,
		},
		"marker on the declaration's own line": {
			"    omega_rad_s: f32, // det-ok: integrated, never sampled\n",
			nil,
		},
		"marker past the lines cap inside one long comment block": {
			"    // det-ok: integrated, never sampled\n" +
				"    /// one\n    /// two\n    /// three\n    /// four\n" +
				"    omega_rad_s: f32,\n",
			[]string{"a.rs:6"},
		},
		// #658 also stopped the walk at a blank line, and this case pinned
		// that. For a law with a `lines` cap the cap IS the window: a blank
		// line inside it is not evidence the marker belongs to something
		// else, and treating it as evidence is the same false-hit mechanism
		// this file's code-token test exists to stop — subprocess_stderr_dropped
		// reaches 20 lines up, where a blank line is certain. The run edge
		// still bounds a `contiguous` law, which has no cap; see
		// TestMarkerWithinLines_contiguousLawStopsAtItsRunEdge.
		"marker a blank line above, still inside the lines cap": {
			"    // det-ok: integrated, never sampled\n\n    omega_rad_s: f32,\n",
			nil,
		},
	}
	for name, c := range cases {
		got := lineKeys(l.HitsIn("a.rs", c.src))
		if !sameStrings(got, c.want) {
			t.Errorf("%s: hits = %v, want %v", name, got, c.want)
		}
	}
}

// seededLaw is the shape the field report came in as: a `proptest_seeding`
// law whose marker is a CODE token (`rng_seed`), not a comment, with a
// two-line window. Marker and trigger both live inside the same struct
// literal, so there is no comment run around either one.
func seededLaw() Law {
	return lawWith(Matcher{
		Kind:    KindMarkerWithinLines,
		Trigger: regexp.MustCompile(`\.\.\w*Config::default\(\)`),
		Marker:  regexp.MustCompile(`rng_seed`),
		Lines:   2,
		Key:     KeyLineContent,
	})
}

// TestMarkerWithinLines_findsACodeTokenMarkerAboveTheTrigger is the shipped
// regression: #658's upward walk tested the comment run BEFORE the marker, so
// a marker that is a code token on the line directly above the trigger halted
// the walk before that line was ever tested and every law whose marker is code
// reported a false hit on correctly-marked sites. Both sources are verbatim
// from the report (a workspace where all 48 trigger sites carried `rng_seed`
// within the window and the law reported 20 of them as unseeded).
func TestMarkerWithinLines_findsACodeTokenMarkerAboveTheTrigger(t *testing.T) {
	l := seededLaw()
	cases := map[string]struct {
		src  string
		want []string
	}{
		"marker between two other struct fields, directly above the trigger": {
			"    cases: 24 * 8,\n" +
				"    rng_seed: proptest::test_runner::RngSeed::Fixed(1),\n" +
				"    ..Config::default()\n",
			nil,
		},
		"marker inside an inner attribute's struct literal": {
			"        #![proptest_config(ProptestConfig {\n" +
				"            rng_seed: proptest::test_runner::RngSeed::Fixed(1),\n" +
				"            ..ProptestConfig::default()\n",
			nil,
		},
		"marker two code lines above, at the window edge": {
			"    rng_seed: RngSeed::Fixed(1),\n" +
				"    cases: 24 * 8,\n" +
				"    ..Config::default()\n",
			nil,
		},
		"marker past the lines cap is still a hit": {
			"    rng_seed: RngSeed::Fixed(1),\n" +
				"    cases: 24 * 8,\n" +
				"    max_shrink_iters: 4,\n" +
				"    ..Config::default()\n",
			[]string{"a.rs:4"},
		},
		"a genuinely unseeded config is still a hit": {
			"    cases: 24 * 8,\n" +
				"    ..Config::default()\n",
			[]string{"a.rs:2"},
		},
		"the previous trigger still stops the walk": {
			"    rng_seed: RngSeed::Fixed(1),\n" +
				"    ..Config::default()\n" +
				"    ..Config::default()\n",
			[]string{"a.rs:3"},
		},
	}
	for name, c := range cases {
		got := lineKeys(l.HitsIn("a.rs", c.src))
		if !sameStrings(got, c.want) {
			t.Errorf("%s: hits = %v, want %v", name, got, c.want)
		}
	}
}

// TestMarkerWithinLines_contiguousLawStopsAtItsRunEdge is where the comment
// run is still the boundary: a `contiguous` law (this repo's own
// suppression_reason) declares the run AS its window and carries no `lines`
// cap, so without that edge a `reason:` anywhere above in the file would
// vouch for a suppression it has nothing to do with.
func TestMarkerWithinLines_contiguousLawStopsAtItsRunEdge(t *testing.T) {
	l := lawWith(Matcher{
		Kind:       KindMarkerWithinLines,
		Trigger:    regexp.MustCompile(`#\[allow\(`),
		Marker:     regexp.MustCompile(`reason\s*:`),
		Direction:  DirectionBoth,
		Contiguous: true,
		Key:        KeyLineContent,
	})
	// `contiguous = true` under [matcher] is copied to the law-level flag as
	// the TOML is read (law.go), and the walk reads the law-level one; a
	// hand-built Law never goes through that step.
	l.Contiguous = true
	cases := map[string]struct {
		src  string
		want []string
	}{
		"marker in the run directly above": {
			"// reason: the bound is measured against the rig\n" +
				"#[allow(clippy::too_many_lines)]\n" +
				"fn f() {}\n",
			nil,
		},
		"marker a blank line above, outside the run": {
			"// reason: the bound is measured against the rig\n" +
				"\n" +
				"#[allow(clippy::too_many_lines)]\n" +
				"fn f() {}\n",
			[]string{"a.rs:3"},
		},
		"marker on a code line above, outside the run": {
			"let reason: u8 = measure();\n" +
				"#[allow(clippy::too_many_lines)]\n" +
				"fn f() {}\n",
			[]string{"a.rs:2"},
		},
	}
	for name, c := range cases {
		got := lineKeys(l.HitsIn("a.rs", c.src))
		if !sameStrings(got, c.want) {
			t.Errorf("%s: hits = %v, want %v", name, got, c.want)
		}
	}
}

// TestMarkerWithinLines_belowStillReachesThroughCode keeps the downward half
// unchanged: a `direction = "below"` law (a proptest! block's config, a
// TestMain's m.Run) looks INSIDE the block it triggered on, which is code by
// construction, so the comment-block stop applies above only.
func TestMarkerWithinLines_belowStillReachesThroughCode(t *testing.T) {
	l := lawWith(Matcher{
		Kind:      KindMarkerWithinLines,
		Trigger:   regexp.MustCompile(`^func TestMain\(m \*testing\.M\)`),
		Marker:    regexp.MustCompile(`m\.Run\(`),
		Direction: DirectionBelow,
		Lines:     6,
		Key:       KeyLineContent,
	})
	runs := "func TestMain(m *testing.M) {\n\tsetup()\n\tcode := m.Run()\n\tos.Exit(code)\n}\n"
	if hits := l.HitsIn("a_test.go", runs); len(hits) != 0 {
		t.Errorf("m.Run below a line of code still satisfies the law: %v", keys(hits))
	}
	never := "func TestMain(m *testing.M) {\n\tsetup()\n\tos.Exit(0)\n}\n"
	if hits := l.HitsIn("a_test.go", never); len(hits) != 1 {
		t.Errorf("a TestMain that never runs the suite is still a hit: %v", keys(hits))
	}
}

// stderrLaw is subprocess_stderr_dropped's exact shape: an END-ANCHORED
// trigger, a marker that is a code token (`cmd.Stderr = &buf`), a twenty-line
// both-direction window, and a trailing-comment escape.
func stderrLaw() Law {
	l := lawWith(Matcher{
		Kind:      KindMarkerWithinLines,
		Trigger:   regexp.MustCompile(`\.Output\(\)\s*$`),
		Marker:    regexp.MustCompile(`\.Stderr\b`),
		Direction: DirectionBoth,
		Lines:     20,
		Key:       KeyLineContent,
	})
	l.Escape = "// stderr-ok:"
	return l
}

// TestMarkerWithinLines_triggerWithATrailingCommentStillStopsTheWalk is
// issue #667's reach, measured at the unit level. The upward walk stops at
// the PREVIOUS TRIGGER so that a marker vouches for exactly one call, but it
// tested the line AS WRITTEN: an end-anchored trigger (`\.Output\(\)\s*$`)
// cannot match a line that carries a trailing comment, so an escaped
// `cmd.Output() // stderr-ok: ...` was not seen as a trigger at all, the walk
// stepped past it, and the `cmd.Stderr` belonging to THAT call vouched for an
// unrelated call in the next function twelve lines below. Escaping suppresses
// a HIT; it never transfers ownership of the marker to a neighbour. The stop
// is therefore tested against the line with its trailing comment stripped.
func TestMarkerWithinLines_triggerWithATrailingCommentStillStopsTheWalk(t *testing.T) {
	l := stderrLaw()
	cases := map[string]struct {
		src  string
		want []string
	}{
		"an escaped capture does not vouch for the next function's call": {
			"func gitShowReported(ref string) (string, error) {\n" +
				"\tcmd := exec.Command(\"git\", \"show\", ref)\n" +
				"\tvar stderr bytes.Buffer\n" +
				"\tcmd.Stderr = &stderr\n" +
				"\tout, err := cmd.Output() // stderr-ok: folded into the error below\n" +
				"\tif err != nil {\n" +
				"\t\treturn \"\", fmt.Errorf(\"git show: %w: %s\", err, stderr.String())\n" +
				"\t}\n" +
				"\treturn string(out), nil\n" +
				"}\n" +
				"\n" +
				"func ghChecks(branch string) ([]byte, error) {\n" +
				"\tcmd := exec.Command(\"gh\", \"pr\", \"checks\", \"--\", branch)\n" +
				"\tout, err := cmd.Output()\n" +
				"\treturn out, err\n" +
				"}\n",
			[]string{"a.go:14"},
		},
		"a trigger with an ordinary trailing comment also stops the walk": {
			"\tcmd.Stderr = &stderr\n" +
				"\tout, err := cmd.Output() // the checks list\n" +
				"\tother, err := exec.Command(\"gh\").Output()\n",
			[]string{"a.go:3"},
		},
		"the capture still vouches for the call it belongs to": {
			"\tcmd.Stderr = &stderr\n" +
				"\tout, err := cmd.Output()\n",
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

func lineKeys(hits []Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, fmt.Sprintf("%s:%d", h.File, h.Line))
	}
	return out
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
