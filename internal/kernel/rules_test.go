package kernel

import (
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
	"testing"
)

// wantHeldOut is the holdout arm computed independently of the production
// hash: FNV-1a over the lane, a NUL and the rule id, one lane in ten.
func wantHeldOut(lane, rule string) bool {
	h := fnv.New32a()
	h.Write([]byte(lane + "\x00" + rule))
	return h.Sum32()%10 == 0
}

// laneHeldFor finds a lane that the holdout shadows for the rule held and for
// no rule in live.
func laneHeldFor(t testing.TB, held string, live ...string) string {
	t.Helper()
	for i := 0; i < 5000; i++ {
		lane := fmt.Sprintf("arm-%d", i)
		if wantHeldOut(lane, held) && !slices.ContainsFunc(live, func(id string) bool { return wantHeldOut(lane, id) }) {
			return lane
		}
	}
	t.Fatalf("no lane held out for %s and live for %v", held, live)
	return ""
}

func allRuleIDs() []string {
	var ids []string
	for _, r := range ruleTable {
		ids = append(ids, r.ID)
	}
	return ids
}

// armLane is a lane no rule holds out, so a case's own decision is the live one.
var armLane = func() string {
	for i := 0; i < 5000; i++ {
		lane := fmt.Sprintf("live-%d", i)
		if !slices.ContainsFunc(allRuleIDs(), func(id string) bool { return wantHeldOut(lane, id) }) {
			return lane
		}
	}
	panic("no lane outside every holdout arm")
}()

func q(kind Kind, opts ...func(*Event)) Event {
	e := Event{Kind: kind, Lane: armLane, Actor: "s/a", At: t0, Claude: true}
	for _, o := range opts {
		o(&e)
	}
	return e
}

func onLane(l string) func(*Event)    { return func(e *Event) { e.Lane = l } }
func target(p PathClass) func(*Event) { return func(e *Event) { e.Target = p } }
func running(c Cmd) func(*Event)      { return func(e *Event) { e.Cmds |= c } }
func usingTool(tl Tool) func(*Event)  { return func(e *Event) { e.Tool = tl } }
func human(e *Event)                  { e.Claude = false }
func lawHit(id string, deny bool) func(*Event) {
	return func(e *Event) { e.LawHit, e.LawDeny = id, deny }
}
func failing(c Check) func(*Event) { return func(e *Event) { e.Failed = c } }
func codeEditOf(unit string) func(*Event) {
	return func(e *Event) { e.Tool, e.File, e.Unit, e.Tree = ToolWrite, ClassCode, unit, "t1" }
}

type ruleCase struct {
	id    string
	want  Outcome // for Claude, at the case's own config, outside every holdout arm
	state State
	units Units
	e     Event
	cfg   Config
}

func (c ruleCase) decide() Decision {
	s := c.state
	if s.Branch == "" {
		s.Branch, s.Life = c.e.Lane, LifeOpen
	}
	return Decide(s, c.units, c.e, c.cfg)
}

var (
	enforceTDD = Config{TDD: ModeEnforce}
	closedUnit = Units{"pkg/a": {Phase: PhaseClosed, Tree: "t1"}}
)

// cases fires every rule of the table once. Each is a fact pattern the
// architecture document names for the rule.
func cases() []ruleCase {
	trunk := onLane(TrunkLane)
	return []ruleCase{
		{id: "primary-write", want: OutcomeDeny, e: q(KindPreTool, trunk, usingTool(ToolWrite), target(PathPrimary))},
		{id: "bypass-gate", want: OutcomeDeny, e: q(KindPreTool, usingTool(ToolBash), running(CmdBypassGate))},
		{id: "trunk-move", want: OutcomeDeny, e: q(KindPreTool, trunk, usingTool(ToolBash), target(PathPrimary), running(CmdMoveOffTrunk))},
		{id: "trunk-commit", want: OutcomeDeny, e: q(KindPreCommit, trunk, target(PathPrimary), func(e *Event) { e.NonMerge = true })},
		{id: "trunk-push", want: OutcomeDeny, e: q(KindPrePush, running(CmdPushTrunk), func(e *Event) { e.NonMerge = true })},
		{id: "merge-bypass", want: OutcomeDeny, e: q(KindPreTool, usingTool(ToolBash), running(CmdMergePR))},
		{id: "merge-no-green", want: OutcomeDeny, e: q(KindPreMerge, func(e *Event) { e.GreenOS = []string{"linux"} }),
			cfg: Config{CIOS: []string{"linux", "windows"}}},
		{id: "discard-work", want: OutcomeDeny, e: q(KindPreTool, usingTool(ToolBash), running(CmdDiscard))},
		{id: "attribution", want: OutcomeDeny, e: q(KindPreCommit, func(e *Event) { e.Attribution = true }), cfg: Config{Undercover: true}},
		{id: "secrets", want: OutcomeDeny, e: q(KindPreCommit, func(e *Event) { e.Secret = true })},
		{id: "deny-law-edit", want: OutcomeDeny, e: q(KindPreTool, usingTool(ToolWrite), lawHit("no-panic", true))},
		{id: "deny-law-commit", want: OutcomeDeny, e: q(KindPreCommit, lawHit("no-panic", true))},
		{id: "commit-proof", want: OutcomeDeny, e: q(KindPreCommit, failing(CheckProof))},
		{id: "commit-vet", want: OutcomeDeny, e: q(KindPreCommit, failing(CheckVet))},
		{id: "bypass-verb", want: OutcomeDeny, e: q(KindPreTool, usingTool(ToolBash), running(CmdOutward))},
		{id: "red-green", want: OutcomeDeny, e: q(KindPreTool, codeEditOf("pkg/a")), units: closedUnit, cfg: enforceTDD},
		{id: "stop-red", want: OutcomeDeny, e: q(KindStop, func(e *Event) { e.UnseenRed = true }),
			units: Units{"pkg/a": {Phase: PhaseOpen, Test: "TestA"}}, cfg: enforceTDD},
		{id: "mutation", want: OutcomeDeny, e: q(KindPreCommit, func(e *Event) { e.Survivors = 2 }), cfg: Config{Mutation: LevelEnforce}},
		{id: "warn-law", want: OutcomeGuide, e: q(KindPreTool, usingTool(ToolWrite), lawHit("no-todo", false))},
		{id: "lint", want: OutcomeGuide, e: q(KindPreCommit, failing(CheckLint))},
		{id: "commit-checks", want: OutcomeGuide, e: q(KindPreCommit, failing(CheckDocs))},
		{id: "long-wait", want: OutcomeGuide, e: q(KindPreTool, usingTool(ToolBash), running(CmdLongWait))},
		{id: "rerun-suite", want: OutcomeGuide, e: q(KindPreTool, usingTool(ToolBash), running(CmdRerun))},
		{id: "noisy-output", want: OutcomeGuide, e: q(KindPreTool, usingTool(ToolBash), running(CmdNoisy))},
		{id: "not-tested", want: OutcomeGuide, units: closedUnit,
			e: q(KindRunResult, func(e *Event) { e.Unit, e.Tree, e.Verdict, e.Cause = "pkg/a", "t1", VerdictNotTested, CauseTimeout }, human)},
		{id: "escape-hold", want: OutcomeGuide, units: Units{"pkg/a": {Phase: PhaseHeld, Hold: "esc-1"}}, e: q(KindPreTool, codeEditOf("pkg/a"))},
		{id: "run-result", want: OutcomeGuide, units: closedUnit,
			e: q(KindRunResult, func(e *Event) { e.Unit, e.Tree, e.Verdict = "pkg/a", "t1", VerdictRedBogus }, human)},
	}
}

func ruleByID(id string) Rule {
	for _, r := range ruleTable {
		if r.ID == id {
			return r
		}
	}
	panic("no rule " + id)
}

func TestRuleTable_everyRowIsComplete(t *testing.T) {
	seen := map[string]bool{}
	last := RuleWall
	order := []RuleClass{RuleWall, RuleEarned, RulePinned, RuleGuide}
	for _, r := range ruleTable {
		if seen[r.ID] {
			t.Errorf("rule id %q appears twice", r.ID)
		}
		seen[r.ID] = true
		if r.ID == "" || r.ID != strings.ToLower(r.ID) || strings.ContainsAny(r.ID, " _") {
			t.Errorf("rule id %q is not kebab-case", r.ID)
		}
		if !strings.HasPrefix(r.Section, "§") {
			t.Errorf("%s: section %q names no document section", r.ID, r.Section)
		}
		if r.Cause == "" || r.Next == "" || r.Reads == nil {
			t.Errorf("%s: a row needs a cause, a next step and a reading of the facts", r.ID)
		}
		switch r.Do {
		case OutcomeDeny:
			if r.Override == "" {
				t.Errorf("%s denies and offers no override", r.ID)
			}
			if r.Class == RuleGuide {
				t.Errorf("%s is a guide-class rule that denies", r.ID)
			}
		case OutcomeGuide:
			if r.Class != RuleGuide {
				t.Errorf("%s guides but is class %v: only a guide-class rule may stop at a guide", r.ID, r.Class)
			}
		default:
			t.Errorf("%s decides %q: a row denies or guides", r.ID, r.Do)
		}
		if slices.Index(order, r.Class) < slices.Index(order, last) {
			t.Errorf("%s (class %v) comes after a later class: the table order is the precedence", r.ID, r.Class)
		}
		last = r.Class
	}
	for _, c := range cases() {
		if !seen[c.id] {
			t.Errorf("case for %q names no rule", c.id)
		}
		delete(seen, c.id)
	}
	for id := range seen {
		t.Errorf("rule %q has no case in the walk", id)
	}
}

func TestDecide_eachRuleFiresItsDecisionForClaude(t *testing.T) {
	for _, c := range cases() {
		d := c.decide()
		if d.Rule != c.id || d.Outcome != c.want {
			t.Errorf("%s: got rule %q outcome %q, want %q", c.id, d.Rule, d.Outcome, c.want)
			continue
		}
		if d.Section != ruleByID(c.id).Section || d.Cause == "" || d.Next == "" {
			t.Errorf("%s: decision %+v lacks the row's section, cause or next step", c.id, d)
		}
		if c.want == OutcomeDeny && (d.Override == "" || d.Level != LevelEnforce || d.WouldDeny || d.HeldOut) {
			t.Errorf("%s: deny %+v must offer an override at enforce and not be marked as a downgrade", c.id, d)
		}
	}
}

func TestDecide_aRuleAtOffIsSilent(t *testing.T) {
	for _, c := range cases() {
		c.cfg.Rules = map[string]Level{c.id: LevelOff}
		if d := c.decide(); d.Rule == c.id {
			t.Errorf("%s pinned off still fired: %+v", c.id, d)
		}
	}
}

func TestDecide_aRuleAtWarnGuidesAndNoRuleDenies(t *testing.T) {
	for _, c := range cases() {
		c.cfg.Rules = map[string]Level{c.id: LevelWarn}
		d := c.decide()
		if d.Rule != c.id || d.Outcome != OutcomeGuide {
			t.Errorf("%s pinned warn: got rule %q outcome %q, want a guide", c.id, d.Rule, d.Outcome)
		}
		if wantDeny := ruleByID(c.id).Do == OutcomeDeny; d.WouldDeny != wantDeny {
			t.Errorf("%s pinned warn: WouldDeny = %v, want %v", c.id, d.WouldDeny, wantDeny)
		}
	}
}

func TestDecide_aGuideRuleNeverDeniesEvenPinnedToEnforce(t *testing.T) {
	for _, c := range cases() {
		if ruleByID(c.id).Class != RuleGuide {
			continue
		}
		c.cfg.Rules = map[string]Level{c.id: LevelEnforce}
		if d := c.decide(); d.Outcome != OutcomeGuide || d.WouldDeny {
			t.Errorf("%s at enforce: %+v, want a plain guide: the document lets this rule only guide", c.id, d)
		}
	}
}

func TestDecide_aHumanIsAdvisedNotBlockedExceptForSecrets(t *testing.T) {
	for _, c := range cases() {
		c.e.Claude = false
		d := c.decide()
		if c.id == "secrets" {
			if d.Outcome != OutcomeDeny {
				t.Errorf("secrets must block every author, got %q", d.Outcome)
			}
			continue
		}
		if d.Rule != c.id || d.Outcome != OutcomeGuide {
			t.Errorf("%s as a human: got rule %q outcome %q, want its advice", c.id, d.Rule, d.Outcome)
		}
		if want := ruleByID(c.id).Do == OutcomeDeny; d.WouldDeny != want {
			t.Errorf("%s as a human: WouldDeny = %v, want %v", c.id, d.WouldDeny, want)
		}
	}
}

func TestDecide_theHoldoutDowngradesOnlyAnEarnedBlockInItsArm(t *testing.T) {
	for _, c := range cases() {
		r := ruleByID(c.id)
		if r.Class == RuleEarned {
			c.e.Lane = laneHeldFor(t, c.id)
		}
		d := c.decide()
		switch {
		case r.Class == RuleEarned:
			if d.Rule != c.id || d.Outcome != OutcomeGuide || !d.HeldOut || !d.WouldDeny {
				t.Errorf("%s held out: got %+v, want a marked guide that would have denied", c.id, d)
			}
			// the same lane with the rule pinned is never shadowed
			c.cfg.Rules = map[string]Level{c.id: LevelEnforce}
			if d := c.decide(); d.Outcome != OutcomeDeny || d.HeldOut {
				t.Errorf("%s pinned to enforce at a held-out lane: got %+v, want a deny", c.id, d)
			}
		case d.HeldOut:
			t.Errorf("%s (class %v) was held out: only earned blocks are shadowed", c.id, r.Class)
		}
	}
}

func TestDecide_theArmIsPerLaneAndAboutOneInTen(t *testing.T) {
	held := 0
	for i := 0; i < 2000; i++ {
		lane := fmt.Sprintf("lane-%d", i)
		got := heldOut(lane, "commit-proof")
		if got != wantHeldOut(lane, "commit-proof") || got != heldOut(lane, "commit-proof") {
			t.Fatalf("lane %q: arm %v disagrees with the reference hash or with itself", lane, got)
		}
		if got {
			held++
		}
	}
	if held < 120 || held > 280 {
		t.Errorf("%d of 2000 lanes held out, want about 200 (10%%)", held)
	}
	// the arm is a function of the lane and the rule: another rule splits the lanes differently
	same := true
	for i := 0; i < 200 && same; i++ {
		lane := fmt.Sprintf("lane-%d", i)
		same = heldOut(lane, "commit-proof") == heldOut(lane, "commit-vet")
	}
	if same {
		t.Error("two rules put the same lanes in the holdout: the rule id is not part of the hash")
	}
}

func TestDecide_earlierRowWinsAndADenyBeatsAGuide(t *testing.T) {
	wall := q(KindPreTool, usingTool(ToolBash), running(CmdBypassGate|CmdDiscard|CmdOutward|CmdNoisy))
	if d := Decide(State{}, nil, wall, Config{}); d.Rule != "bypass-gate" {
		t.Errorf("wall vs wall vs earned: rule %q, want bypass-gate (first row)", d.Rule)
	}
	both := q(KindPreTool, usingTool(ToolBash), running(CmdOutward|CmdNoisy))
	if d := Decide(State{}, nil, both, Config{}); d.Rule != "bypass-verb" || d.Outcome != OutcomeDeny {
		t.Errorf("earned deny vs guide: %+v, want the bypass-verb deny", d)
	}
	advised := q(KindPreTool, usingTool(ToolBash), running(CmdDiscard|CmdLongWait), human)
	if d := Decide(State{}, nil, advised, Config{}); d.Rule != "discard-work" || d.Outcome != OutcomeGuide {
		t.Errorf("two guides: %+v, want the earlier row's advice", d)
	}
	// a held-out earned deny is only a guide, so a later earned deny still wins
	lane := laneHeldFor(t, "deny-law-commit", "commit-proof")
	mixed := q(KindPreCommit, onLane(lane), lawHit("no-panic", true), failing(CheckProof))
	if d := Decide(State{}, nil, mixed, Config{}); d.Rule != "commit-proof" || d.Outcome != OutcomeDeny {
		t.Errorf("held-out deny vs live deny: %+v, want the live commit-proof deny", d)
	}
}

func TestDecide_neverBlocksWhereTheFactsDoNotSayDamage(t *testing.T) {
	trunk := onLane(TrunkLane)
	for name, c := range map[string]ruleCase{
		"a write that lands in a lane":                  {e: q(KindPreTool, trunk, usingTool(ToolWrite), target(PathLane))},
		"a write whose target is unknown":               {e: q(KindPreTool, trunk, usingTool(ToolWrite))},
		"a primary write from a lane that is not trunk": {e: q(KindPreTool, usingTool(ToolWrite), target(PathPrimary))},
		"a primary write with isolation off":            {e: q(KindPreTool, trunk, usingTool(ToolWrite), target(PathPrimary)), cfg: Config{NoIsolation: true}},
		"attribution where the repo is not undercover":  {e: q(KindPreCommit, func(e *Event) { e.Attribution = true })},
		"a merge green on every declared OS":            {e: q(KindPreMerge, func(e *Event) { e.GreenOS = []string{"windows", "linux"} }), cfg: Config{CIOS: []string{"linux", "windows"}}},
		"a merge commit pushed to trunk":                {e: q(KindPrePush, running(CmdPushTrunk))},
		"a stop in the stop hook's own turn": {e: q(KindStop, func(e *Event) { e.UnseenRed, e.StopActive = true, true }),
			units: Units{"pkg/a": {Phase: PhaseOpen}}, cfg: enforceTDD},
		"a stop under warn":                      {e: q(KindStop, func(e *Event) { e.UnseenRed = true }), units: Units{"pkg/a": {Phase: PhaseOpen}}},
		"a stop on a red already seen":           {e: q(KindStop), units: Units{"pkg/a": {Phase: PhaseOpen}}, cfg: enforceTDD},
		"a code edit of tested code":             {e: q(KindPreTool, codeEditOf("pkg/a"), func(e *Event) { e.Covered = true }), units: closedUnit, cfg: enforceTDD},
		"a code edit in an open unit":            {e: q(KindPreTool, codeEditOf("pkg/a")), units: Units{"pkg/a": {Phase: PhaseOpen, Tree: "t1"}}, cfg: enforceTDD},
		"an untested code edit with tdd off":     {e: q(KindPreTool, codeEditOf("pkg/a")), units: closedUnit, cfg: Config{TDD: ModeOff}},
		"mutation survivors with mutation unset": {e: q(KindPreCommit, func(e *Event) { e.Survivors = 3 })},
		"an unknown tdd mode":                    {e: q(KindPreTool, codeEditOf("pkg/a")), units: closedUnit, cfg: Config{TDD: "odd"}},
	} {
		if d := c.decide(); d.Outcome == OutcomeDeny {
			t.Errorf("%s was denied: %+v", name, d)
		}
	}
}

func TestDecide_aBashWriteToThePrimaryCheckoutOnTrunkIsDeniedLikeAWrite(t *testing.T) {
	e := q(KindPreTool, onLane(TrunkLane), usingTool(ToolBash), target(PathPrimary), running(CmdWrite))
	if d := Decide(State{}, nil, e, Config{}); d.Rule != "primary-write" || d.Outcome != OutcomeDeny {
		t.Errorf("bash write: %+v, want a primary-write deny", d)
	}
}

func TestDecide_theMergeWallNamesTheOSesWithoutAGreen(t *testing.T) {
	e := q(KindPreMerge, func(e *Event) { e.GreenOS = []string{"linux"} })
	declared := State{Branch: e.Lane, Life: LifePR, CIRequired: []string{"linux", "macos", "windows"}}
	d := Decide(declared, nil, e, Config{CIOS: []string{"linux"}})
	if d.Rule != "merge-no-green" || d.Detail != "macos,windows" {
		t.Errorf("got rule %q detail %q, want merge-no-green naming macos,windows (the lane's declared OSes win over config)", d.Rule, d.Detail)
	}
	if d := Decide(State{}, nil, q(KindPreMerge), Config{}); d.Rule != "merge-no-green" || d.Detail != "linux" {
		t.Errorf("no declaration anywhere: %+v, want the document's default OS list, linux", d)
	}
}

func TestDecide_theStopWallNamesTheOpenUnit(t *testing.T) {
	units := Units{"pkg/z": {Phase: PhaseOpen}, "pkg/b": {Phase: PhaseOpen}, "pkg/a": {Phase: PhaseClosed}}
	e := q(KindStop, func(e *Event) { e.UnseenRed = true })
	for range 20 {
		if d := Decide(State{}, units, e, enforceTDD); d.Rule != "stop-red" || d.Detail != "pkg/b" {
			t.Fatalf("got %+v, want stop-red naming the first open unit, pkg/b", d)
		}
	}
}

func TestDecide_redGreenFollowsTheTddModeAndItsOwnPin(t *testing.T) {
	edit := q(KindPreTool, codeEditOf("pkg/a"))
	for name, tc := range map[string]struct {
		units Units
		cfg   Config
		want  Outcome
		rule  string
	}{
		"warn guides the first time":            {closedUnit, Config{}, OutcomeGuide, "red-green"},
		"off freezes the machine and the rule":  {closedUnit, Config{TDD: ModeOff}, OutcomeAllow, ""},
		"warn guides once per unit per lane":    {Units{"pkg/a": {Phase: PhaseClosed, Tree: "t1", GuidedUntested: true}}, Config{}, OutcomeAllow, ""},
		"enforce denies every time":             {Units{"pkg/a": {Phase: PhaseClosed, Tree: "t1", GuidedUntested: true}}, enforceTDD, OutcomeDeny, "red-green"},
		"the rule pinned to enforce under warn": {closedUnit, Config{Rules: map[string]Level{"red-green": LevelEnforce}}, OutcomeDeny, "red-green"},
		"the rule pinned off under enforce":     {closedUnit, Config{TDD: ModeEnforce, Rules: map[string]Level{"red-green": LevelOff}}, OutcomeAllow, ""},
	} {
		d := Decide(State{}, tc.units, edit, tc.cfg)
		if d.Outcome != tc.want || d.Rule != tc.rule {
			t.Errorf("%s: got rule %q outcome %q, want %q %q", name, d.Rule, d.Outcome, tc.rule, tc.want)
		}
	}
}

func TestDecide_aQuestionLeavesBothMachinesWhereTheyWereAndAFactMovesThem(t *testing.T) {
	units := Units{"pkg/a": {Phase: PhaseClosed, Tree: "t1"}}
	lane := State{Branch: armLane, Life: LifeOpen}
	// a test edit asked about at PreToolUse is not yet an edit that happened
	ask := q(KindPreTool, func(e *Event) { e.Tool, e.File, e.Test, e.Unit, e.Tree = ToolWrite, ClassTest, "TestA", "pkg/a", "t1" })
	d := Decide(lane, units, ask, Config{})
	if d.Units["pkg/a"].Phase != PhaseClosed || d.Lane.Life != LifeOpen || len(d.Effects) != 0 {
		t.Errorf("a question moved state: %+v", d)
	}
	// the same file as a recorded edit does move the unit
	d = Decide(lane, units, q(KindEdit, func(e *Event) { e.File, e.Test, e.Unit, e.Tree = ClassTest, "TestA", "pkg/a", "t1" }), Config{})
	if d.Units["pkg/a"].Phase != PhasePending || !slices.ContainsFunc(d.Effects, func(f Effect) bool { return f.Kind == EffectRequestRun }) {
		t.Errorf("an edit fact did not run the TDD machine: %+v", d)
	}
	// a merge fact moves the lane machine
	d = Decide(State{Branch: armLane, Life: LifeCIGreen}, nil, q(KindLaneMerged), Config{})
	if d.Lane.Life != LifeMerged || !slices.ContainsFunc(d.Effects, func(f Effect) bool { return f.Kind == EffectLaneNews }) {
		t.Errorf("a merge fact did not run the lane machine: %+v", d)
	}
}

func TestDecide_aDenyOnAFactThatCannotBeRefusedIsOnlyAGuide(t *testing.T) {
	// an edit already made (PostToolUse) cannot be denied: the untested-code line is a guide
	c := ruleCase{e: q(KindEdit, codeEditOf("pkg/a")), units: closedUnit, cfg: enforceTDD}
	d := c.decide()
	if d.Rule != "red-green" || d.Outcome != OutcomeGuide || !d.WouldDeny {
		t.Errorf("got %+v, want a red-green guide that would have denied", d)
	}
}

func TestDecide_aPinWithAnUnknownLevelIsIgnoredNotReadAsOff(t *testing.T) {
	c := cases()[0]
	c.cfg.Rules = map[string]Level{c.id: "blokc"}
	if d := c.decide(); d.Rule != c.id || d.Outcome != OutcomeDeny {
		t.Errorf("a misspelt pin changed the decision: %+v", d)
	}
}
