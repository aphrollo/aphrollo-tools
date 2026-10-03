package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// bin is one build of aphrollo under comparison.
type bin struct{ Label, Path string }

// runFunc runs a binary in dir with stdin and answers what it printed. An error
// is a process that could not be run at all; a non-zero exit is an answer.
type runFunc func(b bin, dir, stdin string, args ...string) (result, error)

// tree is a tree both binaries read: a directory under git, and the fixed set of
// files the gate leg judges. PrevMustRead is set on a tree the previous release
// made itself (laws and all), which it has to read to the end.
type tree struct {
	Name         string
	Dir          string
	Files        []string
	Store        string // the state store both binaries share
	PrevMustRead bool
}

// leg is one read-only command, run by both binaries with the same arguments.
type leg struct {
	Name string
	Read func(run runFunc, b bin, t tree) legOut
}

// A leg's reading is cold unless it is the warm one: the binary's scan cache is
// derived data, and the previous release must recompute what the candidate
// cached, never trust it.
var legs = []leg{
	{"ratchet", ratchetLeg("--no-cache")},
	{"docs", func(run runFunc, b bin, t tree) legOut {
		res, err := run(b, t.Dir, "", "docs", "check")
		if err != nil {
			return legOut{Err: err}
		}
		h, err := parseDocs(res)
		return legOut{h, err}
	}},
	{"gate", gateLeg},
	{"ratchet (warm)", ratchetLeg()},
	{"store", storeLeg},
}

func ratchetLeg(extra ...string) func(runFunc, bin, tree) legOut {
	return func(run runFunc, b bin, t tree) legOut {
		args := append([]string{"ratchet", "check", "--dry"}, extra...)
		res, err := run(b, t.Dir, "", append(args, "--format", "json")...)
		if err != nil {
			return legOut{Err: err}
		}
		h, err := parseRatchet(res)
		return legOut{h, err}
	}
}

// gateLeg has the gate judge each file of the fixed set as a Write of a new file
// holding the file's bytes: the edit-time law and smell verdict over every line.
// It also writes to the gate's state store, which the previous release then reads.
func gateLeg(run runFunc, b bin, t tree) legOut {
	all := hits{}
	for _, rel := range t.Files {
		data, err := os.ReadFile(filepath.Join(t.Dir, filepath.FromSlash(rel)))
		if err != nil {
			return legOut{Err: err}
		}
		res, err := run(b, t.Dir, string(gatePayload(b.Label, t.Dir, rel, string(data))), "gate", "pretooluse")
		if err != nil {
			return legOut{Err: err}
		}
		h, err := parseGate(rel, t.Dir, res)
		if err != nil {
			return legOut{Err: fmt.Errorf("%s: %w", rel, err)}
		}
		for f, n := range h {
			all[f] += n
		}
	}
	return legOut{Hits: all}
}

// gatePayload is the hook payload of a Write of content to a new file beside
// rel. Each binary has a session of its own: the gate shows some notes once per
// session, and one session shared would show them to the binary that reads first
// and not to the one that reads second, though neither differs.
func gatePayload(session, dir, rel, content string) []byte {
	target := filepath.Join(dir, filepath.Dir(filepath.FromSlash(rel)), replayNew+filepath.Base(rel))
	payload, _ := json.Marshal(map[string]any{
		"session_id":      "replay-" + session,
		"cwd":             dir,
		"hook_event_name": "PreToolUse",
		"tool_name":       "Write",
		"tool_input":      map[string]any{"file_path": target, "content": content},
	})
	return payload
}

// storeLeg has the binary read what the gate wrote to the store: `gate status`
// always, and `gate stats` once the store holds a gate.log (it is an error to ask
// for the stats of a store with none). Each must answer without failing. What
// they say carries times and counts, so only that they can say it is compared.
func storeLeg(run runFunc, b bin, t tree) legOut {
	cmds := [][]string{{"gate", "status"}}
	if _, err := os.Stat(filepath.Join(t.Store, "claude", "gate-state", "gate.log")); err == nil {
		cmds = append(cmds, []string{"gate", "stats", "--since", "7d"})
	}
	for _, args := range cmds {
		res, err := run(b, t.Dir, "", args...)
		if err == nil {
			err = sane(strings.Join(args, " "), res, 0)
		}
		if err != nil {
			return legOut{Err: err}
		}
	}
	return legOut{Hits: hits{}}
}

// compareTree reads a tree with the candidate, then with the previous release,
// on one store: what the previous release reads is what the candidate wrote.
func compareTree(run runFunc, prev, cand bin, t tree) []legReport {
	var reps []legReport
	candOut, prevOut := make([]legOut, len(legs)), make([]legOut, len(legs))
	for i, l := range legs {
		candOut[i] = l.Read(run, cand, t)
	}
	for i, l := range legs {
		prevOut[i] = l.Read(run, prev, t)
	}
	for i, l := range legs {
		reps = append(reps, settle(t.Name, l.Name, t.PrevMustRead, prevOut[i], candOut[i]))
	}
	return reps
}
