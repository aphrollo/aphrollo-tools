package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/shadow"
)

// This package's tests run with a minute for a shadow record, so a loaded box
// never turns the record under test into a budget skip. That would hide the
// overrun path from every wired test, so it is pinned here, with a budget no
// record can meet: the call still answers as it would, and the shadow says what
// it could not judge, as unjudged and by its cause, never as a guess.
func TestRun_PreToolUse_ARedGreenRecordPastTheBudgetIsUnjudgedWithItsCause(t *testing.T) {
	gateConfigDir(t)
	dir := goRepo(t)
	old := shadow.Budget
	t.Cleanup(func() { shadow.Budget = old })
	shadow.Budget = time.Nanosecond

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "pretooluse"}, strings.NewReader(goEditPayload(t, dir, "pkg/p.go")), &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0: the budget never changes the live decision\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}

	got := shadowOfRule(dir, "red-green")
	if len(got) != 1 {
		t.Fatalf("%d red-green records, want the one unjudged record: %+v", len(got), shadowEvents(dir))
	}
	if d := got[0].Detail; d["relation"] != "unjudged" || d["cause"] != "budget" {
		t.Errorf("record detail = %v, want relation unjudged and cause budget", d)
	}
}
