package cli

import (
	"encoding/json"
	"path/filepath"
	"slices"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// preShadow gathers what one PreToolUse judgement observed, as facts for the
// shadow record: the rules the kernel decides from the call's own facts, and
// what the gate did about each. It decides nothing and the gate never reads it
// back. It gathers by flags and values only; whatever needs a look at the
// repository is asked for inside the record's own bound (record).
type preShadow struct {
	raw  []byte
	bash bool

	facts []shadow.Fact

	// The primary-checkout wall is recorded lazily: where its write lands is a
	// question for the record, not for the hook.
	primaryBlocked bool // the wall blocked the call
	primaryWaived  bool // a waiver let the call by the wall unlooked at

	// final is what the call came to once every judgement was folded: a waived
	// call a later judgement blocked was blocked.
	final shadow.Action
}

func newPreShadow(raw []byte) *preShadow {
	return &preShadow{raw: raw, bash: tdd.IsBashHook(raw), final: shadow.Allow}
}

func (p *preShadow) add(f shadow.Fact) { p.facts = append(p.facts, f) }

func (p *preShadow) tool() kernel.Tool {
	if p.bash {
		return kernel.ToolBash
	}
	return kernel.ToolWrite
}

// wall notes the wall that blocked the call, by the policy it counts under.
func (p *preShadow) wall(d tdd.Decision) {
	if d.Action != tdd.Block {
		return
	}
	switch d.Policy {
	case "primary-checkout":
		p.primaryBlocked = true
	case "discard-bash":
		p.add(shadow.Discard(shadow.Block))
	case "direct-pr-open":
		p.add(shadow.Outward(d.Policy, shadow.Block))
	case "undercover":
		p.add(shadow.Attribution(shadow.Block))
	}
}

// passedWalls notes that no wall blocked the call: a write a primary-edits waiver
// let through is one the wall never looked at.
func (p *preShadow) passedWalls() { p.primaryWaived = true }

// law notes each finding of the law engine over the edit, by its own severity: a
// deny finding is the deny-law-edit rule, a warn finding the warn-law rule.
func (p *preShadow) law(found []tdd.LawFinding) {
	for _, f := range found {
		actual := shadow.Warn
		if f.Deny {
			actual = shadow.Block
		}
		p.add(shadow.Law("ratchet:"+f.Law, f.Deny, actual))
	}
}

// settle notes what the call came to.
func (p *preShadow) settle(final tdd.Decision) { p.final = actionOf(final) }

func actionOf(d tdd.Decision) shadow.Action {
	switch d.Action {
	case tdd.Block:
		return shadow.Block
	case tdd.Warn:
		return shadow.Warn
	}
	return shadow.Allow
}

// record writes the facts the call yielded, after the hook's answer is written:
// record-only and bounded by shadow.Budget, so the hook waits at most that long
// and never fails on it.
func (p *preShadow) record() {
	src, ok := hookSource(p.raw)
	if !ok || (len(p.facts) == 0 && !p.primaryBlocked && !p.primaryWaived) {
		return
	}
	shadow.RecordFacts(src, func() []shadow.Fact {
		facts := slices.Clone(p.facts)
		if p.primaryBlocked || p.primaryWaived {
			if root := tdd.PrimaryLanding(p.raw); root != "" {
				actual := shadow.Block
				if !p.primaryBlocked {
					actual = p.final
				}
				f := shadow.PrimaryWrite(p.tool(), actual)
				f.Root = root
				facts = append(facts, f)
			}
		}
		return facts
	})
}

// hookSource is where a hook's records are filed: the root of the file an edit
// names, else the cwd, and the actor as the other events name it. A fact about
// another checkout names its own root.
func hookSource(raw []byte) (shadow.Source, bool) {
	var in struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
		Cwd       string `json:"cwd"`
		ToolInput struct {
			FilePath string `json:"file_path"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return shadow.Source{}, false
	}
	root := in.Cwd
	if in.ToolInput.FilePath != "" {
		root = filepath.Dir(in.ToolInput.FilePath)
	}
	if root == "" {
		return shadow.Source{}, false
	}
	actor := in.SessionID
	if in.AgentID != "" {
		actor += "/" + in.AgentID
	}
	return shadow.Source{Root: root, Actor: actor}, true
}
