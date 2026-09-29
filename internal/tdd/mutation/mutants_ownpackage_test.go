package mutation

import (
	"context"
	"io"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// fanOutFixturesCfg lists the two fixture packages (torque, gate) whose
// killing tests live in the packages that import them, so a test about
// settling against importers states that the repo declared them integration
// packages.
var fanOutFixturesCfg = MutantsConfig{AtMerge: true, IntegrationPackages: []string{"torque", "gate"}}

// The own-package rule (issue #1015): a mutant on a line the diff adds that
// its own package's tests miss is refused at once. Only a package the repo
// lists in mutants-integration-packages keeps being settled against the
// tests of the packages above it, nearest first.

// chainModule is leaf <- near <- far, each with a test. leaf's own test
// checks nothing, near's kills a change to leaf's boundary, and far's would
// too.
func chainModule(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, filepath.FromSlash("leaf/leaf.go"),
		"package leaf\n\nfunc Kind(n int) string {\n\tswitch {\n\tcase n > 10:\n\t\treturn \"big\"\n\t}\n\treturn \"small\"\n}\n")
	write(t, root, filepath.FromSlash("leaf/leaf_test.go"),
		"package leaf\n\nimport \"testing\"\n\nfunc TestKindRuns(t *testing.T) {\n\t_ = Kind(1)\n}\n")
	write(t, root, filepath.FromSlash("near/near.go"),
		"package near\n\nimport \"example.com/m/leaf\"\n\nfunc Label(n int) string {\n\treturn leaf.Kind(n)\n}\n")
	write(t, root, filepath.FromSlash("near/near_test.go"),
		"package near\n\nimport \"testing\"\n\nfunc TestLabelSplitsAtTen(t *testing.T) {\n"+
			"\tif Label(10) != \"small\" || Label(11) != \"big\" {\n\t\tt.Fatal(\"wrong label\")\n\t}\n}\n")
	write(t, root, filepath.FromSlash("far/far.go"),
		"package far\n\nimport \"example.com/m/near\"\n\nfunc Name(n int) string {\n\treturn near.Label(n)\n}\n")
	write(t, root, filepath.FromSlash("far/far_test.go"),
		"package far\n\nimport \"testing\"\n\nfunc TestNameSplitsAtTen(t *testing.T) {\n"+
			"\tif Name(10) != \"small\" {\n\t\tt.Fatal(\"wrong name\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root
}

// leafBoundary is the `>` of leaf's case condition, as gremlins would report
// it after its own tests missed it.
func leafBoundary(status string) MutantOutcome {
	return MutantOutcome{File: "leaf/leaf.go", Line: 5, Col: 9, Mutation: "CONDITIONALS_BOUNDARY",
		Name: "leaf/leaf.go:5:9: CONDITIONALS_BOUNDARY", Status: status, NewLine: true}
}

// recordSettleRuns records every package a settle run names, in order.
func recordSettleRuns(t *testing.T) *[]string {
	t.Helper()
	var mu sync.Mutex
	var ran []string
	prev := resolveExecFn
	resolveExecFn = func(ctx context.Context, dir string, env, argv []string, log io.Writer) (int, error) {
		mu.Lock()
		for _, a := range argv {
			if strings.HasPrefix(a, "./") {
				ran = append(ran, a)
			}
		}
		mu.Unlock()
		return prev(ctx, dir, env, argv, log)
	}
	t.Cleanup(func() { resolveExecFn = prev })
	return &ran
}

func chainGraph(t *testing.T, root string) goReachGraph {
	t.Helper()
	g, err := goReachOnce(root)()
	if err != nil {
		t.Fatalf("reach graph: %v", err)
	}
	return g
}

func TestResolveStages_OnlyAnIntegrationPackageIsSettledAgainstItsImporters(t *testing.T) {
	g := chainGraph(t, chainModule(t))
	integration := MutantsConfig{AtMerge: true, IntegrationPackages: []string{"leaf"}}
	other := MutantsConfig{AtMerge: true, IntegrationPackages: []string{"near"}}

	cases := []struct {
		name         string
		cfg          MutantsConfig
		dir, status  string
		wantStages   [][]string
		wantReaching []string
	}{
		{"an integration package runs its own tests, then each distance", integration, "leaf", "missed",
			[][]string{{"leaf"}, {"near"}, {"far"}}, []string{"leaf", "near", "far"}},
		{"an inconclusive integration package skips the run gremlins made", integration, "leaf", gremlinsScopeUnknown,
			[][]string{{"near"}, {"far"}}, []string{"leaf", "near", "far"}},
		{"any other package runs its own tests alone", other, "leaf", "missed",
			[][]string{{"leaf"}}, []string{"leaf"}},
		{"an inconclusive package that is not listed runs nothing further", MutantsConfig{AtMerge: true}, "leaf", gremlinsScopeUnknown,
			nil, []string{"leaf"}},
		{"an integration package nothing imports runs its own tests only", integration, "far", "missed",
			[][]string{{"far"}}, []string{"far"}},
		{"an inconclusive integration package nothing imports has nothing left to run", MutantsConfig{AtMerge: true, IntegrationPackages: []string{"far"}},
			"far", gremlinsScopeUnknown, nil, []string{"far"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stages, reaching := resolveStages(g, tc.cfg, tc.dir, tc.status)
			if !reflect.DeepEqual(stages, tc.wantStages) || !reflect.DeepEqual(reaching, tc.wantReaching) {
				t.Errorf("stages = %v, reaching = %v; want %v, %v", stages, reaching, tc.wantStages, tc.wantReaching)
			}
		})
	}
}

// A package with no test of its own has none to miss the mutant, so it is
// settled against the tests above it, nearest first, even unlisted.
func TestResolveStages_APackageWithNoTestsOfItsOwnIsSettledAgainstItsImporters(t *testing.T) {
	root := chainModule(t)
	write(t, root, filepath.FromSlash("leaf/leaf_test.go"), "package leaf\n")
	// a file with no Test function still counts as a test file to go list
	g := chainGraph(t, root)
	g.Tested["leaf"] = false

	stages, reaching := resolveStages(g, MutantsConfig{AtMerge: true}, "leaf", gremlinsNotCovered)

	if want := [][]string{{"near"}, {"far"}}; !reflect.DeepEqual(stages, want) {
		t.Errorf("stages = %v, want %v", stages, want)
	}
	if want := []string{"near", "far"}; !reflect.DeepEqual(reaching, want) {
		t.Errorf("reaching = %v, want %v", reaching, want)
	}
}

func TestResolveStages_NoTestsAnywhereRunsNothing(t *testing.T) {
	g := goReachGraph{Tested: map[string]bool{}}

	stages, reaching := resolveStages(g, MutantsConfig{AtMerge: true}, "leaf", gremlinsNotCovered)

	if len(stages) != 0 || len(reaching) != 0 {
		t.Errorf("stages = %v, reaching = %v, want neither", stages, reaching)
	}
}

func TestTestedLayers_SkipsUntestedImportersAndEmptyDistances(t *testing.T) {
	g := chainGraph(t, chainModule(t))
	g.Tested["near"] = false

	got := testedLayers(g, "leaf")

	if want := [][]string{{"far"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("testedLayers(leaf) = %v, want %v: near has no tests, and the distance holding only near is left out", got, want)
	}
	if got := testedLayers(g, "far"); len(got) != 0 {
		t.Errorf("testedLayers(far) = %v, want none: nothing imports far", got)
	}
}

// The survivor is refused without a single test run and without a copy of
// the lane: its own package's tests were the whole opportunity.
func TestResolveGapMutants_AnInconclusiveMutantOfAnUnlistedPackageIsAMissWithNoRun(t *testing.T) {
	root := chainModule(t)
	ran := recordSettleRuns(t)

	out := resolveGapMutants(context.Background(), root, MutantsConfig{AtMerge: true}, goReachOnce(root),
		[]MutantOutcome{leafBoundary(gremlinsScopeUnknown)}, []int{0}, io.Discard)

	if out[0].Status != "missed" || !out[0].NewLine {
		t.Fatalf("got %q newline=%v (%s), want a survivor on a new line", out[0].Status, out[0].NewLine, out[0].Note)
	}
	if !strings.Contains(out[0].Note, "mutants-integration-packages") {
		t.Errorf("note = %q, want it to say why no other package's tests were run", out[0].Note)
	}
	if len(*ran) != 0 {
		t.Errorf("ran %v, want no test run for a package outside the integration list", *ran)
	}
}

// An unlisted package's NOT COVERED gap runs its own tests and no others: a
// kill above it does not count.
func TestResolveGapMutants_AnUnlistedPackagesGapIsRunAgainstItsOwnTestsAlone(t *testing.T) {
	root := chainModule(t)
	ran := recordSettleRuns(t)

	out := resolveGapMutants(context.Background(), root, MutantsConfig{AtMerge: true}, goReachOnce(root),
		[]MutantOutcome{leafBoundary(gremlinsNotCovered)}, []int{0}, io.Discard)

	if out[0].Status != "missed" {
		t.Fatalf("status = %q (%s), want missed: leaf's own test checks nothing", out[0].Status, out[0].Note)
	}
	if got := strings.Join(*ran, " "); got != "./leaf" {
		t.Errorf("ran %q, want ./leaf alone", got)
	}
}

// A listed package is run nearest first and the run stops at the first kill:
// near's test kills it, so far's is never started.
func TestResolveGapMutants_AListedPackageIsSettledNearestFirstAndStopsAtTheFirstKill(t *testing.T) {
	root := chainModule(t)
	ran := recordSettleRuns(t)
	cfg := MutantsConfig{AtMerge: true, IntegrationPackages: []string{"leaf"}}

	out := resolveGapMutants(context.Background(), root, cfg, goReachOnce(root),
		[]MutantOutcome{leafBoundary(gremlinsScopeUnknown)}, []int{0}, io.Discard)

	if out[0].Status != "caught" || !strings.Contains(out[0].Note, "example.com/m/near") {
		t.Fatalf("got %q (%s), want it caught by near's test", out[0].Status, out[0].Note)
	}
	if got, want := strings.Join(*ran, " "), "./near"; got != want {
		t.Errorf("ran %q, want %q: never far", got, want)
	}
}

// The whole run is capped: once the cap is spent a mutant is left UNRESOLVED
// without running anything.
func TestResolveGapMutants_PastTheRunsSettleCapTheRestAreUnresolved(t *testing.T) {
	root := chainModule(t)
	ran := recordSettleRuns(t)
	cfg := MutantsConfig{AtMerge: true, IntegrationPackages: []string{"leaf"}}
	t.Cleanup(setResolveTotalCapForTest(time.Hour))
	now := time.Unix(0, 0)
	prev := resolveClock
	resolveClock = func() time.Time { now = now.Add(40 * time.Minute); return now }
	t.Cleanup(func() { resolveClock = prev })
	second := leafBoundary(gremlinsScopeUnknown)
	second.Col = 10

	out := resolveGapMutants(context.Background(), root, cfg, goReachOnce(root),
		[]MutantOutcome{leafBoundary(gremlinsScopeUnknown), second}, []int{0, 1}, io.Discard)

	if out[0].Status != "caught" {
		t.Errorf("first: %q (%s), want it settled inside the cap", out[0].Status, out[0].Note)
	}
	if out[1].Status != gremlinsScopeUnknown || !strings.HasPrefix(out[1].Note, "UNRESOLVED: the run's settle time cap of 1h0m0s") {
		t.Errorf("second: %q (%s), want it left inconclusive and UNRESOLVED by the cap", out[1].Status, out[1].Note)
	}
	if strings.Contains(strings.Join(*ran, " "), "far") {
		t.Errorf("ran %v, want the second mutant never started", *ran)
	}
}

func TestSettleBudget_CutsAMutantToWhatIsLeftOfTheRunsCap(t *testing.T) {
	const cap = 10 * time.Minute
	cases := []struct {
		name               string
		perMutant, elapsed time.Duration
		want               time.Duration
		wantOK             bool
	}{
		{"nothing spent", 2 * time.Minute, 0, 2 * time.Minute, true},
		{"the mutant's own budget is what is left", 2 * time.Minute, cap - 2*time.Minute, 2 * time.Minute, true},
		{"one nanosecond less than its budget is left", 2 * time.Minute, cap - 2*time.Minute + 1, 2*time.Minute - 1, true},
		{"one nanosecond left", 2 * time.Minute, cap - 1, 1, true},
		{"the cap is spent exactly", 2 * time.Minute, cap, 0, false},
		{"one nanosecond over the cap", 2 * time.Minute, cap + 1, 0, false},
		{"well over the cap", 2 * time.Minute, 3 * cap, 0, false},
		{"a budget longer than what is left", 20 * time.Minute, time.Minute, cap - time.Minute, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := settleBudget(tc.perMutant, cap, tc.elapsed)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("settleBudget(%v, %v, %v) = (%v, %v), want (%v, %v)", tc.perMutant, cap, tc.elapsed, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// Only the mutants the report refuses take the `file.go:line:col: text` shape
// CI turns into an Error annotation. A settled kill, an unviable one and an
// inconclusive one are plain lines.
func TestJudgeMutants_OnlyARefusedMutantIsNamedInTheAnnotatedShape(t *testing.T) {
	t.Parallel()
	survivor := MutantOutcome{File: "p/a.go", Line: 1, Col: 2, Mutation: "ARITHMETIC_BASE", Status: "missed"}
	timedOut := MutantOutcome{File: "p/b.go", Line: 3, Col: 4, Mutation: "CONDITIONALS_BOUNDARY", Status: "timeout"}
	killed := MutantOutcome{File: "p/c.go", Line: 5, Col: 6, Mutation: "INVERT_LOGICAL", Status: "caught", Note: "killed by the tests of q"}
	unviable := MutantOutcome{File: "p/d.go", Line: 7, Col: 8, Mutation: "ARITHMETIC_BASE", Status: "unviable", Note: "does not compile"}
	inconclusive := MutantOutcome{File: "p/e.go", Line: 9, Col: 10, Mutation: "CONDITIONALS_NEGATION", Status: gremlinsScopeUnknown,
		Note: "outside tests reach it"}

	v := judgeMutants(MutantsConfig{AtMerge: true}, []MutantOutcome{survivor, timedOut, killed, unviable, inconclusive})

	annotated := regexp.MustCompile(`(?m)^\S*\.go:\d+:\d+: `)
	var got []string
	for _, line := range strings.Split(v.Message, "\n") {
		if annotated.MatchString(line) {
			got = append(got, strings.SplitN(line, ": ", 2)[0])
		}
	}
	if want := []string{"p/a.go:1:2", "p/b.go:3:4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("annotated lines name %v, want only the refused %v:\n%s", got, want, v.Message)
	}
	for _, plain := range []string{"p/c.go:5:6 INVERT_LOGICAL — killed", "p/d.go:7:8 ARITHMETIC_BASE — does not compile",
		"p/e.go:9:10 CONDITIONALS_NEGATION — outside tests reach it"} {
		if !strings.Contains(v.Message, plain) {
			t.Errorf("message does not carry the plain line %q:\n%s", plain, v.Message)
		}
	}
}

func TestIsIntegrationPackage_MatchesTheWholeDirectoryOnly(t *testing.T) {
	cfg := MutantsConfig{IntegrationPackages: []string{"internal/cli", "cmd/aphrollo"}}
	for dir, want := range map[string]bool{
		"internal/cli":   true,
		"cmd/aphrollo":   true,
		"internal/cli/x": false,
		"internal":       false,
		"internal/clip":  false,
		"":               false,
	} {
		if got := isIntegrationPackage(cfg, dir); got != want {
			t.Errorf("isIntegrationPackage(%q) = %v, want %v", dir, got, want)
		}
	}
	if isIntegrationPackage(MutantsConfig{}, "internal/cli") {
		t.Error("an empty list matched a package")
	}
}
