package lang

import (
	"regexp"
	"testing"
)

func TestEmbeddedDigest_IsStableSixteenHexDigits(t *testing.T) {
	a, b := EmbeddedDigest(), EmbeddedDigest()
	if a != b {
		t.Errorf("two calls differ: %q and %q", a, b)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(a) {
		t.Errorf("digest = %q, want 16 hex digits", a)
	}
}
