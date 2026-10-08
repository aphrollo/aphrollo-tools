package rapidpkg

import (
	"testing"

	"pgregory.net/rapid"
)

func TestRoundTrip(t *testing.T) { rapid.Check(t, func(*rapid.T) {}) }
