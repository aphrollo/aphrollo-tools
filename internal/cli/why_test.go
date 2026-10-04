package cli

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/measure"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

func runWhyCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := Run(append([]string{"why"}, args...), strings.NewReader(""), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

// whyRepo seeds a deny, an override 2 minutes after it, and a run result, and
// returns the repo and the seq of each.
func whyRepo(t *testing.T) (repo string, denySeq, runSeq int64) {
	t.Helper()
	repo = statsRepo(t, map[time.Duration]tdd.Event{
		30 * time.Minute: {Kind: "deny", Lane: "lane/w", Stage: "preedit", Verdict: "pretooluse-denied:ratchet:module_size",
			Detail: map[string]string{"rule": "ratchet:module_size", "cause": "law", "override": "law-escape-comment"}},
		28 * time.Minute: {Kind: "override", Lane: "lane/w", Detail: map[string]string{"override": "override-x"}},
		25 * time.Minute: {Kind: "run.result", Lane: "lane/w", Verdict: "red", Detail: map[string]string{"result": "red", "latency_ms": "1241"}},
	})
	for _, e := range core.ReadEvents(repo) {
		switch e.Kind {
		case "deny":
			denySeq = e.Seq
		case "run.result":
			runSeq = e.Seq
		}
	}
	if denySeq == 0 || runSeq == 0 {
		t.Fatalf("seeded events carry no seq: deny %d run %d", denySeq, runSeq)
	}
	return repo, denySeq, runSeq
}

func TestWhy_replaysADenyAndARunFromTheRepoLog(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	repo, denySeq, runSeq := whyRepo(t)
	code, out, errOut := runWhyCmd(t, strconv.FormatInt(denySeq, 10), "--repo", repo)
	if code != 0 || errOut != "" {
		t.Fatalf("why <deny> exit %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"rule        ratchet:module_size\n", "offered     law-escape-comment\n",
		"a wrong block, within 10 min\n", "outcome     overridden\n", "shadow      no shadow fires recorded for this rule\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("deny output has no line %q:\n%s", want, out)
		}
	}
	code, out, errOut = runWhyCmd(t, "--repo", repo, strconv.FormatInt(runSeq, 10))
	if code != 0 || errOut != "" || !strings.Contains(out, "verdict     red\n") || !strings.Contains(out, "latency     1241 ms from edit to verdict\n") {
		t.Errorf("why <run> exit %d, stderr %q, output:\n%s", code, errOut, out)
	}
}

func TestWhy_jsonIsTheWhyReportAndAFlagMayFollowTheSeq(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	repo, denySeq, _ := whyRepo(t)
	code, out, errOut := runWhyCmd(t, strconv.FormatInt(denySeq, 10), "--json", "--repo", repo)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var w measure.Why
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatalf("--json is not a Why: %v\n%s", err, out)
	}
	if w.Seq != denySeq || w.Deny == nil || w.Deny.Rule != "ratchet:module_size" || !w.Deny.WrongBlock {
		t.Errorf("json = %+v, want the deny %d as a wrong block", w, denySeq)
	}
}

func TestWhy_aSeqTheLogLacksIsOneLineNamingTheSeqAndTheLog(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	repo, _, _ := whyRepo(t)
	code, out, errOut := runWhyCmd(t, "424242", "--repo", repo)
	if code != 1 || out != "" {
		t.Errorf("exit %d, stdout %q, want exit 1 and nothing on stdout", code, out)
	}
	dir := core.EventLogDir(repo)
	if strings.Count(errOut, "\n") != 1 || !strings.Contains(errOut, "424242") || !strings.Contains(errOut, dir) {
		t.Errorf("stderr %q, want one line naming seq 424242 and the log %q", errOut, dir)
	}
}

func TestWhy_refusesWhatItCannotRead(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	repo, denySeq, _ := whyRepo(t)
	seq := strconv.FormatInt(denySeq, 10)
	for name, args := range map[string][]string{
		"no seq":             {"--repo", repo},
		"two seqs":           {seq, seq, "--repo", repo},
		"not a number":       {"abc", "--repo", repo},
		"zero":               {"0", "--repo", repo},
		"unknown flag":       {seq, "--lane", "x", "--repo", repo},
		"a repo that is not": {seq, "--repo", repo + "-missing"},
	} {
		if code, out, _ := runWhyCmd(t, args...); code != 2 || out != "" {
			t.Errorf("%s: exit %d, stdout %q, want exit 2 and nothing on stdout", name, code, out)
		}
	}
}
