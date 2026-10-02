package rollback

import (
	"reflect"
	"testing"

	core "github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

func TestEmit_WritesAnUpdateEventWithItsStageVerdictAndDetail(t *testing.T) {
	stateDir(t)

	Emit("pin", "set", map[string]string{"ref": "v1.3.0", "commit": commitA})

	got := core.ReadEvents()
	if len(got) != 1 {
		t.Fatalf("events = %+v, want exactly one", got)
	}
	e := got[0]
	if e.Kind != "update" || e.Stage != "pin" || e.Verdict != "set" {
		t.Fatalf("event = %+v, want kind update, stage pin, verdict set", e)
	}
	if want := map[string]string{"ref": "v1.3.0", "commit": commitA}; !reflect.DeepEqual(e.Detail, want) {
		t.Fatalf("detail = %v, want %v", e.Detail, want)
	}
	if e.Repo != "" || e.Lane != "" {
		t.Fatalf("event names a repo or lane (%q, %q): a box-level update belongs to neither", e.Repo, e.Lane)
	}
}
