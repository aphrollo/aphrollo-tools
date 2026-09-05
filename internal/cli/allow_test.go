package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestAllowPrimary_WaivesForTheSessionAndRevokeRestores walks the whole
// allow/revoke lifecycle for the primary wall: the exact waiver line, the
// predicate seeing it, the bare listing naming who and since when, and
// revoke clearing all three.
func TestAllowPrimary_WaivesForTheSessionAndRevokeRestores(t *testing.T) {
	gateConfigDir(t)
	const session = "s-allow-primary"
	t.Setenv("CLAUDE_SESSION_ID", session)

	var out, errb bytes.Buffer
	if code := runGateAllow([]string{"primary"}, &out, &errb); code != 0 {
		t.Fatalf("runGateAllow(primary) exit = %d\nstderr: %s", code, errb.String())
	}
	const want = "Primary-checkout edits ALLOWED for this session — the merge-only rule is waived. Run `aphrollo gate revoke primary` to restore it.\n"
	if out.String() != want {
		t.Fatalf("stdout = %q, want %q", out.String(), want)
	}
	if !tdd.Waived(tdd.WallPrimary) {
		t.Fatal("the waiver predicate must report the wall waived after allow")
	}

	var listAfterAllow bytes.Buffer
	if code := runGateAllow(nil, &listAfterAllow, io.Discard); code != 0 {
		t.Fatalf("bare runGateAllow exit = %d", code)
	}
	line := strings.TrimSuffix(listAfterAllow.String(), "\n")
	prefix, suffix := "primary since ", " by "+session
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
		t.Fatalf("waiver listing = %q, want %q<RFC3339>%q", line, prefix, suffix)
	}
	since := strings.TrimSuffix(strings.TrimPrefix(line, prefix), suffix)
	if _, err := time.Parse(time.RFC3339, since); err != nil {
		t.Fatalf("waiver listing timestamp %q is not RFC3339: %v", since, err)
	}

	var revokeOut, revokeErr bytes.Buffer
	if code := runGateRevoke([]string{"primary"}, &revokeOut, &revokeErr); code != 0 {
		t.Fatalf("runGateRevoke(primary) exit = %d\nstderr: %s", code, revokeErr.String())
	}
	if tdd.Waived(tdd.WallPrimary) {
		t.Fatal("the waiver predicate must report the wall restored after revoke")
	}

	var listAfterRevoke bytes.Buffer
	if code := runGateAllow(nil, &listAfterRevoke, io.Discard); code != 0 {
		t.Fatalf("bare runGateAllow exit = %d", code)
	}
	if got := listAfterRevoke.String(); got != "no waivers\n" {
		t.Fatalf("waiver listing after revoke = %q, want %q", got, "no waivers\n")
	}
}

// TestAllow_RefusesAnUnknownWall proves allow does not silently waive a wall
// it has never heard of.
func TestAllow_RefusesAnUnknownWall(t *testing.T) {
	gateConfigDir(t)
	t.Setenv("CLAUDE_SESSION_ID", "s-unknown-wall")

	var out, errb bytes.Buffer
	code := runGateAllow([]string{"nonsense"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	const want = "usage: aphrollo gate allow [primary]\n"
	if errb.String() != want {
		t.Fatalf("stderr = %q, want %q", errb.String(), want)
	}
}

// TestPrimaryEditsOn_IsAnAliasOfAllowPrimary proves the pre-rename spelling
// dispatches through the identical code as `gate allow primary`, not a
// second copy that happens to agree: same stdout, same resulting state.
func TestPrimaryEditsOn_IsAnAliasOfAllowPrimary(t *testing.T) {
	gateConfigDir(t)
	t.Setenv("CLAUDE_SESSION_ID", "s-primary-edits-alias")

	var wantOut, wantErr bytes.Buffer
	if code := runGateAllow([]string{"primary"}, &wantOut, &wantErr); code != 0 {
		t.Fatalf("runGateAllow(primary) exit = %d\nstderr: %s", code, wantErr.String())
	}
	var discard bytes.Buffer
	runGateRevoke([]string{"primary"}, &discard, &discard)

	var gotOut, gotErr bytes.Buffer
	if code := runGatePrimaryEdits([]string{"on"}, &gotOut, &gotErr); code != 0 {
		t.Fatalf("primary-edits on exit = %d\nstderr: %s", code, gotErr.String())
	}
	if gotOut.String() != wantOut.String() {
		t.Fatalf("primary-edits on stdout = %q, want %q (same as allow primary)", gotOut.String(), wantOut.String())
	}
	if !tdd.Waived(tdd.WallPrimary) {
		t.Fatal("primary-edits on must waive the same wall as allow primary")
	}
}
