package precommit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A commit answers for the diagnostics it adds, not for the ones HEAD
// already carried. tsc checks the whole project, so one error standing in an
// untouched file would otherwise refuse every commit to its root until
// somebody fixed it — and filtering to the staged files instead would let a
// signature change that breaks a caller elsewhere through. So a failing run
// is repeated on HEAD's tree, and only the diagnostics it did not have there
// block. A diagnostic is keyed by file, code (or rule) and message, never by
// line or column, so code moved down by an edit above it is not new.
//
// HEAD's run happens in a detached worktree placed under the root's own
// node_modules: Node's module resolution and TypeScript's @types lookup walk
// up from there to the root's installed packages, so neither a symlink nor a
// Windows junction is needed. Its result is cached under HEAD's tree and the
// command, so the next commit on the same HEAD does not pay for it again.
// When the HEAD run cannot be produced the gate says so and holds every
// diagnostic against the commit, as it did before there was a baseline.

// diagnostic is one error a check reported: key compares it across trees,
// line is how the report shows it.
type diagnostic struct {
	Key  string `json:"key"`
	Line string `json:"line"`
}

// npmCheckStage runs one check and judges it against HEAD when it fails.
func npmCheckStage(gateName, repoRoot, root string, c npmCheck, r Runner, run SuiteRunner) GateResult {
	res := run(r, root)
	ran := func(Runner, string) SuiteResult { return res }
	now := c.parse(res.Output, root)
	// A pass, a timeout, or a failure this gate cannot read diagnostics out
	// of (a broken config, a crashed tool) is judged as it always was.
	if res.Passed || res.TimedOut || len(now) == 0 {
		return goCheckStage(gateName, c.stage, root, r, ran)
	}
	head, err := headDiagnostics(repoRoot, root, c, r, run)
	if err != nil {
		note := fmt.Sprintf("no baseline at HEAD (%v), so every diagnostic is held against this commit", err)
		fmt.Fprintf(os.Stderr, "[%s] gate %s: %s in %s → %s\n", c.stage, gateName, c.bin, root, note)
		AppendGateLog(gateName, root, cmdString(r), c.stage+"-no-head-baseline", 0)
		return diagnosticsBlock(gateName, c, root, r, res, now, 0, note)
	}
	fresh := newDiagnostics(now, head)
	held := len(now) - len(fresh)
	if len(fresh) == 0 {
		fmt.Fprintf(os.Stderr, "[%s] gate %s: %s in %s → no new diagnostics; %d already at HEAD, not held against this commit\n",
			c.stage, gateName, c.bin, root, held)
		AppendGateLog(gateName, root, cmdString(r), c.stage+"-head-only", res.Duration)
		return verdictFor(gateName, c.stage, root, cmdString(r), stageOutcome{Kind: outcomePass})
	}
	return diagnosticsBlock(gateName, c, root, r, res, fresh, held, "")
}

// newDiagnostics is now less head, counted: a key HEAD had twice and the
// tree has three times is one new diagnostic.
func newDiagnostics(now, head []diagnostic) []diagnostic {
	seen := map[string]int{}
	for _, d := range head {
		seen[d.Key]++
	}
	var fresh []diagnostic
	for _, d := range now {
		if seen[d.Key] > 0 {
			seen[d.Key]--
			continue
		}
		fresh = append(fresh, d)
	}
	return fresh
}

// diagnosticsBlock refuses the commit over the diagnostics it is held to,
// each as the tool printed it, and says how many it was not.
func diagnosticsBlock(gateName string, c npmCheck, root string, r Runner, res SuiteResult, diags []diagnostic, held int, note string) GateResult {
	var b strings.Builder
	fmt.Fprintf(&b, "TDD quality: %s found %d new diagnostic(s) in %s — fix before committing.\n", c.stage, len(diags), root)
	fmt.Fprintf(&b, "command: %s\n", cmdString(r))
	for _, d := range diags {
		b.WriteString(d.Line + "\n")
	}
	if held > 0 {
		fmt.Fprintf(&b, "%d diagnostic(s) already at HEAD were not held against this commit.\n", held)
	}
	if note != "" {
		b.WriteString(note + "\n")
	}
	return verdictFor(gateName, c.stage, root, cmdString(r), stageOutcome{Kind: outcomeFail, Result: res, Message: b.String()})
}

// headDiagnostics is what r reports on HEAD's tree, from the cache when the
// same command already ran on the same tree.
func headDiagnostics(repoRoot, root string, c npmCheck, r Runner, run SuiteRunner) ([]diagnostic, error) {
	tree, err := git(repoRoot, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		return nil, fmt.Errorf("no HEAD commit to compare against: %v", err)
	}
	// root is repoRoot or below it, both from the same walk.
	rel, _ := filepath.Rel(repoRoot, root)
	cache := headCachePath(strings.TrimSpace(tree), rel, r)
	if diags, ok := readHeadCache(cache); ok {
		return diags, nil
	}
	base, err := os.MkdirTemp(filepath.Join(root, "node_modules"), ".aphrollo-head-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(base)
	if _, err := git(repoRoot, "worktree", "add", "--detach", base, "HEAD"); err != nil {
		return nil, fmt.Errorf("worktree at HEAD: %v", err)
	}
	defer func() { _, _ = git(repoRoot, "worktree", "remove", "--force", base) }()
	headRoot := filepath.Join(base, rel)
	var diags []diagnostic
	if args := c.headArgs(headRoot, r.Args[1:]); args != nil {
		res := run(Runner{Cmd: r.Cmd, Args: append([]string{r.Args[0]}, args...)}, headRoot)
		diags = c.parse(res.Output, headRoot)
		if res.TimedOut || (!res.Passed && len(diags) == 0) {
			return nil, fmt.Errorf("the run at HEAD reached no reading: %s", firstDiagnostic(res.Output))
		}
	}
	writeHeadCache(cache, diags)
	return diags, nil
}

// headCachePath is where HEAD's diagnostics for this command on this tree
// are kept, "" when there is no state dir.
func headCachePath(tree, rel string, r Runner) string {
	dir := StateDir()
	if dir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(append([]string{tree, filepath.ToSlash(rel), r.Cmd}, r.Args...), "\x00")))
	return filepath.Join(dir, "npm-head", hex.EncodeToString(sum[:])+".json")
}

// readHeadCache is a cached HEAD run, and false for a miss: an entry that
// is absent or unreadable is simply run again.
func readHeadCache(path string) ([]diagnostic, bool) {
	data, err := os.ReadFile(path)
	var diags []diagnostic
	return diags, err == nil && json.Unmarshal(data, &diags) == nil
}

// writeHeadCache is best-effort: a lost write costs the next commit one
// more run at HEAD.
func writeHeadCache(path string, diags []diagnostic) {
	data, _ := json.Marshal(diags)
	if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		_ = os.WriteFile(path, data, 0o600)
	}
}

// tscDiagRe is one tsc error: `file(line,col): error TS1234: message`, or
// the same without a location for a config-level error.
var tscDiagRe = regexp.MustCompile(`^(?:(.+)\(\d+,\d+\): )?error (TS\d+): (.*)$`)

func parseTscDiagnostics(output, _ string) []diagnostic {
	var diags []diagnostic
	for line := range strings.Lines(output) {
		line = strings.TrimRight(line, "\r\n")
		if m := tscDiagRe.FindStringSubmatch(line); m != nil {
			diags = append(diags, diagnostic{Key: m[1] + "\x00" + m[2] + "\x00" + m[3], Line: line})
		}
	}
	return diags
}

// parseEslintDiagnostics reads `--format json` output: the errors only,
// since a warning does not fail the run. File paths come back absolute and
// are keyed relative to dir, so the same file compares equal in both trees.
func parseEslintDiagnostics(output, dir string) []diagnostic {
	start := strings.Index(output, "[")
	if start < 0 {
		return nil
	}
	var files []struct {
		FilePath string `json:"filePath"`
		Messages []struct {
			RuleID   string `json:"ruleId"`
			Message  string `json:"message"`
			Line     int    `json:"line"`
			Column   int    `json:"column"`
			Severity int    `json:"severity"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(strings.NewReader(output[start:])).Decode(&files); err != nil {
		return nil
	}
	var diags []diagnostic
	for _, f := range files {
		rel, err := filepath.Rel(dir, f.FilePath)
		if err != nil {
			rel = f.FilePath
		}
		rel = filepath.ToSlash(rel)
		for _, m := range f.Messages {
			if m.Severity != eslintError {
				continue
			}
			diags = append(diags, diagnostic{
				Key:  rel + "\x00" + m.RuleID + "\x00" + m.Message,
				Line: fmt.Sprintf("%s:%d:%d: error %s (%s)", rel, m.Line, m.Column, m.Message, m.RuleID),
			})
		}
	}
	return diags
}

// eslintError is ESLint's severity for an error, as against 1 for a warning.
const eslintError = 2
