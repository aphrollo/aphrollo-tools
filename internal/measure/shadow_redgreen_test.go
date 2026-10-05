package measure

import (
	"strings"
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
		// a proof that names no package is about no unit: a stage's label is no proof of this one
		redGreenFire(0, "whole", "internal/lane"), proofAt(600, "whole", "violated", "cargo test"),
		redGreenFire(0, "label", "internal/lane"), proofAt(600, "label", "red-proven", "postedit-ledger"),
		// a refusal that is not the proof (lint, vet, docs) is no catch of red-green
		redGreenFire(0, "lint", "internal/lane"), ev(600, "lint", "commit_gate_result", verdictDetail("blocked")),
		// /tdd off within ten minutes is a wrong block, whatever follows
		redGreenFire(0, "off", "internal/lane"), ev(120, "off", "override", detail("override", "override-off")), proofAt(600, "off", "violated", "go test ./..."),
		// a proof refused first and passed later is still a catch
		redGreenFire(0, "both", "internal/lane"), proofAt(600, "both", "violated", "go test ./..."), proofAt(900, "both", "red-proven", "go test ./..."),
	}
	got := shadowRule(t, computeShadow(events, Options{}), "red-green")
	want := ShadowRule{Rule: "red-green", Fires: 8, Stricter: 8, Catches: 2, Wrong: 1, Passes: 1, Open: 4}
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
		{"no package named", "internal/lane", "go test", false},
		{"a stage's label", "internal/lane", "postedit-ledger", false},
		{"a fire naming no unit", "", "go test ./other", true},
	}
	for _, c := range cases {
		if got := proofAbout(proof(c.cmd), fire(c.unit)); got != c.want {
			t.Errorf("%s: proofAbout(%q, unit %q) = %v, want %v", c.name, c.cmd, c.unit, got, c.want)
		}
	}
}

func TestProofAbout_ANestedModulesPackageIsComparedFromItsModuleRootNotBySuffix(t *testing.T) {
	proof := func(cmd string) stamped { return stamped{Event: tdd.Event{Kind: "commit_gate", Cmd: cmd}} }
	nested := func(unit, pkg string) stamped {
		return stamped{Event: tdd.Event{Kind: "shadow", Detail: map[string]string{"rule": "red-green", "unit": unit, "unit_pkg": pkg}}}
	}
	for _, c := range []struct {
		name, cmd string
		want      bool
	}{
		{"the package from the module root", "go test ./pkg", true},
		{"a tree of it", "go test ./pkg/...", true},
		{"another module's package with the same tail", "go test ./other/pkg", false},
		{"a package that merely ends alike", "go test ./sub/pkg/...", false},
	} {
		if got := proofAbout(proof(c.cmd), nested("tools/x/pkg", "pkg")); got != c.want {
			t.Errorf("%s: proofAbout(%q) = %v, want %v", c.name, c.cmd, got, c.want)
		}
	}
}

func TestShadow_ARecordThatOutranTheBudgetIsCountedAndPrinted(t *testing.T) {
	events := []tdd.Event{
		shadowAt(0, "a", "red-green", "unjudged", "cause", "budget"),
		shadowAt(1, "a", "red-green", "unjudged", "cause", "no-unit"),
		shadowAt(2, "a", "red-green", "unjudged", "cause", "budget"),
	}
	s := computeShadow(events, Options{})
	got := shadowRule(t, s, "red-green")
	if got.Unjudged != 3 || got.Overruns != 2 {
		t.Errorf("red-green = %+v, want 3 unjudged of which 2 overran the budget", got)
	}
	if !strings.Contains(s.Text(), "budget overruns 2") {
		t.Errorf("the text does not say the overruns: %s", s.Text())
	}
}
