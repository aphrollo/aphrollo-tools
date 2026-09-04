package cli

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/dev"
)

// TestDevResult_ClassifiesBySentinelNotMessageSubstring proves devResult
// tells a usage error from a runtime one by the SENTINEL a dev error wraps,
// not by matching the rendered message text. Before this, devResult
// substring-matched the literal strings "service not allowed" and "service
// required" against err.Error(), so rewording either message in internal/dev
// (a routine copy edit) would silently flip its exit code from 2 to 1 with
// nothing at compile time to catch it.
func TestDevResult_ClassifiesBySentinelNotMessageSubstring(t *testing.T) {
	var stderr bytes.Buffer

	wrapped := fmt.Errorf("%w: renamed wording nobody agreed to keep in sync", dev.ErrServiceNotAllowed)
	if code := devResult(wrapped, &stderr); code != 2 {
		t.Fatalf("wrapped ErrServiceNotAllowed with reworded text: exit = %d, want 2", code)
	}

	wrappedRequired := fmt.Errorf("%w, and no default", dev.ErrServiceRequired)
	if code := devResult(wrappedRequired, &stderr); code != 2 {
		t.Fatalf("wrapped ErrServiceRequired with extra text: exit = %d, want 2", code)
	}

	lookalike := errors.New("service not allowed: coincidentally the same substring, no sentinel")
	if code := devResult(lookalike, &stderr); code != 1 {
		t.Fatalf("plain error carrying the old substring but no sentinel: exit = %d, want 1 (runtime), not 2", code)
	}
}
