package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// preShadow gathers what one PreToolUse judgement observed, as facts for the
// shadow record: the rules the kernel decides from the call's own facts, and
// what the gate did about each. It decides nothing and the gate never reads it
// back. The nil value is a collector that keeps nothing.
type preShadow struct {
	bash  bool
	facts []shadow.Fact
}

func newPreShadow(raw []byte) *preShadow { return &preShadow{bash: tdd.IsBashHook(raw)} }

func (p *preShadow) add(f shadow.Fact) {
	if p != nil {
		p.facts = append(p.facts, f)
	}
}

func (p *preShadow) tool() kernel.Tool {
	if p.bash {
		return kernel.ToolBash
	}
	return kernel.ToolWrite
}

// wall notes the wall that blocked the call, by the policy it counts under.
func (p *preShadow) wall(d tdd.Decision) {
	if p == nil || d.Action != tdd.Block {
		return
	}
	switch d.Policy {
	case "primary-checkout":
		p.add(shadow.PrimaryWrite(p.tool(), shadow.Block))
	case "discard-bash":
		p.add(shadow.Discard(shadow.Block))
	case "direct-pr-open":
		p.add(shadow.Outward(d.Policy, shadow.Block))
	case "undercover":
		p.add(shadow.Attribution(shadow.Block))
	}
}

// primaryWaived notes a write into the primary checkout that a waiver let by.
func (p *preShadow) primaryWaived(raw []byte) {
	if p != nil && tdd.PrimaryWaivedLanding(raw) {
		p.add(shadow.PrimaryWrite(p.tool(), shadow.Allow))
	}
}

// law notes what the law engine said of the edit.
func (p *preShadow) law(r tdd.Decision) {
	law, ok := strings.CutPrefix(r.Policy, "ratchet:")
	if p == nil || !ok || r.Action == tdd.Allow {
		return
	}
	p.add(shadow.Law(law, r.Action == tdd.Block, actionOf(r)))
}

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
// record-only, bounded by shadow.Budget, never an error to the hook.
func (p *preShadow) record(raw []byte) {
	if p == nil || len(p.facts) == 0 {
		return
	}
	src, ok := hookSource(raw)
	if !ok {
		return
	}
	shadow.RecordFacts(src, p.facts)
}

// hookSource is where a hook's records are filed: the root of the file an edit
// names, else the cwd, and the actor as the other events name it.
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
