package measure

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// pjFire is a would-be block of red-green on a project-root unit (Python, TypeScript):
// the fire names the project's root, for no package pattern can say what a proof of
// it ran.
func pjFire(sec float64, lane, unit, root string) tdd.Event {
	return shadowAt(sec, lane, "red-green", "trellis-stricter", "live_rule", "commit-proof", "unit", unit, "unit_root", root)
}

// pjProofIn is the commit gate's proof stage on lane, run in the project at root.
func pjProofIn(sec float64, lane, verdict, cmd, root string) tdd.Event {
	return ev(sec, lane, "commit_gate", func(e *tdd.Event) { e.Stage, e.Verdict, e.Cmd, e.Root = "precommit", verdict, cmd, root })
}

func TestShadow_APythonOrTypeScriptWouldBeBlockIsJudgedByTheCommitProofRunInItsProject(t *testing.T) {
	events := []tdd.Event{
		// a refused proof run in the unit's project is the catch
		pjFire(0, "catch", "python:backend", "/w/backend"), pjProofIn(600, "catch", "violated", "python -m pytest tests", "/w/backend"),
		// a proof that passed there is the pass
		pjFire(0, "pass", "typescript:frontend", "/w/frontend"), pjProofIn(600, "pass", "red-proven", "npx vitest run", "/w/frontend"),
		// a proof of another project says nothing of this unit
		pjFire(0, "other", "python:backend", "/w/backend"), pjProofIn(600, "other", "violated", "npx vitest run", "/w/frontend"),
		// the same path spelled with backslashes and a trailing separator is the same project
		pjFire(0, "spelled", "python:backend", "/w/backend"), pjProofIn(600, "spelled", "violated", "pytest", `\w\backend\`),
		// a fire of a Go unit is still judged by packages, not by where the proof ran
		redGreenFire(0, "go", "internal/lane"), pjProofIn(600, "go", "violated", "go test ./internal/store", "/w"),
	}
	got := shadowRule(t, computeShadow(events, Options{}), "red-green")
	want := ShadowRule{Rule: "red-green", Fires: 5, Stricter: 5, Catches: 2, Passes: 1, Open: 2}
	if got != want {
		t.Errorf("red-green = %+v, want %+v", got, want)
	}
}
