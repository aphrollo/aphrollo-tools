package precommit

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
)

// The npm root's counterpart to clippy and go vet: a TypeScript root is
// typechecked and an ESLint root linted before its suite, in cost order.
// Without it a type error committed cleanly — the only thing an npm root
// answered to was its test selection, and `export const probe: number =
// "not a number"` is not a test failure (issue #890).
//
// Each check runs only where the root configured the tool (a tsconfig.json,
// an eslint config), and runs the root's OWN installed copy as
// `node <package entry>`: never `npx`, which fetches whatever the registry
// serves when the tool is not installed, and never the node_modules/.bin
// .cmd shim, which on Windows goes through cmd.exe and mangles `^`, `&`, `%`
// and quotes in a staged path (the reason this repo's own shims are real
// executables, internal/cli/argv0.go). A root without the tool says NOT RUN
// and names `npm ci`: nothing was checked, and silence would read as a pass.
// A repo whose root needs its own command declares it in aphrollo.toml
// (precommit_declared.go).
//
// tsc checks the whole project, so a diagnostic that already stood at HEAD
// would refuse every commit to the root until somebody fixed it. A run that
// fails is compared with the same run at HEAD (precommit_npmbaseline.go), and
// only what the commit adds is held against it.

// npmCheck judges one tool of an npm root's typecheck or lint.
type npmCheck struct {
	stage string // the gate.log and stderr name
	// pkg is the npm package that installs the tool; "" finds the
	// installed package that declares bin (npmToolEntry).
	pkg string
	bin string // the tool's name in that package's "bin"
	// headArgs is the run's argument list as the same run at HEAD takes
	// it, nil when HEAD holds nothing for it to check.
	headArgs func(headRoot string, args []string) []string
	// parse reads the diagnostics out of a run in dir; nil for a tool whose
	// output the gate cannot read, which is judged by its output lines.
	parse func(output, dir string) []diagnostic
	// prelude is the earlier runs of the same check a run at HEAD repeats
	// first: the setup, like svelte-kit sync, that the judged tool reads.
	prelude []Runner
}

var (
	tscCheck    = npmCheck{stage: "typecheck", pkg: "typescript", bin: "tsc", headArgs: sameArgs, parse: parseTscDiagnostics}
	eslintCheck = npmCheck{stage: "eslint", pkg: "eslint", bin: "eslint", headArgs: eslintHeadArgs, parse: parseEslintDiagnostics}
)

// isNpmRoot reports whether root is an npm package.
func isNpmRoot(root string, _ Runner) bool {
	return pathExists(filepath.Join(root, "package.json"))
}

// npmQualityStage runs root's typecheck and then its lint, stopping at the
// first rejection. touched is the staged files under root, root-relative.
func npmQualityStage(gateName, repoRoot, root string, touched []string, run SuiteRunner) GateResult {
	for _, plan := range []npmPlan{npmTypecheckPlan(repoRoot, root), npmLintPlan(repoRoot, root, func() [][]string { return eslintArgvs(root, touched) })} {
		if res := runNpmPlan(gateName, repoRoot, root, plan, run); res.Blocked {
			return res
		}
	}
	return verdictFor(gateName, "npm", root, "", stageOutcome{Kind: outcomePass})
}

// lookNode finds the node binary. A variable, so a test can name one
// without depending on the box's PATH.
var lookNode = func() (string, error) { return exec.LookPath("node") }

// tool is `node <entry>` for c's installed copy in root, or why it cannot
// run.
func (c npmCheck) tool(root string) (r Runner, missing string) {
	nodeModules := filepath.Join(root, "node_modules")
	var entry string
	if c.pkg != "" {
		entry = npmBinEntry(filepath.Join(nodeModules, c.pkg), c.bin)
		missing = fmt.Sprintf("node_modules/%s is not installed; run `npm ci` in %s", c.pkg, root)
	} else {
		entry = npmToolEntry(nodeModules, c.bin)
		missing = fmt.Sprintf("no package in node_modules declares the bin %s; run `npm ci` in %s", c.bin, root)
	}
	if entry == "" {
		return r, missing
	}
	node, err := lookNode()
	if err != nil {
		return r, "node is not on PATH; install Node.js so the gate can run " + c.bin
	}
	return Runner{Cmd: node, Args: []string{entry}}, ""
}

// reportNpmCheckNotRun is the loud line for a configured check that cannot
// run here. It does not refuse the commit — a box that has not run `npm ci`
// is not a defect in the code — but it is never a silent pass.
func reportNpmCheckNotRun(gateName, root, stage, bin, why string) {
	fmt.Fprintf(os.Stderr,
		"[%s] gate %s: %s in %s → NOT RUN — %s; nothing was checked and this pass is not a green for it\n",
		stage, gateName, bin, root, why)
	AppendGateLog(gateName, root, bin, stage+"-not-run", 0)
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func hasTsconfig(root string) bool {
	return pathExists(filepath.Join(root, "tsconfig.json"))
}

// eslintConfigNames is every file ESLint reads its configuration from: the
// flat config, then the legacy eslintrc family.
var eslintConfigNames = []string{
	"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs",
	"eslint.config.ts", "eslint.config.mts", "eslint.config.cts",
	".eslintrc", ".eslintrc.js", ".eslintrc.cjs", ".eslintrc.json", ".eslintrc.yaml", ".eslintrc.yml",
}

func hasEslintConfig(root string) bool {
	return slices.ContainsFunc(eslintConfigNames, func(name string) bool {
		return pathExists(filepath.Join(root, name))
	})
}

// eslintExts is the source ESLint lints by default.
var eslintExts = []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts"}

// eslintFormat is the flags every eslint run starts with: JSON is the one
// output every ESLint major still ships, and the only one this gate can
// compare between two trees.
var eslintFormat = []string{"--format", "json"}

// eslintArgvs lints the staged files that still exist: a deleted file
// handed to eslint fails the run on a path, not on the code. A large set
// splits across runs whose arguments each stay within argvbatch.Budget
// (issue #951), which leaves the budget's headroom for the `node <entry>`
// the tool line starts with.
func eslintArgvs(root string, touched []string) [][]string {
	return argvbatch.Split(eslintFormat, existingLintable(root, touched), argvbatch.Budget)
}

// eslintHeadArgs keeps the files HEAD already had: a file the commit adds
// had no diagnostics before it.
func eslintHeadArgs(headRoot string, args []string) []string {
	files := existingLintable(headRoot, args[len(eslintFormat):])
	if len(files) == 0 {
		return nil
	}
	return append(slices.Clone(eslintFormat), files...)
}

func existingLintable(root string, files []string) []string {
	var out []string
	for _, f := range files {
		if slices.Contains(eslintExts, filepath.Ext(f)) && pathExists(filepath.Join(root, f)) {
			out = append(out, f)
		}
	}
	return out
}

// sameArgs is a check whose run at HEAD takes the arguments unchanged.
func sameArgs(_ string, args []string) []string { return args }

// tscArgvs typechecks the root's tsconfig.json, or each project it
// references when it is a solution file. `tsc -p tsconfig.json --noEmit` on
// Vite's solution-style root checks nothing and exits 0; `tsc -b --noEmit`
// would check it, but writes .tsbuildinfo files into the tree and is refused
// outright by a TypeScript older than 5.6. `-p <reference> --noEmit` per
// project checks the same ground and writes nothing.
func tscArgvs(root string, _ []string) [][]string {
	refs := solutionReferences(filepath.Join(root, "tsconfig.json"))
	if len(refs) == 0 {
		return [][]string{{"-p", "tsconfig.json", "--noEmit"}}
	}
	var out [][]string
	for _, ref := range refs {
		out = append(out, []string{"-p", ref, "--noEmit"})
	}
	return out
}

// solutionReferences is the referenced projects of a tsconfig that checks
// nothing of its own — "files": [] and no "include" — and nil for any other
// config, or one that cannot be read (tsc then reports that itself).
func solutionReferences(path string) []string {
	// A config that cannot be read decodes as nothing, so it falls to the
	// plain -p run, where tsc reports it.
	data, _ := os.ReadFile(path)
	var cfg struct {
		Files      *[]string `json:"files"`
		Include    []string  `json:"include"`
		References []struct {
			Path string `json:"path"`
		} `json:"references"`
	}
	if json.Unmarshal([]byte(jsoncToJSON(string(data))), &cfg) != nil {
		return nil
	}
	if cfg.Files == nil || len(*cfg.Files) > 0 || len(cfg.Include) > 0 {
		return nil
	}
	var refs []string
	for _, r := range cfg.References {
		refs = append(refs, r.Path)
	}
	return refs
}

// jsoncState is where jsoncToJSON's scan stands.
type jsoncState int

const (
	jsoncCode jsoncState = iota
	jsoncSlash
	jsoncLineComment
	jsoncBlockComment
	jsoncBlockStar
	jsoncString
	jsoncEscape
)

// trailingCommaRe is a comma closing an array or object, which JSONC allows
// and JSON does not. It does not know about strings: a tsconfig value holding
// ",]" would lose its comma, and no real one does.
var trailingCommaRe = regexp.MustCompile(`,(\s*[\]}])`)

// jsoncToJSON strips the comments and trailing commas tsconfig allows, so
// encoding/json can read it. A comment opener inside a string ("src/**/*")
// is string content, not a comment.
func jsoncToJSON(src string) string {
	var b strings.Builder
	state := jsoncCode
	for _, c := range src {
		switch state {
		case jsoncCode:
			switch c {
			case '/':
				state = jsoncSlash
				continue
			case '"':
				state = jsoncString
			}
			b.WriteRune(c)
		case jsoncSlash:
			switch c {
			case '/':
				state = jsoncLineComment
			case '*':
				state = jsoncBlockComment
			default:
				// A lone slash is not JSON either; keep it for the
				// decoder to refuse.
				b.WriteRune('/')
				b.WriteRune(c)
				state = jsoncCode
			}
		case jsoncLineComment:
			if c == '\n' {
				b.WriteRune(c)
				state = jsoncCode
			}
		case jsoncBlockComment:
			if c == '*' {
				state = jsoncBlockStar
			}
		case jsoncBlockStar:
			switch c {
			case '/':
				state = jsoncCode
			case '*':
			default:
				state = jsoncBlockComment
			}
		case jsoncString:
			b.WriteRune(c)
			switch c {
			case '\\':
				state = jsoncEscape
			case '"':
				state = jsoncCode
			}
		case jsoncEscape:
			b.WriteRune(c)
			state = jsoncString
		}
	}
	return trailingCommaRe.ReplaceAllString(b.String(), "$1")
}
