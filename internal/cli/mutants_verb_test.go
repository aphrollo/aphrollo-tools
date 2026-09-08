package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// inDir runs the rest of the test standing somewhere else, because `run` is
// defined by the checkout it is typed in.
func inDir(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
}

// Typed outside a repo, `run` must say why it measured nothing — the boundary
// that proves the verb reaches the current checkout at all, rather than
// exiting clean having done nothing.
func TestGateMutantsRun_OutsideARepositorySaysWhyItMeasuredNothing(t *testing.T) {
	gateConfigDir(t)
	inDir(t, t.TempDir())

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "run"}, strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatalf("`gate mutants run` outside a repo exited 0 having measured nothing\nstdout: %s\nstderr: %s", out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "git repository") {
		t.Fatalf("stderr %q never says why nothing was measured", errb.String())
	}
}

// A verb list nobody can print is a verb list nobody finds. `-h` used to be
// parsed as a verb name and rejected as unknown.
func TestGateMutants_HelpPrintsTheVerbsInsteadOfRejectingHAsAVerb(t *testing.T) {
	gateConfigDir(t)

	for _, arg := range []string{"-h", "--help"} {
		var out, errb bytes.Buffer
		if code := Run([]string{"gate", "mutants", arg}, strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("`gate mutants %s` exit = %d, want 0\nstderr: %s", arg, code, errb.String())
		}
		if strings.Contains(errb.String(), "unknown verb") {
			t.Fatalf("`gate mutants %s` reported the help flag as an unknown verb: %s", arg, errb.String())
		}
		if !strings.Contains(errb.String(), "run ") || !strings.Contains(errb.String(), "prove ") {
			t.Fatalf("`gate mutants %s` never lists the verbs: %s", arg, errb.String())
		}
	}
}

// The refusal a session reads at the moment it is about to act must name the
// command it should type. It named the producer script instead, which is the
// unlocked path — so the message that was meant to prevent a stampede was the
// thing causing it.
func TestCargoShimMutantsRefusal_NamesTheGateVerbNotTheUnlockedProducer(t *testing.T) {
	if !strings.Contains(mutantsRefusal, "aphrollo gate mutants run") {
		t.Fatalf("the refusal never names the command to type: %q", mutantsRefusal)
	}
	if strings.Contains(mutantsRefusal, "run tools/mutation_gate.sh") {
		t.Fatalf("the refusal still instructs the unlocked producer directly: %q", mutantsRefusal)
	}
}

// ratchet: test_removed TestGateMutantsRun_WithNoJobFlagAnswersForTheCurrentCheckout: renamed now that there is no --job to be absent; the claim it makes about a run typed outside a repository is unchanged
// ratchet: test_removed TestGateMutantsRun_AnExplicitlyEmptyJobIsRefusedRatherThanDefaulted: `--job` is deleted with the detached job, so there is no empty one to refuse
