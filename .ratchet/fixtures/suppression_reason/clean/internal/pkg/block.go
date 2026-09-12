package pkg

// blockExample keeps its reason further up the SAME comment run as the
// directive it belongs to, which is the feature: a marker anywhere in the
// declaration's own contiguous comment block still vouches for it.
// reason: the generated table trips govet's struct-tag check and is regenerated wholesale.
// The suppression below is the one this block describes.
//
//nolint:govet
func blockExample() int { return 3 }
