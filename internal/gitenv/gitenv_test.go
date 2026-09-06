package gitenv

import (
	"strings"
	"testing"
)

// TestClean_DropsGitPrefixedVarsKeepsOthers is the closed-form contract:
// every GIT_* variable is gone from the result, and a same-process,
// non-GIT_ variable survives — proving the filter is a GIT_ prefix
// allowlist-drop, not "clear everything" or "no-op".
func TestClean_DropsGitPrefixedVarsKeepsOthers(t *testing.T) {
	t.Setenv("GIT_DIR", "/outer/.git")
	t.Setenv("GIT_INDEX_FILE", "/outer/.git/index")
	t.Setenv("GIT_WORK_TREE", "/outer")
	t.Setenv("GITENV_TEST_MARKER", "keep-me")

	out := Clean()

	for _, kv := range out {
		if strings.HasPrefix(kv, "GIT_") {
			t.Fatalf("Clean() leaked %q", kv)
		}
	}
	found := false
	for _, kv := range out {
		if kv == "GITENV_TEST_MARKER=keep-me" {
			found = true
		}
	}
	if !found {
		t.Fatal("Clean() dropped a non-GIT_ variable it must have preserved")
	}
}
