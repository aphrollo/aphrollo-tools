package escape

import (
	"errors"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// labelHost answers each label create with the next error in fail (nil for
// success) and counts the creates.
type labelHost struct {
	host.Issues
	fail  []error
	calls int
}

func (h *labelHost) EnsureLabel(name, colour, description string) error {
	h.calls++
	if h.calls <= len(h.fail) {
		return h.fail[h.calls-1]
	}
	return nil
}

// A label is created once per process, but a create that failed left no label
// behind: it must be asked for again, or the next issue names a label that does
// not exist.
func TestEnsureLabel_ACreatedLabelIsRememberedAndAFailedOneIsNot(t *testing.T) {
	resetLabelCache()
	t.Cleanup(resetLabelCache)
	h := &labelHost{fail: []error{errors.New("no auth")}}

	ensureLabel(h, "/repo", "theme", labelMeta{})
	ensureLabel(h, "/repo", "theme", labelMeta{})
	ensureLabel(h, "/repo", "theme", labelMeta{})

	if h.calls != 2 {
		t.Errorf("label creates = %d, want 2: the failed one asked again, the one that succeeded remembered", h.calls)
	}
}
