package pkg

// neighbour.go is issue #652's cascade in this law's own vocabulary: the
// reason states why the FIRST directive is suppressed, and before the fix the
// comment run let it vouch for the second one too — which then read clean
// with nothing explaining it.
// reason: the generated table trips govet's struct-tag check and is regenerated wholesale.
//
//nolint:govet
//nolint:errcheck
func twoDirectives() int { return 2 }
