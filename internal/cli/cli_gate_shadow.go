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
	laws  []tdd.LawFinding // the findings already noted, one fact each

	// The primary-checkout wall's judgement of the call, read once: whether it
	// blocked or a waiver covers the call. Where a write lands is a question for the
	// record, not for the hook.
	primary tdd.PrimaryJudgement

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
	case "discard-bash":
		p.add(shadow.Discard(shadow.Block))
	case "direct-pr-open":
		p.add(shadow.Outward(d.Policy, shadow.Block))
	case "undercover":
		p.add(shadow.Attribution(shadow.Block))
	}
}

// law notes the findings of the law engine over the edit, by their own severity: a
// deny finding is the deny-law-edit rule, a warn finding the warn-law rule. The
// engine reports one finding per hit; a law that hit twenty lines is one fact of
// the call, for the kernel reads the law, not how often it matched.
func (p *preShadow) law(found []tdd.LawFinding) {
	for _, f := range found {
		if slices.Contains(p.laws, f) {
			continue
		}
		p.laws = append(p.laws, f)
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

// primaryBlocked is whether the primary-checkout wall blocked the call.
func (p *preShadow) primaryBlocked() bool { return p.primary.Decision.Action == tdd.Block }

// hasWork is whether the call yielded anything to record: a fact, a block of the
// primary wall, or a waiver whose landing is yet to be resolved.
func (p *preShadow) hasWork() bool {
	return len(p.facts) > 0 || p.primaryBlocked() || p.primary.Waived
}

// record writes the facts the call yielded, after the hook's answer is written:
// record-only and bounded by shadow.Budget, so the hook waits at most that long
// and never fails on it.
func (p *preShadow) record() {
	src, ok := hookSource(p.raw)
	steps := p.redGreen(src)
	// A call with no fact, nothing the primary wall judged and no code file to
	// write has nothing to record, and starts no goroutine.
	if !ok || (!p.hasWork() && len(steps) == 0) {
		return
	}
	blocked := p.primaryBlocked()
	shadow.RecordFactsAnd(src, func() []shadow.Fact {
		facts := slices.Clone(p.facts)
		if blocked || p.primary.Waived {
			if root := p.primary.Landing(p.raw); root != "" {
				actual := shadow.Block
				if !blocked {
					actual = p.final
				}
				f := shadow.PrimaryWrite(p.tool(), actual)
				f.Root = root
				facts = append(facts, f)
			}
		}
		return facts
	}, steps)
}

// redGreen is the red→green step of the call: asked of each code file it writes,
// when the call went ahead. Aphrollo's own decision for it is always allow, the
// proof being held at the commit, so a call a wall or a law stopped is not asked:
// the edit it was about does not happen.
func (p *preShadow) redGreen(src shadow.Source) []shadow.Step {
	if p.final == shadow.Block {
		return nil
	}
	pl, ok := shadow.ParsePayload(p.raw)
	if !ok {
		return nil
	}
	return shadow.RedGreenSteps(tdd.ShadowWorld(), src, pl, pl.Targets())
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
