package cli

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// The `post-merge` shim runs `aphrollo gate postmerge`, so the dispatch has
// to know the verb: an unknown-subcommand exit 2 out of a git hook is a
// scary line printed after every merge on the box, for a hook that must be
// inert wherever the repo did not opt in (issue #582).
func TestRun_GatePostMerge_IsSilentOutsideAnOptedInRepo(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "postmerge"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — a post-merge hook can never block; stderr: %s", code, errb.String())
	}
	if out.String() != "" || errb.String() != "" {
		t.Fatalf("stdout = %q, stderr = %q, want both silent outside a repo", out.String(), errb.String())
	}
}

// The verb table mirrors the dispatch, and TestSurface_TablesMatchDispatch
// holds the two together — a verb missing from the table is one the surface
// tests never exercise.
func TestSurface_GateTableCarriesPostMerge(t *testing.T) {
	if !slices.Contains(GateVerbs(), "postmerge") {
		t.Fatalf("gate verb table = %v, want it to carry postmerge", GateVerbs())
	}
}
