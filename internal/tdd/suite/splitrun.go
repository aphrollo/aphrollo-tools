package suite

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Wrap is run as the plan carries it out. A line the plan cannot rebuild is
// run as it came. A list that fits is run as the one command it was, and its
// packages are recorded. A split list is run as its several runs
// (runSplit): the verdict is green only if every run is green.
func (p GoTestPlan) Wrap(run SuiteRunner) SuiteRunner {
	switch {
	case !p.recordable:
		return run
	case !p.Split():
		return func(r Runner, root string) SuiteResult {
			res := run(r, root)
			recordPkgSamples(p.samples(p.patterns, res, time.Now()))
			return res
		}
	}
	return func(r Runner, root string) SuiteResult { return p.runSplit(run, r, root) }
}

// runSplit carries out the plan's runs, p.parallel at a time. Each run is the
// original command over its own packages, with a deadline of the per-run
// budget from when it starts and never past the one the stage has left. A run
// that does not pass ends the starting of new ones: what is in flight finishes
// inside its own deadline, and the rest are reported as not started.
func (p GoTestPlan) runSplit(run SuiteRunner, r Runner, root string) SuiteResult {
	start := time.Now()
	overall := r.Deadline
	if overall.IsZero() {
		overall = start.Add(p.overall)
	}
	results := make([]*SuiteResult, len(p.groups))
	var next atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range max(1, p.parallel) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(p.groups) || stop.Load() {
					return
				}
				res, started := p.startRun(run, r, root, i, overall)
				if !started {
					stop.Store(true)
					return
				}
				results[i] = &res
				if !res.Passed || res.TimedOut {
					stop.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	return p.merge(results, time.Since(start))
}

// startRun runs group i and records what it showed of its packages. started is
// false when the overall deadline left no time to begin.
func (p GoTestPlan) startRun(run SuiteRunner, r Runner, root string, i int, overall time.Time) (res SuiteResult, started bool) {
	deadline := time.Now().Add(p.perRun)
	if overall.Before(deadline) {
		deadline = overall
	}
	if !deadline.After(time.Now()) {
		return SuiteResult{}, false
	}
	pkgs := names(p.groups[i])
	g := r
	g.Args = p.with(pkgs)
	g.Deadline = deadline
	res = run(g, root)
	recordPkgSamples(p.samples(pkgs, res, time.Now()))
	return res, true
}

// merge is the one result of a split list from its runs (nil: never started),
// in plan order, with wall as its duration.
//
// Green only if every run started and passed. A run that failed is the
// verdict whatever else timed out beside it: a failure is reported as the
// failure it is. Otherwise the list did not finish, and the result is the
// inconclusive one every consumer already refuses to read as a pass, with the
// note naming what did not finish and what never started.
func (p GoTestPlan) merge(results []*SuiteResult, wall time.Duration) SuiteResult {
	m := SuiteResult{Duration: wall}
	var failed, timedOut, slow bool
	var unfinished, ended, unstarted []string
	for i, res := range results {
		label := fmt.Sprintf("run %d of %d [%s]", i+1, len(results), strings.Join(names(p.groups[i]), " "))
		if res == nil {
			unstarted = append(unstarted, label)
			continue
		}
		m.Output += res.Output
		m.GoTestJSON += res.GoTestJSON
		m.Dir = res.Dir
		if m.Err == "" {
			m.Err = res.Err
		}
		switch {
		case res.TimedOut && res.Inconclusive != "":
			timedOut = true
			ended = append(ended, fmt.Sprintf("%s ended: %s", label, res.Inconclusive))
		case res.TimedOut:
			timedOut, slow = true, true
			unfinished = append(unfinished, fmt.Sprintf("%s after %.0fs", label, res.Duration.Seconds()))
			m.Output += fmt.Sprintf("aphrollo: %s did not finish within %.0fs\n", label, res.Duration.Seconds())
		case !res.Passed:
			failed = true
		}
	}
	m.Passed = !failed && !timedOut && len(unstarted) == 0
	m.TimedOut = !failed && !m.Passed
	if m.TimedOut {
		if !slow {
			m.Inconclusive = strings.Join(ended, "; ")
		}
		m.SplitNote = splitNote(len(results), unfinished, ended, unstarted)
	}
	return m
}

// splitNote words what a split list that did not finish left undone.
func splitNote(runs int, unfinished, ended, unstarted []string) string {
	var parts []string
	if len(unfinished) > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d runs did not finish: %s", len(unfinished), runs, strings.Join(unfinished, "; ")))
	}
	if len(ended) > 0 {
		parts = append(parts, strings.Join(ended, "; "))
	}
	if len(unstarted) > 0 {
		parts = append(parts, "not started: "+strings.Join(unstarted, ", "))
	}
	return strings.Join(parts, "\n")
}

// samples is what a run showed of its packages, for the record: each package
// its JSON shows passed at its own seconds, and, if the run timed out
// unfinished (not ended by the memory cap, which says nothing about cost),
// each other package it was given as cut off at the seconds the run had spent.
func (p GoTestPlan) samples(pkgs []string, res SuiteResult, at time.Time) []pkgSample {
	done := goTestPackageSecs(res.GoTestJSON)
	paths := make([]string, 0, len(done))
	for path := range done {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	out := make([]pkgSample, 0, len(paths))
	for _, path := range paths {
		out = append(out, pkgSample{At: at, Pkg: path, Race: p.race, Secs: done[path]})
	}
	if !res.TimedOut || res.Inconclusive != "" {
		return out
	}
	for _, pat := range pkgs {
		path := importPathOf(p.module, pat)
		if _, finished := done[path]; path != "" && !finished {
			out = append(out, pkgSample{At: at, Pkg: path, Race: p.race, Secs: res.Duration.Seconds(), Cut: true})
		}
	}
	return out
}
