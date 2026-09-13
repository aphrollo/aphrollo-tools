package tdd

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MutantOutcome is one mutant and what happened to it. Both runners report
// into this one shape, so a judgement cannot depend on which tool measured.
//
// It used to carry the source blob, the package fence, the producer version
// and the invocation version as well — everything a stored outcome needed to
// say whether it was still true when it was read back weeks later. Nothing
// reads one back any more: a lane is measured on the tree that is about to
// land, in the same event that judges it, so the only outcomes that exist are
// this run's own.
type MutantOutcome struct {
	File string `json:"file"`
	Line int    `json:"line"`
	// Col is part of the identity, not decoration: cargo-mutants emits
	// several distinct mutants on one line with identical text, and
	// crates/editor_client/src/creator.rs:101:33 and :101:16 in a real run
	// are two different `replace || with && in send_undo_redo`.
	Col      int    `json:"col,omitempty"`
	Mutation string `json:"mutation"`
	// Name is the tool's own spelling of the mutant, kept verbatim because it
	// is what a re-run's own name filter has to match.
	Name string `json:"name,omitempty"`
	// Package is the crate/package whose test set constrains this mutant.
	Package string `json:"package,omitempty"`
	// Status is the producer's own word for the result ("caught", "missed",
	// "timeout", "unviable"). This package invents exactly one,
	// gremlinsScopeUnknown, and only about the RUN rather than the mutant:
	// a selection that could not have observed a killing test is a fact the
	// producer does not report and cannot be read off its verdict.
	Status string `json:"status,omitempty"`
	// Note is why an outcome could not be judged, written where that is
	// known — the reach classification (mutants_go_reach.go) — and printed
	// verbatim by the report, because "inconclusive" with no cause leaves
	// the reader nothing to act on.
	Note string `json:"note,omitempty"`
}

// mutantLineRe reads a mutant named as one line. The real shape, from a
// cargo-mutants 27.1.0 run, is "<file>:<line>:<col>: <mutation>":
//
//	crates/editor_client/src/creator.rs:101:33: replace || with && in send_undo_redo
//
// The column is captured because it is part of the mutant's IDENTITY — the
// same file names two `replace || with &&` mutants on line 101, at columns 33
// and 16 — and the whole line is kept verbatim because that string, not a
// rebuilt one, is what a re-run's name filter has to match.
var mutantLineRe = regexp.MustCompile(`^(.+?):(\d+):(\d+): (.+)$`)

// parseMutantLine reads one mutant out of the tool's own one-line spelling.
func parseMutantLine(line string) (MutantOutcome, bool) {
	m := mutantLineRe.FindStringSubmatch(line)
	if m == nil {
		return MutantOutcome{}, false
	}
	lineNo, lineErr := strconv.Atoi(m[2])
	col, colErr := strconv.Atoi(m[3])
	if lineErr != nil || colErr != nil {
		return MutantOutcome{}, false
	}
	return MutantOutcome{
		File:     filepath.ToSlash(m[1]),
		Line:     lineNo,
		Col:      col,
		Mutation: strings.TrimSpace(m[4]),
		Name:     line,
	}, true
}

// mutantLineOf is the tool's own spelling of a mutant, for a producer that
// reports its parts rather than a line.
func mutantLineOf(file string, line, col int, mutation string) string {
	return fmt.Sprintf("%s:%d:%d: %s", file, line, col, mutation)
}

// sortOutcomes puts a run's outcomes in one order whatever the tool did, so
// two runs of the same tree produce the same report.
func sortOutcomes(out []MutantOutcome) {
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Col != b.Col {
			return a.Col < b.Col
		}
		return a.Mutation < b.Mutation
	})
}

// cargo-mutants writes every verdict twice: once as log text a human reads
// and once as mutants.out/outcomes.json. Only the second is parsed here. The
// text form has changed shape between releases and drops the column that is
// part of a mutant's identity, and a gate that reads its verdict out of a
// progress line is one release away from reading "0 missed" off a log it no
// longer understands.

// cargoMutantsOutcomes is the outcomes.json shape, as written by
// cargo-mutants 27.1.0. Only the fields this side judges on are named: the
// scenario (Baseline or one Mutant), and the summary word.
type cargoMutantsOutcomes struct {
	Outcomes []struct {
		Scenario json.RawMessage `json:"scenario"`
		Summary  string          `json:"summary"`
	} `json:"outcomes"`
}

// cargoMutantsScenario is the Mutant half of a scenario. The Baseline
// scenario serialises as the bare string "Baseline", so a scenario that does
// not decode into this shape is simply not a mutant.
type cargoMutantsScenario struct {
	Mutant *struct {
		Name    string `json:"name"`
		Package string `json:"package"`
		File    string `json:"file"`
		Span    struct {
			Start struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"start"`
		} `json:"span"`
	} `json:"Mutant"`
}

// cargoMutantsStatus maps cargo-mutants' SummaryOutcome onto the vocabulary
// every judgement in this package already speaks. Anything unrecognised is
// unviable, never caught: a gate that reads an unknown status as a pass is
// not a gate.
func cargoMutantsStatus(summary string) string {
	switch summary {
	case "CaughtMutant":
		return "caught"
	case "MissedMutant":
		return "missed"
	case "Timeout":
		return "timeout"
	default:
		return "unviable"
	}
}

// parseCargoMutantsOutcomes reads one run's mutants out of its outcomes file.
// An error means the run reached no readable verdict at all — never an empty
// list, which a caller would be entitled to read as "nothing survived".
func parseCargoMutantsOutcomes(data []byte) ([]MutantOutcome, error) {
	var file cargoMutantsOutcomes
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.Outcomes == nil {
		return nil, errors.New("no outcomes array: this is not a cargo-mutants outcomes.json")
	}
	var out []MutantOutcome
	for _, o := range file.Outcomes {
		var scenario cargoMutantsScenario
		if err := json.Unmarshal(o.Scenario, &scenario); err != nil || scenario.Mutant == nil {
			// The Baseline scenario, or a scenario shape this release does
			// not know. Neither is a mutant, and neither is an error: the
			// baseline is in every file.
			continue
		}
		m := scenario.Mutant
		outcome := MutantOutcome{
			File:     filepath.ToSlash(m.File),
			Line:     m.Span.Start.Line,
			Col:      m.Span.Start.Column,
			Name:     m.Name,
			Package:  m.Package,
			Status:   cargoMutantsStatus(o.Summary),
			Mutation: mutationTextOf(m.Name),
		}
		out = append(out, outcome)
	}
	sortOutcomes(out)
	return out, nil
}

// mutationTextOf strips the "<file>:<line>:<col>: " prefix off cargo-mutants'
// own name for a mutant, leaving the mutation itself — the half the
// accept-list is keyed on beside the location (survivorKey).
func mutationTextOf(name string) string {
	if m, ok := parseMutantLine(name); ok {
		return m.Mutation
	}
	return strings.TrimSpace(name)
}
