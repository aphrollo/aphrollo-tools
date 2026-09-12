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
		"marker cut off by a blank line, which ends the block": {
			"    // det-ok: integrated, never sampled\n\n    omega_rad_s: f32,\n",
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
