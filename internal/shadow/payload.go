package shadow

import (
	"encoding/json"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/shell"
)

// Payload is the part of a hook's JSON input the adapters read: who is asking, from
// where, and what the call names. It is read once per hook from the bytes the hook
// already holds.
type Payload struct {
	SessionID      string `json:"session_id"`
	AgentID        string `json:"agent_id"`
	Cwd            string `json:"cwd"`
	ToolName       string `json:"tool_name"`
	StopHookActive bool   `json:"stop_hook_active"`
	ToolInput      struct {
		FilePath string `json:"file_path"`
		Command  string `json:"command"`
	} `json:"tool_input"`
}

// ParsePayload reads a hook payload; false when it is not JSON.
func ParsePayload(raw []byte) (Payload, bool) {
	var p Payload
	if json.Unmarshal(raw, &p) != nil {
		return Payload{}, false
	}
	return p, true
}

// Actor is "session" or "session/agent", the way the other events name it.
func (p Payload) Actor() string {
	if p.AgentID != "" {
		return p.SessionID + "/" + p.AgentID
	}
	return p.SessionID
}

// IsShell reports whether the call is a Bash or PowerShell command, whose
// writes are the targets its text names.
func (p Payload) IsShell() bool {
	return p.ToolName == "Bash" || p.ToolName == "PowerShell"
}

// Targets are the files the call is about to write: the file of an Edit, Write,
// MultiEdit or NotebookEdit, or the paths a shell command's text writes. The parse
// reads the command's text alone and spawns nothing.
func (p Payload) Targets() []string {
	if p.IsShell() {
		return shell.BashWriteTargets(p.ToolInput.Command, p.Cwd)
	}
	if p.ToolInput.FilePath != "" {
		return []string{p.ToolInput.FilePath}
	}
	return nil
}

// FileClassOf is the kernel's class of a file: code and test files are the ones the
// gates act on, anything else (docs, config, vendored code) moves no TDD state.
func FileClassOf(file string) kernel.FileClass {
	switch core.ClassifyFile(file) {
	case core.Source:
		return kernel.ClassCode
	case core.Test:
		return kernel.ClassTest
	}
	return kernel.ClassOther
}

// Edit is one file a call writes, with what the adapters resolved of it.
type Edit struct {
	File    string
	Class   kernel.FileClass
	Unit    Unit
	Covered bool
	Tree    string // the tree key after the write; "" before it, when only the run knows it
}

// PreEvent is the question a PreToolUse call asks of the kernel for one edit: a
// write of Edit in lane, made by an agent. The target is a lane's, never the
// primary checkout's (the primary-checkout wall is a rule of its own), and
// AddsSymbol is left false: whether an edit adds an exported symbol or a function
// is read from the text the write leaves, which the hook has not parsed.
func PreEvent(p Payload, lane string, e Edit) kernel.Event {
	tool := kernel.ToolWrite
	if p.IsShell() {
		tool = kernel.ToolBash
	}
	return kernel.Event{
		Kind: kernel.KindPreTool, Claude: true, Lane: lane, Actor: p.Actor(),
		Tool: tool, Target: kernel.PathLane, Unit: e.Unit.ID, File: e.Class, Covered: e.Covered,
	}
}

// EditEvent is the fact of an edit made: the file class and unit, the tree the
// write produced, and whether the unit is covered on it.
func EditEvent(actor, lane string, e Edit) kernel.Event {
	return kernel.Event{
		Kind: kernel.KindEdit, Claude: true, Lane: lane, Actor: actor,
		Unit: e.Unit.ID, Tree: e.Tree, File: e.Class, Covered: e.Covered,
	}
}

// RunEvent is the fact of a run that finished: its verdict and cause for the
// unit on the tree it measured. job names the run, so a retried fold is dropped.
func RunEvent(actor, lane, unit, tree, job string, v kernel.Verdict, cause string) kernel.Event {
	return kernel.Event{
		Kind: kernel.KindRunResult, Claude: true, Lane: lane, Actor: actor,
		Unit: unit, Tree: tree, Job: job, Verdict: v, Cause: cause,
	}
}

// StopEvent is the question a Stop or SubagentStop asks: may this actor stop, with
// an unseen red outstanding or not. The kernel reads the lane's units for the red.
func StopEvent(p Payload, lane string, unseenRed bool) kernel.Event {
	return kernel.Event{
		Kind: kernel.KindStop, Claude: true, Lane: lane, Actor: p.Actor(),
		UnseenRed: unseenRed, StopActive: p.StopHookActive,
	}
}

// hasWrites says whether any of files is a code or test file.
func hasWrites(files []string) bool {
	for _, f := range files {
		if strings.TrimSpace(f) != "" && FileClassOf(f) != kernel.ClassOther {
			return true
		}
	}
	return false
}
