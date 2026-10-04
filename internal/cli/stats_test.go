package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// TestTDDStats_ReadsTheGateLog pins the command end to end: `aphrollo tdd
// stats` prints one table of what the gate has been doing, and --since
// narrows the window. Without it, the only measure of pipeline health was
// scrolling thousands of gate.log lines.
func TestTDDStats_ReadsTheGateLog(t *testing.T) {
	gateConfigDir(t)
	now := time.Now().UTC()
	stage := func(at time.Time, verdict string) {
		tdd.AppendEvent(tdd.Event{
			Kind: "gate", Stage: "precommit", Root: "D:/repo/crates/server", Cmd: "cargo test -p server",
			Verdict: verdict, Secs: 5, At: at.Format("2006-01-02T15:04:05.000Z07:00"),
		})
	}
	stage(now.Add(-time.Hour), "green")
	stage(now.Add(-200*time.Hour), "timeout-rejected")

	var out, errBuf bytes.Buffer
	if code := runGate([]string{"stats", "--since", "1d"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errBuf.String())
	}
	got := out.String()
	if !strings.Contains(got, "precommit") || !strings.Contains(got, "1 entries") && !strings.Contains(got, "1 entr") {
		t.Fatalf("table did not count the in-window entry:\n%s", got)
	}
	if strings.Contains(got, "timeout-rejected               1") {
		t.Fatalf("--since 1d must exclude the 200h-old rejection:\n%s", got)
	}

	out.Reset()
	if code := runGate([]string{"stats"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "2 entries") {
		t.Fatalf("with no window the whole log counts:\n%s", out.String())
	}
}

// With nothing logged there is no table to print: the report says so and
// exits 1, as it did before the stage lines moved to the event log.
func TestTDDStats_NoHistorySaysSoAndExitsOne(t *testing.T) {
	gateConfigDir(t)
	var out, errBuf bytes.Buffer

	code := runGate([]string{"stats"}, strings.NewReader(""), &out, &errBuf)

	if code != 1 || !strings.Contains(errBuf.String(), "no gate history") || out.Len() != 0 {
		t.Fatalf("exit = %d, stdout %q, stderr %q, want 1, nothing printed, and the reason", code, out.String(), errBuf.String())
	}
}

// A record from a newer binary may be shaped in ways this one reads wrong, so
// the report refuses rather than tally it.
func TestTDDStats_RefusesAnEventLogFromANewerSchema(t *testing.T) {
	gateConfigDir(t)
	repo := gitHubRepoCwd(t)
	tdd.AppendGateLog("postedit", repo, "go test ./...", "green", time.Second)
	files, err := filepath.Glob(filepath.Join(core.StateRoot(), "state", "*", "events-*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no event file written (%v)", err)
	}
	f, err := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(f, "{\"v\":%d,\"kind\":\"x\"}\n", core.EventSchema+1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer

	code := runGate([]string{"stats"}, strings.NewReader(""), &out, &errBuf)

	if code != 1 || !strings.Contains(errBuf.String(), "event log is at schema") {
		t.Fatalf("exit = %d, stderr %q, want 1 and the schema named", code, errBuf.String())
	}
}

// A window that opens before the oldest line kept says where the history
// starts, so a short table is not read as a quiet week.
func TestTDDStats_StatesWhereTheHistoryStartsWhenTheWindowOpensBeforeIt(t *testing.T) {
	gateConfigDir(t)
	tdd.AppendEvent(tdd.Event{
		Kind: "gate", Stage: "precommit", Root: "D:/repo", Cmd: "go test ./...", Verdict: "green", Secs: 5,
		At: time.Now().Add(-2 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z07:00"),
	})
	var out, errBuf bytes.Buffer

	if code := runGate([]string{"stats", "--since", "30d"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "events since "+time.Now().Add(-2*time.Hour).UTC().Format("2006-01-02")) {
		t.Fatalf("the window opens before the history and the table must say where it starts:\n%s", out.String())
	}
	out.Reset()
	if code := runGate([]string{"stats", "--since", "1h"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(out.String(), "events since") {
		t.Fatalf("a window the history covers needs no horizon line:\n%s", out.String())
	}
}
