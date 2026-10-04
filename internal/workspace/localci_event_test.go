package workspace

import (
	"errors"
	"io"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A settled local CI verdict is the same ci event a settled GitHub one is:
// once per commit, with its sha and PR, and a ci field saying who judged it.

func withHead(t *testing.T, sha string) {
	t.Helper()
	prev := ghViewPR
	ghViewPR = func(wt, branch string) (*PRInfo, error) {
		return &PRInfo{Number: 5, URL: "u", HeadSHA: sha}, nil
	}
	t.Cleanup(func() { ghViewPR = prev })
}

func TestMergeCI_ALocalGreenEmitsTheSettledCIEventNamingLocal(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	newCIWorld(t, tdd.CILocal, CIStatus{})
	withHead(t, "sha-green-local")
	if _, err := applyMerge(t, ""); err != nil {
		t.Fatal(err)
	}
	cis := onSHA(emitted(t), "sha-green-local")
	if len(cis) != 1 || cis[0].Verdict != "green" || cis[0].Detail["sha"] != "sha-green-local" || cis[0].Detail["pr"] != "5" || cis[0].Detail["ci"] != "local" {
		t.Fatalf("ci events = %+v, want one green local event for sha-green-local on PR 5", cis)
	}
}

func TestMergeCI_ALocalRedEmitsARedEventAndASetupRefusalEmitsNone(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	w := newCIWorld(t, tdd.CILocal, CIStatus{})
	withHead(t, "sha-red-local")
	localCI = func(*Target, string, io.Writer) (tdd.LocalCIVerdict, error) {
		return tdd.LocalCIVerdict{Red: true}, errors.New("local CI is red: x")
	}
	_ = w
	if _, err := applyMerge(t, ""); err == nil {
		t.Fatal("a red must refuse")
	}
	cis := onSHA(emitted(t), "sha-red-local")
	if len(cis) != 1 || cis[0].Verdict != "red" || cis[0].Detail["ci"] != "local" {
		t.Fatalf("ci events = %+v, want one red local event", cis)
	}
	localCI = func(*Target, string, io.Writer) (tdd.LocalCIVerdict, error) {
		return tdd.LocalCIVerdict{}, errors.New("no workflow to run")
	}
	_, _ = applyMerge(t, "")
	if got := len(onSHA(emitted(t), "sha-red-local")); got != 1 {
		t.Errorf("a refusal that judged nothing emitted a ci event: %d events", got)
	}
}

func TestMergeCI_AGithubGreenEventNamesGithub(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	newCIWorld(t, tdd.CIGithub, CIStatus{State: "green", SHA: "sha-gh"})
	withHead(t, "sha-gh")
	if _, err := applyMerge(t, ""); err != nil {
		t.Fatal(err)
	}
	cis := onSHA(emitted(t), "sha-gh")
	if len(cis) != 1 || cis[0].Detail["ci"] != "github" {
		t.Fatalf("ci events = %+v, want one github event", cis)
	}
}

// onSHA is the ci events recorded for one commit.
func onSHA(evs []tdd.Event, sha string) []tdd.Event {
	var out []tdd.Event
	for _, e := range ofKind(evs, "ci") {
		if e.Detail["sha"] == sha {
			out = append(out, e)
		}
	}
	return out
}
