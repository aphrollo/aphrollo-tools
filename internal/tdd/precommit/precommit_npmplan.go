package precommit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// An npm root's typecheck and lint are repo data, not a table in this
// source. The typecheck is the first of:
//
//  1. the argv aphrollo.toml declares for the root under [aphrollo.typecheck]
//     ("web" = ["svelte-check", "--tsconfig", "./tsconfig.json"]);
//  2. the root's package.json "typecheck" script, else its "check" script,
//     each `&&` step run on its own;
//  3. svelte-check, when the root depends on it and has a tsconfig.json,
//     after svelte-kit sync when it is a SvelteKit app (the types svelte-check
//     reads are the ones sync writes);
//  4. tsc, when the root has a tsconfig.json.
//
// The lint is the argv under [aphrollo.lint], else eslint over the staged
// files when the root configured it. Every command names an npm bin and runs
// as `node <that bin's entry>` from the root's own node_modules, never
// through npx or a shell (#929). A tool whose output the gate reads (tsc,
// vue-tsc, svelte-check, eslint) is judged by the diagnostics it adds over
// HEAD; any other command by the output lines it adds over HEAD's run. A run
// at HEAD repeats first the earlier steps whose output the gate cannot read:
// svelte-kit sync is setup that the svelte-check after it depends on.

// The aphrollo.toml tables a root's typecheck and lint are declared in.
const (
	typecheckTable = "[aphrollo.typecheck]"
	lintTable      = "[aphrollo.lint]"
)

// typecheckScripts are the package.json scripts that typecheck a root, in
// the order one is chosen: many repos spend "check" on formatting.
var typecheckScripts = []string{"typecheck", "check"}

// npmStep is one command of a root's typecheck or lint: the check that judges
// it, its arguments, and the output flag the gate adds to read it.
type npmStep struct {
	check  npmCheck
	args   []string
	output []string
}

// npmPlan is one of an npm root's checks as this commit runs it.
type npmPlan struct {
	stage string
	runs  []npmStep
	// notRun says why the check the root asked for cannot run here; the
	// commit is not refused over it, and the line says nothing was checked.
	notRun string
	// err is a declaration the gate cannot read; it refuses the commit.
	err error
}

// npmOutput is how the gate reads a known tool: the flag that makes it print
// what parse reads, unless the command already chose an output with one of
// names.
type npmOutput struct {
	flag, names []string
	parse       func(output, dir string) []diagnostic
}

var npmOutputs = map[string]npmOutput{
	"tsc":          {parse: parseTscDiagnostics},
	"vue-tsc":      {parse: parseTscDiagnostics},
	"svelte-check": {flag: []string{"--output", "machine"}, names: []string{"--output"}, parse: parseSvelteCheckDiagnostics},
	"eslint":       {flag: eslintFormat, names: []string{"--format", "-f"}, parse: parseEslintDiagnostics},
}

// binRun is argv, whose first word names an npm bin, as a run of stage.
func binRun(stage string, argv []string) npmStep {
	out := npmOutputs[argv[0]]
	nr := npmStep{check: npmCheck{stage: stage, bin: argv[0], headArgs: sameArgs, parse: out.parse}, args: argv[1:]}
	if !slices.ContainsFunc(nr.args, func(a string) bool {
		name, _, _ := strings.Cut(a, "=")
		return slices.Contains(out.names, name)
	}) {
		nr.output = out.flag
	}
	return nr
}

// npmTypecheckPlan is root's typecheck.
func npmTypecheckPlan(repoRoot, root string) npmPlan {
	plan := npmPlan{stage: "typecheck"}
	if argv, declared, err := declaredArgv(repoRoot, root, typecheckTable); declared {
		return declaredPlan(plan, typecheckTable, argv, err)
	}
	pkg := readPackageJSON(root)
	for _, name := range typecheckScripts {
		script, ok := pkg.Scripts[name]
		if !ok {
			continue
		}
		argvs, err := scriptArgvs(script)
		if err != nil {
			plan.notRun = fmt.Sprintf("package.json script %q %v; declare the command in aphrollo.toml %s instead", name, err, typecheckTable)
			return plan
		}
		for _, argv := range argvs {
			plan.runs = append(plan.runs, binRun(plan.stage, argv))
		}
		return plan
	}
	if !hasTsconfig(root) {
		return plan
	}
	if pkg.dependsOn("svelte-check") {
		if pkg.dependsOn("@sveltejs/kit") {
			plan.runs = append(plan.runs, binRun(plan.stage, []string{"svelte-kit", "sync"}))
		}
		plan.runs = append(plan.runs, binRun(plan.stage, []string{"svelte-check", "--tsconfig", "./tsconfig.json"}))
		return plan
	}
	for _, args := range tscArgvs(root, nil) {
		plan.runs = append(plan.runs, npmStep{check: tscCheck, args: args})
	}
	return plan
}

// npmLintPlan is root's lint. eslintRuns is the built-in eslint's argument
// lists: the staged files at a commit, the whole root at `aphrollo check`.
func npmLintPlan(repoRoot, root string, eslintRuns func() [][]string) npmPlan {
	plan := npmPlan{stage: "lint"}
	if argv, declared, err := declaredArgv(repoRoot, root, lintTable); declared {
		return declaredPlan(plan, lintTable, argv, err)
	}
	if !hasEslintConfig(root) {
		return plan
	}
	for _, args := range eslintRuns() {
		plan.runs = append(plan.runs, npmStep{check: eslintCheck, args: args})
	}
	return plan
}

// declaredPlan is plan running the argv table declares, or refusing over a
// declaration the gate could not read.
func declaredPlan(plan npmPlan, table string, argv []string, err error) npmPlan {
	if err != nil {
		plan.err = fmt.Errorf("aphrollo.toml %s declares this root's command in a shape the gate cannot read (%v); write it as one argv array, e.g. [\"svelte-check\", \"--tsconfig\", \"./tsconfig.json\"]", table, err)
		return plan
	}
	plan.runs = []npmStep{binRun(plan.stage, argv)}
	return plan
}

// declaredArgv is the argv aphrollo.toml's table declares for root.
func declaredArgv(repoRoot, root, table string) (argv []string, declared bool, err error) {
	value, declared := declaredEntry(repoRoot, root, table)
	if !declared {
		return nil, false, nil
	}
	err = json.Unmarshal([]byte(trailingCommaRe.ReplaceAllString(value, "$1")), &argv)
	if err == nil && (len(argv) == 0 || argv[0] == "") {
		err = errors.New("a command with no program")
	}
	return argv, true, err
}

// runNpmPlan runs plan's commands in order, stopping at the first rejection.
// A command whose tool is not installed ends the plan with a NOT RUN line:
// the steps after it read what it would have done.
func runNpmPlan(gateName, repoRoot, root string, plan npmPlan, run SuiteRunner) GateResult {
	if plan.err != nil {
		return verdictFor(gateName, plan.stage, root, "aphrollo.toml", stageOutcome{
			Kind: outcomeCheckError, Err: plan.err,
			Message: fmt.Sprintf("gate %s: %v, so nothing in %s was judged and the commit is refused.", gateName, plan.err, root),
		})
	}
	if plan.notRun != "" {
		reportNpmCheckNotRun(gateName, root, plan.stage, plan.stage, plan.notRun)
		plan.runs = nil
	}
	var prelude []Runner
	for _, nr := range plan.runs {
		tool, missing := nr.check.tool(root)
		if missing != "" {
			reportNpmCheckNotRun(gateName, root, nr.check.stage, nr.check.bin, missing)
			break
		}
		r := Runner{Cmd: tool.Cmd, Args: slices.Concat(tool.Args, nr.args, nr.output)}
		c := nr.check
		c.prelude = prelude
		var res GateResult
		if c.parse == nil {
			res = linesStage(gateName, c.stage, repoRoot, root, prelude, r, run)
			prelude = append(prelude, r)
		} else {
			res = npmCheckStage(gateName, repoRoot, root, c, r, run)
		}
		if res.Blocked {
			return res
		}
	}
	return verdictFor(gateName, plan.stage, root, "", stageOutcome{Kind: outcomePass})
}

// packageJSON is the part of a package.json the plans read.
type packageJSON struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// readPackageJSON is root's package.json; one that cannot be read declares
// nothing.
func readPackageJSON(root string) packageJSON {
	var pkg packageJSON
	data, _ := os.ReadFile(filepath.Join(root, "package.json"))
	_ = json.Unmarshal(data, &pkg)
	return pkg
}

func (p packageJSON) dependsOn(name string) bool {
	_, dev := p.DevDependencies[name]
	_, prod := p.Dependencies[name]
	return dev || prod
}

// scriptShellOnly is the characters only a shell gives meaning to in a
// script; `&&` is read here, as a sequence of commands.
const scriptShellOnly = "|;<>$`&()\\\n"

// scriptArgvs is the commands a package.json script runs, one per `&&`
// step, each split on spaces with a quoted word's quotes taken off. A script
// that needs a shell to mean what it says is an error naming why.
func scriptArgvs(script string) ([][]string, error) {
	var argvs [][]string
	for step := range strings.SplitSeq(script, "&&") {
		if strings.ContainsAny(step, scriptShellOnly) {
			return nil, fmt.Errorf("needs a shell to run (%s)", strings.TrimSpace(step))
		}
		argv := strings.Fields(step)
		if len(argv) == 0 {
			return nil, errors.New("has an empty step")
		}
		for i, w := range argv {
			w = unquoteWord(w)
			if strings.ContainsAny(w, `'"`) {
				return nil, fmt.Errorf("needs a shell to run (%s)", w)
			}
			argv[i] = w
		}
		argvs = append(argvs, argv)
	}
	return argvs, nil
}

// unquoteWord is w without the quotes around the whole of it.
func unquoteWord(w string) string {
	for _, q := range []string{`'`, `"`} {
		if inner, ok := strings.CutPrefix(w, q); ok {
			if inner, ok = strings.CutSuffix(inner, q); ok {
				return inner
			}
		}
	}
	return w
}

// npmToolEntry is the script of the installed package, scoped or not, that
// declares bin: the package named for it first, then every other in name
// order. "" when none does.
func npmToolEntry(nodeModules, bin string) string {
	if entry := npmBinEntry(filepath.Join(nodeModules, bin), bin); entry != "" {
		return entry
	}
	var pkgs []string
	top, _ := os.ReadDir(nodeModules)
	for _, e := range top {
		dir := filepath.Join(nodeModules, e.Name())
		if !strings.HasPrefix(e.Name(), "@") {
			pkgs = append(pkgs, dir)
			continue
		}
		scoped, _ := os.ReadDir(dir)
		for _, s := range scoped {
			pkgs = append(pkgs, filepath.Join(dir, s.Name()))
		}
	}
	for _, dir := range pkgs {
		if declaresBinByName(dir, bin) {
			return npmBinEntry(dir, bin)
		}
	}
	return ""
}

// declaresBinByName reports whether pkgDir's "bin" map names bin. The one-
// string form names only the package's own command, which the lookup by
// package name already tried.
func declaresBinByName(pkgDir, bin string) bool {
	var pkg struct {
		Bin map[string]string `json:"bin"`
	}
	data, _ := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	_ = json.Unmarshal(data, &pkg)
	_, ok := pkg.Bin[bin]
	return ok
}

// svelteCheckDiagRe is one error in svelte-check's machine output:
// `<ms> ERROR "<file>" <line>:<col> "<message>"`.
var svelteCheckDiagRe = regexp.MustCompile(`^\d+ ERROR "(.*)" (\d+):(\d+) "(.*)"$`)

// parseSvelteCheckDiagnostics reads the errors out of svelte-check's machine
// output, keyed by file and message so code moved by an edit is not new.
func parseSvelteCheckDiagnostics(output, _ string) []diagnostic {
	var diags []diagnostic
	for line := range strings.Lines(output) {
		m := svelteCheckDiagRe.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
		if m == nil {
			continue
		}
		diags = append(diags, diagnostic{
			Key:  m[1] + "\x00" + m[4],
			Line: fmt.Sprintf("%s:%s:%s: error %s", m[1], m[2], m[3], m[4]),
		})
	}
	return diags
}

// NpmVerifyStep is one command of an npm root's typecheck or lint as
// `aphrollo check` runs it, or why it cannot run there.
type NpmVerifyStep struct {
	Name string   // "typecheck" or "lint"
	Argv []string // `node <entry> args`; nil when Skip is set
	Skip string
}

// NpmVerifySteps is root's typecheck and then its lint, from the same data
// the commit gate reads, over the whole root: the built-in eslint lints "."
// rather than a commit's staged files, and no output flag is added, since a
// person reads this output, not the gate. err is a declaration the commit
// gate would refuse.
func NpmVerifySteps(repoRoot, root string) ([]NpmVerifyStep, error) {
	var steps []NpmVerifyStep
	whole := func() [][]string { return [][]string{{"."}} }
	for _, plan := range []npmPlan{npmTypecheckPlan(repoRoot, root), npmLintPlan(repoRoot, root, whole)} {
		if plan.err != nil {
			return nil, plan.err
		}
		steps = append(steps, plan.verifySteps(root)...)
	}
	return steps, nil
}

// verifySteps is plan as `aphrollo check` steps: one per command, up to the
// first whose tool cannot run, which is a skip saying why.
func (plan npmPlan) verifySteps(root string) []NpmVerifyStep {
	if plan.notRun != "" {
		return []NpmVerifyStep{{Name: plan.stage, Skip: plan.notRun}}
	}
	if len(plan.runs) == 0 {
		return []NpmVerifyStep{{Name: plan.stage, Skip: "nothing declared or detected for this root"}}
	}
	var steps []NpmVerifyStep
	for _, nr := range plan.runs {
		tool, missing := nr.check.tool(root)
		if missing != "" {
			return append(steps, NpmVerifyStep{Name: plan.stage, Skip: missing})
		}
		steps = append(steps, NpmVerifyStep{Name: plan.stage, Argv: slices.Concat([]string{tool.Cmd}, tool.Args, nr.args)})
	}
	return steps
}
