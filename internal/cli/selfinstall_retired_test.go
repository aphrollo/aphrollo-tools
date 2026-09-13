package cli

import (
	"bytes"
	"strings"
	"testing"
)

// `gate self-install` built ./cmd/aphrollo from an ARBITRARY checkout and
// made it the box's binary. That was its only capability `aphrollo update`
// does not have, and it existed for one reason: the fixtures stage judged a
// lane's laws with the installed binary, so a matcher correction could not
// commit until the box already carried it, and the way out was to move a
// machine-wide binary to unmerged code (#659, #673). The stage now builds
// the lane for the laws the lane changed, so the bootstrap is gone and the
// capability has no remaining use. `aphrollo update` — which builds from a
// detached worktree at origin/main and nowhere else — is the only way the
// box binary moves.
//
// This is the wall, not a note: a verb left dispatching is a verb someone
// reaches for at 2am.
func TestGate_SelfInstallIsNoLongerAVerb(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "self-install", "--repo", t.TempDir(), "--no-init"},
		strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatalf("self-install still ran\nstdout:%s\nstderr:%s", out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "unknown subcommand") {
		t.Fatalf("the refusal must say the verb does not exist; stderr:\n%s", errb.String())
	}
}

// The usage text is the other half of the wall: a retired verb still listed
// reads as a real command to whoever is looking for a way past a refusal.
func TestGateUsage_DoesNotOfferSelfInstall(t *testing.T) {
	var out, errb bytes.Buffer
	Run([]string{"gate", "--help"}, strings.NewReader(""), &out, &errb)
	if strings.Contains(out.String()+errb.String(), "self-install") {
		t.Fatalf("gate --help still offers self-install:\n%s%s", out.String(), errb.String())
	}
}
