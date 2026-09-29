package mutation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/scanner"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Settling a mutant the run could not judge, by running it (issue #910).
// Two kinds reach here, both on lines the diff adds: a NOT COVERED mutant at
// a position Go coverage never counts (mutants_covershape.go), and a
// survivor gremlins judged with the mutated package's tests alone while
// other packages' tests reach it (mutants_go_reach.go). Either is applied
// on its own — through `go test -overlay`, so the checkout is never written
// — and run in one `go test` over every package with tests that reaches its
// line. A test failure is a kill; a build failure means the mutant was never
// viable; green everywhere is a survivor. A run the budget cuts off, or one
// that cannot be set up, leaves the mutant as it was and says UNRESOLVED,
// which the judge refuses without calling it a survivor.

// resolveBudgetFn is how long the run of one mutant may take: the same
// per-mutant budget the Cargo half gives a mutant's suite
// (mutantsMinTestTimeout). A seam so a test can make it run out.
var resolveBudgetFn = func(root string) time.Duration {
	return time.Duration(mutantsMinTestTimeout(root)) * time.Second
}

// setResolveBudgetForTest pins the per-mutant budget and answers the restore.
func setResolveBudgetForTest(d time.Duration) (restore func()) {
	prev := resolveBudgetFn
	resolveBudgetFn = func(string) time.Duration { return d }
	return func() { resolveBudgetFn = prev }
}

// resolveGapMutants settles each outcome at idx, holding the box-wide
// mutation-run lock for all of them, and answers the outcomes with those
// replaced. The input slice is never written to.
func resolveGapMutants(ctx context.Context, root string, cfg MutantsConfig, reach func() (goReachGraph, error),
	outcomes []MutantOutcome, idx []int, log io.Writer) []MutantOutcome {
	out := make([]MutantOutcome, len(outcomes))
	copy(out, outcomes)
	if len(idx) == 0 {
		return out
	}
	release := acquireMutantsRunLock("mutants resolve for "+root, root)
	defer release()
	g, gerr := reach()
	env := measureEnv(root, cfg)
	budget := resolveBudgetFn(root)
	start := time.Now()
	logf(log, "mutants: settling %d mutant(s) on lines this diff adds by running each against the tests that reach it", len(idx))
	for n, i := range idx {
		m := out[i]
		if gerr != nil {
			out[i] = unresolved(m, fmt.Sprintf("the module's own package graph could not be read (%v)", gerr))
			continue
		}
		dir := goMutantPackageDir(m.File)
		stages, reaching := resolveStages(g, dir, m.Status)
		if len(reaching) == 0 {
			m.Note = "no package with tests reaches " + dir + ", so no test runs this line"
			out[i] = m
			continue
		}
		out[i] = resolveInCopy(ctx, root, env, m, stages, reaching, budget, filepath.Join(measureTempDir(root), "resolve", strconv.Itoa(n)))
		logf(log, "mutants: %s: %s", outcomeName(out[i]), out[i].Note)
	}
	logf(log, "mutants: settled %d mutant(s) in %s", len(idx), time.Since(start).Round(time.Second))
	return out
}

// resolveStages is which tests settle a mutant in dir, run by run, and
// every package with tests that reaches it. A not-covered mutant runs its
// own package's tests first, alone, since those kill most mutants and finish
// long before a wide run does; then every other package that reaches it. An
// inconclusive survivor skips its own package, whose tests gremlins already
// ran over it without a kill.
func resolveStages(g goReachGraph, dir, status string) (stages [][]string, reaching []string) {
	others := testedPackagesReaching(g, dir)
	if !g.Tested[dir] {
		return [][]string{others}, others
	}
	reaching = append([]string{dir}, others...)
	if status == gremlinsScopeUnknown {
		return [][]string{others}, reaching
	}
	return [][]string{{dir}, others}, reaching
}

// resolveInCopy settles m in a disposable copy of the checkout at root, one
// copy per mutant. The mutated tests run wherever `go test` stands, and a
// mutant that skips a guard can write, delete or reset there (#972), so
// they never run in the checkout itself, and a mutant that wrecks its copy
// wrecks nothing the next one runs in. The copy is removed on every exit.
func resolveInCopy(ctx context.Context, root string, env []string, m MutantOutcome, stages [][]string, reaching []string,
	budget time.Duration, work string) MutantOutcome {
	lane := RepoRoot(root)
	if lane == "" {
		return unresolved(m, root+" is not inside a git repository to copy")
	}
	box, err := newProveSandbox(lane, root)
	if err != nil {
		return unresolved(m, fmt.Sprintf("a disposable copy of %s to run in could not be made (%v)", lane, err))
	}
	defer box.remove()
	defer watchProveSignals(box.remove, io.Discard)()
	boxRoot, err := box.path(lane, root)
	if err != nil {
		return unresolved(m, err.Error())
	}
	return resolveOne(ctx, boxRoot, env, m, stages, reaching, budget, work)
}

// resolveOne applies m in an overlay under work and runs each stage's tests
// over it, stopping at the first that settles it. reaching is every package
// with tests that reaches m's line, which a survivor's note names.
func resolveOne(ctx context.Context, root string, env []string, m MutantOutcome, stages [][]string, reaching []string,
	budget time.Duration, work string) MutantOutcome {
	path := filepath.Join(root, filepath.FromSlash(m.File))
	src, err := os.ReadFile(path)
	if err != nil {
		return unresolved(m, fmt.Sprintf("the source could not be read (%v)", err))
	}
	mutated, err := mutateAt(src, m.Line, m.Col, m.Mutation)
	if err != nil {
		return unresolved(m, err.Error())
	}
	overlay, err := writeOverlay(work, path, mutated)
	if err != nil {
		return unresolved(m, fmt.Sprintf("the overlay could not be written (%v)", err))
	}
	for _, pkgs := range stages {
		args := packageArgs(pkgs)
		verdict, detail := runResolveTests(ctx, root, env, overlay, args, budget)
		switch verdict {
		case resolveKilled:
			if why := failsWithoutTheMutant(ctx, root, env, killerArgs(detail, args), budget); why != "" {
				return unresolved(m, why)
			}
			m.Status = "caught"
			m.Note = "killed by the tests of " + detail + " (settled by running this one mutant)"
			return m
		case resolveUnviable:
			m.Status = "unviable"
			m.Note = "does not compile, so no test can run it (settled by running this one mutant)"
			return m
		case resolveCutOff:
			return unresolved(m, detail)
		}
	}
	m.Status = "missed"
	m.Note = "survived the tests of " + strings.Join(reaching, ", ") + ", every package with tests that reaches this line"
	return m
}

// unresolved leaves m's status as the run found it, saying why it stayed so.
func unresolved(m MutantOutcome, why string) MutantOutcome {
	m.Note = "UNRESOLVED: " + why
	return m
}

// resolveVerdict is what one package's run of one mutant showed.
type resolveVerdict int

const (
	resolveSurvived resolveVerdict = iota
	resolveKilled
	resolveUnviable
	resolveCutOff
)

// resolveExecFn runs one `go test`: the measurement's own spawn, which kills
// the whole process tree when its context ends.
var resolveExecFn = runMutantsTool

// packageArgs names package directories the way `go test` takes them.
func packageArgs(dirs []string) []string {
	args := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		args = append(args, "./"+dir)
	}
	return args
}

// packageNames names the packages args runs, as a note says them: a
// directory without its "./", an import path as it is.
func packageNames(args []string) string {
	names := make([]string, 0, len(args))
	for _, a := range args {
		names = append(names, strings.TrimPrefix(a, "./"))
	}
	return strings.Join(names, ", ")
}

// killerArgs is the packages a kill run names as failed (their import
// paths, which `go test` takes as they are), or the whole stage when the run
// named none.
func killerArgs(detail string, stage []string) []string {
	if detail == "" {
		return stage
	}
	return strings.Split(detail, ", ")
}

// failsWithoutTheMutant runs the packages whose tests failed under the
// mutant again with no overlay, and says why their failure is not shown to
// be its kill: they fail on the unmutated code too, or that run did not
// finish. "" confirms the kill (issue #957). A test that fails whatever the
// code — a flake, or one that depends on the checkout it runs in — was
// credited as a kill, and the same diff then passed in one checkout and was
// refused in another.
func failsWithoutTheMutant(ctx context.Context, root string, env, args []string, budget time.Duration) string {
	verdict, detail := runResolveTests(ctx, root, env, "", args, budget)
	switch verdict {
	case resolveSurvived:
		return ""
	case resolveCutOff:
		return "the tests of " + packageNames(args) + " failed under the mutant, and without it " + detail +
			", so the failure is not shown to be its kill"
	}
	return "the tests of " + packageNames(args) + " fail without the mutant too, so their failure is not its kill"
}

// runResolveTests runs the tests of the packages args names over the overlay
// ("" runs the code as it is) in one `go test`, which builds and runs the
// packages side by side, within budget. detail names the packages that
// failed for a kill, and why for a cut-off run.
func runResolveTests(ctx context.Context, root string, env []string, overlay string, args []string, budget time.Duration) (resolveVerdict, string) {
	if len(args) == 0 {
		// Nothing in this stage; `go test` with no package would test the
		// module root instead.
		return resolveSurvived, ""
	}
	runCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var out bytes.Buffer
	argv := []string{"go", "test", "-count=1", "-failfast"}
	if overlay != "" {
		argv = append(argv, "-overlay", overlay)
	}
	argv = append(argv, args...)
	code, err := resolveExecFn(runCtx, root, env, argv, &out)
	switch {
	case runCtx.Err() != nil:
		return resolveCutOff, fmt.Sprintf("the tests of %s did not finish within %s", packageNames(args), budget)
	case err != nil:
		return resolveCutOff, fmt.Sprintf("the tests of %s could not start (%v)", packageNames(args), err)
	case code == 0:
		return resolveSurvived, ""
	case strings.Contains(out.String(), "[build failed]") || strings.Contains(out.String(), "[setup failed]"):
		return resolveUnviable, ""
	}
	return resolveKilled, failedPackages(out.String())
}

// failedPackages names the packages whose `FAIL\t<import path>` lines the
// run printed: go test prints one for every package a failure, a panic or
// a timeout ends.
func failedPackages(output string) string {
	var failed []string
	for _, l := range strings.Split(output, "\n") {
		if rest, ok := strings.CutPrefix(l, "FAIL\t"); ok {
			failed = append(failed, strings.Fields(rest)[0])
		}
	}
	return strings.Join(failed, ", ")
}

// writeOverlay writes the mutated source and the -overlay file that swaps it
// in for path, both under work, and answers the overlay file's path.
func writeOverlay(work, path string, mutated []byte) (string, error) {
	if err := os.MkdirAll(work, 0o755); err != nil {
		return "", err
	}
	mutant := filepath.Join(work, filepath.Base(path))
	if err := os.WriteFile(mutant, mutated, 0o600); err != nil {
		return "", err
	}
	data, err := json.Marshal(map[string]map[string]string{"Replace": {path: mutant}})
	if err != nil {
		return "", err
	}
	overlay := filepath.Join(work, "overlay.json")
	return overlay, os.WriteFile(overlay, data, 0o600)
}

// gremlinsTokenMutations is gremlins v0.6.0's own mapping table: for each
// mutator, which operator token becomes which.
var gremlinsTokenMutations = map[string]map[token.Token]token.Token{
	"ARITHMETIC_BASE": {token.ADD: token.SUB, token.MUL: token.QUO, token.QUO: token.MUL,
		token.REM: token.MUL, token.SUB: token.ADD},
	"CONDITIONALS_BOUNDARY": {token.GEQ: token.GTR, token.GTR: token.GEQ, token.LEQ: token.LSS,
		token.LSS: token.LEQ},
	"CONDITIONALS_NEGATION": {token.EQL: token.NEQ, token.GEQ: token.LSS, token.GTR: token.LEQ,
		token.LEQ: token.GTR, token.LSS: token.GEQ, token.NEQ: token.EQL},
	"INCREMENT_DECREMENT": {token.DEC: token.INC, token.INC: token.DEC},
	"INVERT_ASSIGNMENTS": {token.ADD_ASSIGN: token.SUB_ASSIGN, token.MUL_ASSIGN: token.QUO_ASSIGN,
		token.QUO_ASSIGN: token.MUL_ASSIGN, token.REM_ASSIGN: token.REM_ASSIGN, token.SUB_ASSIGN: token.ADD_ASSIGN},
	"INVERT_BITWISE": {token.AND: token.OR, token.OR: token.AND, token.XOR: token.AND,
		token.AND_NOT: token.AND, token.SHL: token.SHR, token.SHR: token.SHL},
	"INVERT_BWASSIGN": {token.AND_ASSIGN: token.OR_ASSIGN, token.OR_ASSIGN: token.AND_ASSIGN,
		token.XOR_ASSIGN: token.AND_ASSIGN, token.AND_NOT_ASSIGN: token.AND_ASSIGN,
		token.SHL_ASSIGN: token.SHR_ASSIGN, token.SHR_ASSIGN: token.SHL_ASSIGN},
	"INVERT_LOGICAL":   {token.LAND: token.LOR, token.LOR: token.LAND},
	"INVERT_LOOPCTRL":  {token.BREAK: token.CONTINUE, token.CONTINUE: token.BREAK},
	"INVERT_NEGATIVES": {token.SUB: token.ADD},
	"REMOVE_SELF_ASSIGNMENTS": {token.ADD_ASSIGN: token.ASSIGN, token.AND_ASSIGN: token.ASSIGN,
		token.AND_NOT_ASSIGN: token.ASSIGN, token.MUL_ASSIGN: token.ASSIGN, token.OR_ASSIGN: token.ASSIGN,
		token.QUO_ASSIGN: token.ASSIGN, token.REM_ASSIGN: token.ASSIGN, token.SHL_ASSIGN: token.ASSIGN,
		token.SHR_ASSIGN: token.ASSIGN, token.SUB_ASSIGN: token.ASSIGN, token.XOR_ASSIGN: token.ASSIGN},
}

// mutateAt applies the gremlins mutator to the operator token at line:col
// and answers the mutated source. It refuses rather than guesses: a position
// holding no token, or one the mutator does not rewrite, is an error.
func mutateAt(src []byte, line, col int, mutator string) ([]byte, error) {
	table, ok := gremlinsTokenMutations[mutator]
	if !ok {
		return nil, fmt.Errorf("mutator %s is not one this gate can apply", mutator)
	}
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, 0)
	for {
		pos, tok, _ := s.Scan()
		if tok == token.EOF {
			return nil, fmt.Errorf("no token starts at %d:%d", line, col)
		}
		p := fset.Position(pos)
		if p.Line != line || p.Column != col {
			continue
		}
		to, ok := table[tok]
		if !ok {
			return nil, fmt.Errorf("%s does not rewrite the %q at %d:%d", mutator, tok, line, col)
		}
		return slices.Concat(src[:p.Offset], []byte(to.String()), src[p.Offset+len(tok.String()):]), nil
	}
}
