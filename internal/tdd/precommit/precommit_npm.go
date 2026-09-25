package precommit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// The npm root's counterpart to clippy and go vet: a TypeScript root is
// typechecked and an ESLint root linted before its suite, in cost order.
// Without it a type error committed cleanly — the only thing an npm root
// answered to was its test selection, and `export const probe: number =
// "not a number"` is not a test failure (issue #890).
//
// Each check runs only where the root configured the tool (a tsconfig.json,
// an eslint config), prefers the root's own script for it when package.json
// declares one, and otherwise runs the root's OWN installed binary from
// node_modules/.bin — never `npx`, which would fetch whatever the registry
// serves when the tool is not installed. A root with neither says NOT RUN and
// names `npm ci`: nothing was checked, and silence would read as a pass.

// npmCheck is one tool in an npm root's check sequence.
type npmCheck struct {
	stage  string // the gate.log and stderr name
	tool   string // the executable npm installs under node_modules/.bin
	script string // the package.json script preferred when declared
	// configured reports whether the root set this tool up at all.
	configured func(root string) bool
	// argvs is the local tool's argument lists, one run each; none means
	// there is nothing for it to check.
	argvs func(root string, touched []string) [][]string
}

var npmChecks = []npmCheck{
	{stage: "typecheck", tool: "tsc", script: "typecheck", configured: hasTsconfig, argvs: tscArgvs},
	{stage: "eslint", tool: "eslint", script: "lint", configured: hasEslintConfig, argvs: eslintArgvs},
}

// isNpmRoot reports whether root is an npm package.
func isNpmRoot(root string, _ Runner) bool {
	return pathExists(filepath.Join(root, "package.json"))
}

// npmQualityStage runs root's configured npm checks in order, stopping at
// the first rejection. touched is the staged files under root, root-relative.
func npmQualityStage(gateName, _, root string, touched []string, run SuiteRunner) GateResult {
	scripts := npmScripts(root)
	for _, c := range npmChecks {
		if !c.configured(root) {
			continue
		}
		runners, installed := c.runners(root, scripts, touched)
		if !installed {
			reportNpmCheckNotRun(gateName, root, c)
			continue
		}
		for _, r := range runners {
			if res := goCheckStage(gateName, c.stage, root, r, run); res.Blocked {
				return res
			}
		}
	}
	return verdictFor(gateName, "npm", root, "", stageOutcome{Kind: outcomePass})
}

// runners is what c runs in root: the declared script, or the local binary
// over its argument lists. installed is false when neither can run, because
// node_modules holds no copy of the tool.
func (c npmCheck) runners(root string, scripts map[string]string, touched []string) (runners []Runner, installed bool) {
	_, declared := scripts[c.script]
	if declared && pathExists(filepath.Join(root, "node_modules")) {
		return []Runner{{Cmd: "npm", Args: []string{"run", c.script}}}, true
	}
	bin := npmLocalBin(root, c.tool)
	if !pathExists(bin) {
		return nil, false
	}
	for _, args := range c.argvs(root, touched) {
		runners = append(runners, Runner{Cmd: bin, Args: args})
	}
	return runners, true
}

// reportNpmCheckNotRun is the loud line for a configured check whose tool is
// not installed. It does not refuse the commit — a box that has not run
// `npm ci` is not a defect in the code — but it is never a silent pass.
func reportNpmCheckNotRun(gateName, root string, c npmCheck) {
	fmt.Fprintf(os.Stderr,
		"[%s] gate %s: %s in %s → NOT RUN — node_modules/.bin/%s is not installed, so nothing was checked and this pass is not a green for it; run `npm ci` in %s\n",
		c.stage, gateName, c.tool, root, c.tool, root)
	AppendGateLog(gateName, root, c.tool, c.stage+"-not-run", 0)
}

// npmBinGOOS is the host npmLocalBin names a binary for: a variable, so a
// test on either host can pin the other one's name.
var npmBinGOOS = runtime.GOOS

// npmLocalBin is where npm installs tool for root: the .cmd shim on
// Windows.
func npmLocalBin(root, tool string) string {
	if npmBinGOOS == "windows" {
		tool += ".cmd"
	}
	return filepath.Join(root, "node_modules", ".bin", tool)
}

// npmScripts is package.json's scripts table, empty when it cannot be read.
func npmScripts(root string) map[string]string {
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	// An unreadable or absent manifest declares no script: Unmarshal
	// refuses the empty input and leaves the table nil.
	data, _ := os.ReadFile(filepath.Join(root, "package.json"))
	_ = json.Unmarshal(data, &pkg)
	return pkg.Scripts
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

// eslintArgvs lints the staged files that still exist: a deleted file
// handed to eslint fails the run on a path, not on the code.
func eslintArgvs(root string, touched []string) [][]string {
	var files []string
	for _, f := range touched {
		if slices.Contains(eslintExts, filepath.Ext(f)) && pathExists(filepath.Join(root, f)) {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil
	}
	return [][]string{files}
}

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
