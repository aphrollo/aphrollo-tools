package cli

import (
	"encoding/json"
	"io"
	"path/filepath"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A lane in the off arm (tddarm.ArmOff, assigned by the repo's ab-off-share) runs with
// aphrollo off: it is the control of the on/off comparison, so no hook answers and no gate
// judges it. Which entry point does what is this table, and it is data: a session hook
// that is not here fails the table test, and a new git hook joins gitHooks and this table
// or the same test fails.

// laneOffHow is what an entry point does for an off lane.
type laneOffHow int

const (
	// laneSilent: the entry point answers nothing, decides nothing, runs no suite and
	// leaves no gate event.
	laneSilent laneOffHow = iota
	// laneLifecycle: the entry point records the lane's lifecycle (the lane's arm, a
	// commit's time and sha, a merge) and does nothing else.
	laneLifecycle
	// laneKept: the entry point runs as it does for any lane, because it is a wall that
	// blocks every author, not a gate of aphrollo's (the secrets wall, once it exists, is
	// the first such; the undercover layer of commit-msg and push is the other).
	laneKept
)

// laneEntry is one row: what the entry point does for an off lane, and why.
type laneEntry struct {
	How laneOffHow
	Why string
}

// offLaneEntries is every entry point that decides something, by the name `aphrollo gate`
// accepts it under. The cargo, git and lint shims are not rows: the git shim's walls are
// skipped for an off lane in runGitShim (its undercover refusal stays), and the cargo and
// lint shims only queue behind a lock, answering nothing when it is free.
var offLaneEntries = map[string]laneEntry{
	"sessionstart":       {laneSilent, "injects the skill nudge"},
	"pretooluse":         {laneSilent, "red-green, smells, laws, guardrail walls; the always-on walls (alwaysOnWalls) still judge"},
	"posttooluse":        {laneSilent, "runs the related suites after an edit"},
	"posttoolusefailure": {laneSilent, "counts a suite the agent ran by hand"},
	"userpromptsubmit":   {laneSilent, "re-injects a RED reminder and handles /aphrollo"},
	"sessionend":         {laneSilent, "only reaps the session's own state"},
	"stop":               {laneSilent, "blocks the end of a turn over a red deferred run"},
	"subagentstop":       {laneSilent, "the same, for a subagent"},
	"taskcompleted":      {laneSilent, "exits 2 while the task's tests are red"},
	"guardrail":          {laneSilent, "`aphrollo guardrail pretooluse`, the policy walls as a hook of their own; the always-on walls still judge"},
	"precommit":          {laneSilent, "fail-first, laws, docs, vet, lint, suites"},
	"premerge":           {laneSilent, "the mechanical suites over the merge"},
	"premergecommit":     {laneSilent, "alias of premerge"},
	"prepush":            {laneKept, "its one check is the undercover ref wall"},
	"commitmsg":          {laneKept, "the undercover layer only; house style and verification claims are off"},
	"postcommit":         {laneLifecycle, "records the commit; the gate note and the lane sweep are off"},
	"postmerge":          {laneLifecycle, "records the merges brought in; the lane sweep is off"},
	"postrewrite":        {laneLifecycle, "records the commits a rebase or amend wrote"},
}

// gitHooks are the git-side entry points `aphrollo gate` accepts; each has a row above.
var gitHooks = []string{"precommit", "premerge", "premergecommit", "prepush", "commitmsg", "postcommit", "postmerge", "postrewrite"}

// laneOffAt reports whether the lane of the checkout dir stands in is in the off arm, and
// records the lane's arm at first sight: the record is what keeps the lane in its arm
// (postedit.EffectiveTDD). It spawns nothing.
func laneOffAt(dir string) bool { return laneOffFor(dir, "") }

// laneOffFor is laneOffAt for the session or agent actor that asked.
func laneOffFor(dir, actor string) bool {
	if dir == "" {
		return false
	}
	m := tdd.EffectiveTDD(dir)
	tdd.RecordLaneArm(dir, actor, m)
	return m.Off()
}

// noteOffLane records, once, the arm of the lane of root: the lane-arm event.
func noteOffLane(root, actor string) {
	tdd.RecordLaneArm(root, actor, tdd.EffectiveTDD(root))
}

// offLaneHook reports whether the hook that read raw belongs to an off lane, from the
// cwd its payload names, and records the lane's arm when it does. An off lane's hook
// answers nothing: runOffLaneHook is what is done in its place.
func offLaneHook(raw []byte) bool {
	var in struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
		Cwd       string `json:"cwd"`
		ToolInput struct {
			FilePath string `json:"file_path"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return false
	}
	dir := in.Cwd
	if dir == "" && in.ToolInput.FilePath != "" {
		dir = filepath.Dir(in.ToolInput.FilePath) // only a fallback: the cwd names the lane the hook serves
	}
	actor := in.SessionID
	if in.AgentID != "" {
		actor += "/" + in.AgentID
	}
	return laneOffFor(dir, actor)
}

// runOffLaneHook is the answer of a session hook of an off lane: nothing, bar the
// always-on walls for PreToolUse, and the session's own state reaped at its end.
func runOffLaneHook(hook string, raw []byte, stdout io.Writer) int {
	if hook == "sessionend" {
		tdd.EndSession(raw)
	}
	return runWhenOff(sessionHooks[hook], raw, stdout)
}
