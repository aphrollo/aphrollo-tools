package tdd

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

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
