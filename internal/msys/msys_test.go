package msys

import "testing"

// Anchor is safe to call from every test binary and every run: once, silently,
// and on a box with no shell it does nothing.
func TestAnchor_CanBeCalledRepeatedly(t *testing.T) {
	Anchor()
	Anchor()
}

// Letting the anchor go twice, or before it was ever started, is harmless.
func TestRelease_IsSafeAnyNumberOfTimes(t *testing.T) {
	Release()
	Anchor()
	Release()
	Release()
}
