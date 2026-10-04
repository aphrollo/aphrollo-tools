//go:build !windows

package msys

// startAnchor has nothing to start: only Windows has the shared /tmp mount.
func startAnchor() (release func(), exited <-chan struct{}) {
	done := make(chan struct{})
	close(done)
	return func() {}, done
}
