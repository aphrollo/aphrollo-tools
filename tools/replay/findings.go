package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// result is what one run of a binary left behind.
type result struct {
	Exit   int
	Stdout string
	Stderr string
}

// finding is one thing a read-only command reports: the leg it came from, the
// file it sits in and what it says. Two binaries reading the same tree are
// compared on this identity, never on a line number, so a hit that moved is the
// same hit.
type finding struct{ Leg, File, What string }

// hits is how many times each finding was reported.
type hits map[finding]int

func (h hits) total() int {
	n := 0
	for _, c := range h {
		n += c
	}
	return n
}

// sane refuses what no verdict can be read from: an exit code outside the
// accepted ones, and a crash, which can sit beside a clean exit code when a
// panic is recovered into one.
func sane(command string, res result, exits ...int) error {
	if !slices.Contains(exits, res.Exit) {
		return fmt.Errorf("%s exit %d, want one of %v: %s", command, res.Exit, exits, firstLines(res.Stderr+res.Stdout, 4))
	}
	for _, marker := range []string{"panic: ", "fatal error: ", "\ngoroutine "} {
		if strings.Contains(res.Stderr, marker) {
			return fmt.Errorf("%s crashed (panic): %s", command, firstLines(res.Stderr, 4))
		}
	}
	return nil
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "...")
	}
	return strings.Join(lines, " | ")
}

func slash(s string) string { return strings.ReplaceAll(s, `\`, "/") }

// parseRatchet reads `ratchet check --format json`. A finding is as many hits
// as it stands above its baseline, and at least one.
func parseRatchet(res result) (hits, error) {
	if err := sane("ratchet check", res, 0, 1); err != nil {
		return nil, err
	}
	var doc struct {
		Findings []struct {
			Law      string `json:"law"`
			File     string `json:"file"`
			Key      string `json:"key"`
			Baseline int    `json:"baseline"`
			Measured int    `json:"measured"`
			Excess   int    `json:"excess"`
		} `json:"findings"`
		Skipped []struct {
			Law  string `json:"law"`
			Kind string `json:"kind"`
		} `json:"skipped_laws"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		return nil, fmt.Errorf("ratchet check printed no verdict (%v): %s", err, firstLines(res.Stderr, 4))
	}
	h := hits{}
	for _, f := range doc.Findings {
		n := f.Excess
		if n <= 0 {
			n = max(f.Measured-f.Baseline, 1)
		}
		h[finding{"ratchet", slash(f.File), f.Law + ": " + f.Key}] += n
	}
	for _, s := range doc.Skipped {
		h[finding{"ratchet", "", fmt.Sprintf("law not judged: %s (%s)", s.Law, s.Kind)}]++
	}
	return h, nil
}

var docsMiss = regexp.MustCompile(`^(.+?):\d+: unresolved reference: (.+)$`)

// parseDocs reads `docs check`. A line it does not know is kept as a finding of
// its own, so a reworded message shows in the report instead of vanishing.
func parseDocs(res result) (hits, error) {
	if err := sane("docs check", res, 0, 1); err != nil {
		return nil, err
	}
	h := hits{}
	for line := range strings.SplitSeq(res.Stdout+"\n"+res.Stderr, "\n") {
		line = strings.TrimSpace(line)
		switch m := docsMiss.FindStringSubmatch(line); {
		case line == "":
		case m != nil:
			h[finding{"docs", slash(m[1]), m[2]}]++
		default:
			h[finding{"docs", "", line}]++
		}
	}
	return h, nil
}

// replayNew is what the gate leg puts in front of a file's name: the gate judges
// a Write to a path that does not exist as a file made new, every line of it
// added, which is what a fixed file set has to be judged as.
const replayNew = "replaynew_"

// parseGate reads one `gate pretooluse` answer for the file at rel: nothing is
// an allow, exit 2 a deny, and advice on exit 0 a warning. Paths in the reason
// are made relative to tree so two hosts, and two checkouts, say the same thing.
func parseGate(rel, tree string, res result) (hits, error) {
	if err := sane("gate pretooluse", res, 0, 2); err != nil {
		return nil, err
	}
	if res.Exit == 0 && strings.TrimSpace(res.Stdout) == "" {
		return hits{}, nil
	}
	var doc struct {
		Out struct {
			Deny    string `json:"permissionDecisionReason"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		return nil, fmt.Errorf("gate pretooluse printed no verdict (%v): %s", err, firstLines(res.Stdout, 2))
	}
	kind, reason := "deny", doc.Out.Deny
	if res.Exit == 0 {
		kind, reason = "warn", doc.Out.Context
	}
	if reason == "" {
		return nil, fmt.Errorf("gate pretooluse exit %d printed no verdict: %s", res.Exit, firstLines(res.Stdout, 2))
	}
	reason = strings.ReplaceAll(strings.ReplaceAll(slash(reason), slash(tree), "<tree>"), replayNew, "")
	return hits{{"gate", rel, kind + ": " + reason}: 1}, nil
}

// delta is how many more (or fewer) times a finding was reported.
type delta struct {
	finding
	N int
}

func (d delta) String() string {
	var parts []string
	for _, p := range []string{d.Leg, d.File, d.What} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return fmt.Sprintf("%s (x%d)", strings.Join(parts, " "), d.N)
}

// compare splits two readings of one tree into what the candidate reports more
// of than the previous release (added) and less of (dropped), each sorted.
func compare(prev, cand hits) (added, dropped []delta) {
	for f, n := range cand {
		if d := n - prev[f]; d > 0 {
			added = append(added, delta{f, d})
		}
	}
	for f, n := range prev {
		if d := n - cand[f]; d > 0 {
			dropped = append(dropped, delta{f, d})
		}
	}
	order := func(a, b delta) int {
		return cmp.Or(cmp.Compare(a.Leg, b.Leg), cmp.Compare(a.File, b.File), cmp.Compare(a.What, b.What))
	}
	slices.SortFunc(added, order)
	slices.SortFunc(dropped, order)
	return added, dropped
}

// legOut is one binary's reading of one leg of one tree.
type legOut struct {
	Hits hits
	Err  error
}

// legReport is the verdict on one leg of one tree.
type legReport struct {
	Tree, Leg            string
	Added, Dropped       []delta
	Failure, Note        string
	PrevTotal, CandTotal int
}

func (l legReport) failed() bool { return len(l.Added) > 0 || l.Failure != "" }

// settle judges one leg. The candidate failing to read a tree fails the leg. The
// previous release failing to read one is a failure only where it has to read it
// (mustRead: a tree of its own making, holding the state the candidate wrote);
// on a tree that may use what the candidate adds it is a note, and the leg is
// not compared.
func settle(tree, leg string, mustRead bool, prev, cand legOut) legReport {
	rep := legReport{Tree: tree, Leg: leg}
	switch {
	case cand.Err != nil:
		rep.Failure = "the candidate cannot read the tree: " + cand.Err.Error()
	case prev.Err != nil && mustRead:
		rep.Failure = "the previous release cannot read the tree or the state the candidate wrote: " + prev.Err.Error()
	case prev.Err != nil:
		rep.Note = "the previous release cannot read this tree, not compared: " + prev.Err.Error()
	default:
		rep.Added, rep.Dropped = compare(prev.Hits, cand.Hits)
		rep.PrevTotal, rep.CandTotal = prev.Hits.total(), cand.Hits.total()
	}
	return rep
}

// maxShown caps the findings one leg prints.
const maxShown = 40

// render is the report: one block per leg, the differing findings named.
func render(reps []legReport) string {
	var b strings.Builder
	for _, r := range reps {
		counts := fmt.Sprintf("(previous %d, candidate %d)", r.PrevTotal, r.CandTotal)
		switch {
		case r.Failure != "":
			fmt.Fprintf(&b, "FAIL %s %s: %s\n", r.Tree, r.Leg, r.Failure)
		case len(r.Added) > 0:
			fmt.Fprintf(&b, "FAIL %s %s: the candidate adds %d hit(s) %s\n", r.Tree, r.Leg, len(r.Added), counts)
			for _, d := range r.Added[:min(len(r.Added), maxShown)] {
				fmt.Fprintf(&b, "  + %s\n", d)
			}
			if len(r.Added) > maxShown {
				fmt.Fprintf(&b, "  ... and %d more\n", len(r.Added)-maxShown)
			}
		default:
			fmt.Fprintf(&b, "ok   %s %s %s", r.Tree, r.Leg, counts)
			if len(r.Dropped) > 0 {
				fmt.Fprintf(&b, ", the candidate drops %d", len(r.Dropped))
			}
			b.WriteString("\n")
		}
		if r.Note != "" {
			fmt.Fprintf(&b, "  note: %s\n", r.Note)
		}
	}
	return b.String()
}
