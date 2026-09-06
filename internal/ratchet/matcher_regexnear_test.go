package ratchet

import (
	"regexp"
	"testing"
)

// TestRegexNearRequiresContextCoOccurring_TriggerAloneIsNotAHit is
// KindRegexNear's own shape, kept in its own file rather than growing
// matcher_test.go past module_size's ceiling — see law.go's KindRegexNear
// doc for why this is the COMPLEMENT of marker-within-lines rather than a
// variant of it.
func TestRegexNearRequiresContextCoOccurring_TriggerAloneIsNotAHit(t *testing.T) {
	l := lawWith(Matcher{
		Kind:    KindRegexNear,
		Trigger: regexp.MustCompile(`return\s+(nil|[A-Za-z_]+\{\}|"")\s*,\s*(nil|false)\s*$`),
		Context: regexp.MustCompile(`if err != nil \{`),
		Lines:   3,
		Key:     KeyLineContent,
	})
	cases := map[string]struct {
		src  string
		want int
	}{
		"context directly above":                          {"if err != nil {\nreturn nil, nil\n}\n", 1},
		"context two above":                               {"if err != nil {\nlog(err)\nreturn nil, nil\n}\n", 1},
		"context too far":                                 {"if err != nil {\n\n\n\nreturn nil, nil\n}\n", 0},
		"trigger alone, no context anywhere":              {"return nil, nil\n", 0},
		"honest not-found lookup, no nearby error branch": {"return \"\", false\n", 0},
	}
	for name, c := range cases {
		hits := l.HitsIn("a.go", c.src)
		if len(hits) != c.want {
			t.Errorf("%s: hits = %+v, want %d", name, keys(hits), c.want)
		}
	}
}

// TestRegexNearDirectionBelow_ContextInsideTheBlockAfterTheTrigger proves
// the direction knob works the same way it does for marker-within-lines:
// a context line living BELOW the trigger (inside the block it opens) is
// found only when direction says to look there.
func TestRegexNearDirectionBelow_ContextInsideTheBlockAfterTheTrigger(t *testing.T) {
	l := lawWith(Matcher{
		Kind:      KindRegexNear,
		Trigger:   regexp.MustCompile(`^func `),
		Context:   regexp.MustCompile(`unsafe\.Pointer`),
		Lines:     2,
		Direction: DirectionBelow,
		Key:       KeyLineContent,
	})
	hits := l.HitsIn("a.go", "func f() {\nx := unsafe.Pointer(nil)\n_ = x\n}\n")
	if len(hits) != 1 {
		t.Fatalf("hits = %+v, want 1", keys(hits))
	}
}
