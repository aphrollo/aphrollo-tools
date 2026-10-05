package measure

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// redGreenFire is a would-be block of red-green: trellis blocks the code edit
// where aphrollo allows it, on the unit named.
func redGreenFire(sec float64, lane, unit string) tdd.Event {
	return shadowAt(sec, lane, "red-green", "trellis-stricter", "live_rule", "commit-proof", "unit", unit)
}

// proofAt is the commit gate's fail-first proof stage on lane, running cmd.
func proofAt(sec float64, lane, verdict, cmd string) tdd.Event {
	return ev(sec, lane, "commit_gate", func(e *tdd.Event) { e.Stage, e.Verdict, e.Cmd = "precommit", verdict, cmd })
}

func TestShadow_ARedGreenWouldBeBlockIsJudgedByTheCommitProofOnItsUnit(t *testing.T) {
	events := []tdd.Event{
		// a refused proof of a run holding the unit is the catch
		redGreenFire(0, "catch", "internal/lane"), proofAt(600, "catch", "violated", "go test ./internal/lane/..."),
		// a proof that passed is the pass, with no merge needed
		redGreenFire(0, "pass", "internal/lane"), proofAt(600, "pass", "red-proven", "go test ./internal/lane"),
		// a refusal of another package's proof says nothing of this unit
		redGreenFire(0, "other", "internal/lane"), proofAt(600, "other", "violated", "go test ./internal/store"),
		// a proof that names no package ran the whole project
		redGreenFire(0, "whole", "internal/lane"), proofAt(600, "whole", "violated", "cargo test"),
		// a refusal that is not the proof (lint, vet, docs) is no catch of red-green
		redGreenFire(0, "lint", "internal/lane"), ev(600, "lint", "commit_gate_result", verdictDetail("blocked")),
		// /tdd off within ten minutes is a wrong block, whatever follows
		redGreenFire(0, "off", "internal/lane"), ev(120, "off", "override", detail("override", "override-off")), proofAt(600, "off", "violated", "go test ./..."),
		// a proof refused first and passed later is still a catch
		redGreenFire(0, "both", "internal/lane"), proofAt(600, "both", "violated", "go test ./..."), proofAt(900, "both", "red-proven", "go test ./..."),
	}
	got := shadowRule(t, computeShadow(events, Options{}), "red-green")
	want := ShadowRule{Rule: "red-green", Fires: 7, Stricter: 7, Catches: 3, Wrong: 1, Passes: 1, Open: 2}
	if got != want {
		t.Errorf("red-green = %+v, want %+v", got, want)
	}
}

func TestProofAbout_MatchesThePackagePatternsOfTheRunToTheUnit(t *testing.T) {
	fire := func(unit string) stamped {
		return stamped{Event: tdd.Event{Kind: "shadow", Detail: map[string]string{"rule": "red-green", "unit": unit}}}
	}
	proof := func(cmd string) stamped { return stamped{Event: tdd.Event{Kind: "commit_gate", Cmd: cmd}} }
	cases := []struct {
		name, unit, cmd string
		want            bool
	}{
		{"the package itself", "internal/lane", "go test ./internal/lane", true},
		{"a tree holding it", "internal/lane/sub", "go test ./internal/lane/...", true},
		{"the whole module", "internal/lane", "go test ./...", true},
		{"a sibling", "internal/lane", "go test ./internal/laneage", false},
		{"a sibling tree", "internal/lane", "go test ./internal/store/...", false},
		{"no package named", "internal/lane", "go test", true},
		{"a module below the root", "tools/x/pkg", "go test ./pkg", true},
		{"a tree of a module below the root", "tools/x/pkg/deep", "go test ./pkg/...", true},
		{"a fire naming no unit", "", "go test ./other", true},
	}
	for _, c := range cases {
		if got := proofAbout(proof(c.cmd), fire(c.unit)); got != c.want {
			t.Errorf("%s: proofAbout(%q, unit %q) = %v, want %v", c.name, c.cmd, c.unit, got, c.want)
		}
	}
}
