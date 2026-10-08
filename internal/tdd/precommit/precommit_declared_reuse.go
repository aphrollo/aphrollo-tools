package precommit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// A declared command can name the files it reads:
//
//	"frontend" = [{ argv = ["npx", "eslint", "src"], inputs = ["src/**", "package.json"] }]
//
// inputs are globs relative to the root the command runs in. When the commit
// gate judges such a command it records the verdict, keyed by the command and
// the content of every file those globs select. The merge gate hashes the
// merged tree the same way: an equal hash under a green entry means the
// command would read what it read when it passed, so the merge does not run
// it again. Anything else runs it: no inputs, no entry, a red entry, an
// unreadable store, a hash that could not be taken, a tool that changed.
// Commands that judge a failure against HEAD (baseline = "lines") keep no
// record: they are skipped when their tree equals HEAD's (precommit_declared_lines.go).

const (
	declaredVerdictsName = "declared-verdicts.json"
	declaredVerdictsMax  = 200
	declaredKeyVersion   = "declared-reuse-v2"
)

// declaredVerdict is what one judged run of a command left: whether it passed,
// how long it took (the seconds a reuse saves), and when.
//
// Scope names the command and the root it ran in (the same in every worktree
// of a repo), and Parts the hashes the key is made of. A later miss compares
// its own parts with the latest entry of its scope to say which one moved. An
// entry without them (written before they were kept) is no record.
type declaredVerdict struct {
	Green bool           `json:"green"`
	Secs  float64        `json:"secs"`
	At    string         `json:"at"`
	Scope string         `json:"scope,omitempty"`
	Parts *declaredParts `json:"parts,omitempty"`
	// Seq counts the writes to the store, so two entries made in one second
	// still have an order. An entry from an older binary has none (0).
	Seq int64 `json:"seq,omitempty"`
}

// declaredParts are the three hashes a declared command's key folds together.
type declaredParts struct {
	Inputs string `json:"inputs"`
	Locks  string `json:"locks"`
	Tool   string `json:"tool"`
}

type declaredVerdictStore struct {
	Schema   int                        `json:"schema"`
	Verdicts map[string]declaredVerdict `json:"verdicts"`
	// newer marks a file written at a schema this binary does not know:
	// nothing is read from it and nothing is written back.
	newer bool
	// bad marks a file that was there and did not parse; it has been set
	// aside, and the store reads as empty.
	bad bool
}

// declaredVerdictsWrite serialises this process's writes. Two processes can still
// lose one another's entry; the cost is a miss, which runs the command.
var declaredVerdictsWrite sync.Mutex

func declaredVerdictsPath() string {
	dir := StateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, declaredVerdictsName)
}

func loadDeclaredVerdicts(path string) *declaredVerdictStore {
	s := &declaredVerdictStore{Verdicts: map[string]declaredVerdict{}}
	_, existed := os.Stat(path)
	ok, usable := readStateJSON(path, s)
	if !usable {
		return &declaredVerdictStore{Verdicts: map[string]declaredVerdict{}, newer: true}
	}
	if !ok && existed == nil {
		return &declaredVerdictStore{Verdicts: map[string]declaredVerdict{}, bad: true}
	}
	if s.Verdicts == nil {
		s.Verdicts = map[string]declaredVerdict{}
	}
	return s
}

// recordDeclaredVerdict stores v under key, dropping the oldest entries past
// the cap. Best-effort: a failure only loses the reuse.
func recordDeclaredVerdict(key string, v declaredVerdict) {
	recordDeclaredVerdictAt(declaredVerdictsPath(), key, v, declaredVerdictsMax)
}

// recordDeclaredVerdictAt is recordDeclaredVerdict into the store at path,
// keeping at most max entries. A v with no time is stamped now.
func recordDeclaredVerdictAt(path, key string, v declaredVerdict, max int) {
	if key == "" || path == "" {
		return
	}
	declaredVerdictsWrite.Lock()
	defer declaredVerdictsWrite.Unlock()
	s := loadDeclaredVerdicts(path)
	if s.newer {
		return
	}
	s.Schema = StateSchema
	if v.At == "" {
		v.At = time.Now().UTC().Format(time.RFC3339)
	}
	v.Seq = 1
	for _, e := range s.Verdicts {
		if e.Seq >= v.Seq {
			v.Seq = e.Seq + 1
		}
	}
	s.Verdicts[key] = v
	for len(s.Verdicts) > max {
		oldest := ""
		for k, e := range s.Verdicts {
			if oldest == "" || declaredNewer(oldest, s.Verdicts[oldest], k, e) {
				oldest = k
			}
		}
		delete(s.Verdicts, oldest)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	if data, err := json.MarshalIndent(s, "", "  "); err == nil {
		_ = writeFileAtomic(path, data)
	}
}

func declaredVerdictFor(key string) (declaredVerdict, bool) {
	return declaredVerdictAt(declaredVerdictsPath(), key)
}

func declaredVerdictAt(path, key string) (declaredVerdict, bool) {
	if key == "" || path == "" {
		return declaredVerdict{}, false
	}
	s := loadDeclaredVerdicts(path)
	if s.newer {
		return declaredVerdict{}, false
	}
	v, ok := s.Verdicts[key]
	return v, ok
}

// declaredKeyed is what keying c in root came to: the store key, the short
// form of the inputs' hash for the line a reuse prints, the scope and parts a
// stored entry carries. takes is false for a command that does not take part
// (no inputs, or a baseline); err is set when it does and the key could not be
// taken, and says which of the causes it was.
type declaredKeyed struct {
	key, short, scope string
	parts             declaredParts
	takes             bool
	// baselined marks a command with inputs that is judged against HEAD and so
	// keeps no record.
	baselined bool
	// cmd is the command, kept for the comparison a baselined one is judged by.
	cmd declaredCommand
	err error
}

// declaredKeying keys c run in root.
func declaredKeying(root string, c declaredCommand) declaredKeyed {
	if len(c.Inputs) == 0 {
		return declaredKeyed{}
	}
	if c.Baseline != "" && c.Baseline != baselineNone {
		return declaredKeyed{baselined: true, cmd: c}
	}
	k := declaredKeyed{takes: true}
	r := Runner{Cmd: c.Argv[0], Args: c.Argv[1:]}
	k.scope = mechKey(root, "", r)
	tool, err := toolStamp(root, c.Argv[0])
	if err != nil {
		k.err = &toolError{program: c.Argv[0], err: err}
		return k
	}
	inputs, locks, err := inputsParts(root, c.Inputs)
	if err != nil {
		k.err = err
		return k
	}
	k.parts = declaredParts{Inputs: inputs, Locks: locks, Tool: tool}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s", declaredKeyVersion, strings.Join(c.Argv, "\x00"), tool, inputs, locks)
	k.key = mechKey(root, hex.EncodeToString(h.Sum(nil)), r)
	k.short = inputs[:8]
	return k
}

// declaredReuseKey is the store key for c run in root, and the short form of
// the inputs' hash for the line a reuse prints. ok is false for a command
// that does not take part: no inputs, a baseline, or anything that could not
// be read.
func declaredReuseKey(root string, c declaredCommand) (key, short string, ok bool) {
	k := declaredKeying(root, c)
	if !k.takes || k.err != nil {
		return "", "", false
	}
	return k.key, k.short, true
}

// declaredVerdictFrom is the entry a judged run of the command keyed as k
// leaves: its outcome and seconds, and the scope and parts a later miss
// compares against.
func declaredVerdictFrom(k declaredKeyed, green bool, secs float64) declaredVerdict {
	parts := k.parts
	return declaredVerdict{Green: green, Secs: secs, Scope: k.scope, Parts: &parts}
}

// toolError is a program that could not be resolved.
type toolError struct {
	program string
	err     error
}

func (e *toolError) Error() string { return fmt.Sprintf("tool %s: %v", e.program, e.err) }
func (e *toolError) Unwrap() error { return e.err }

// toolStamp identifies the program a command runs, so an upgraded tool is a
// different command. A program found on PATH (or by absolute path) is its
// location, size and modification time. A program named by a path inside the
// root (./scripts/lint.sh) is the file in the root's own checkout, found from
// root and not from the process, and is its content: a modification time is
// when a worktree was checked out, which differs between the lane's and the
// merge gate's.
func toolStamp(root, program string) (string, error) {
	if strings.ContainsAny(program, `/\`) && !filepath.IsAbs(program) {
		stamp, present, err := contentStamp(root, filepath.ToSlash(program))
		if err != nil {
			return "", err
		}
		if !present {
			return "", fs.ErrNotExist
		}
		return "root:" + filepath.ToSlash(filepath.Clean(program)) + "|" + stamp, nil
	}
	path, err := exec.LookPath(program)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano()), nil
}

// lockfiles are the files of a root a dependency or tool-version move changes
// without touching any input the command lists; their content is part of every
// key, with the manifests beside them.
var lockfiles = []string{
	"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "go.sum", "Cargo.lock",
	"package.json", "go.mod", "Cargo.toml",
}

// dependencyDirs are the path segments of installed dependencies. The lockfile
// fold already stands for their content, so an ignored file under one is
// neither hashed nor a reason to refuse a key. A new dependency dir is a row.
var dependencyDirs = []string{"node_modules"}

func inDependencyDir(path string) bool {
	return slices.ContainsFunc(strings.Split(path, "/"), func(seg string) bool { return slices.Contains(dependencyDirs, seg) })
}

// normalizeInputGlobs is globs as slash paths from the root: backslashes
// become slashes and a leading ./ goes. A glob that starts outside the root
// (/ or ..) is refused by name, since the tree hashed is the root's.
func normalizeInputGlobs(globs []string) ([]string, error) {
	out := make([]string, 0, len(globs))
	for _, g := range globs {
		n := strings.ReplaceAll(g, `\`, "/")
		// walk-terminates: each turn drops two bytes from n
		for strings.HasPrefix(n, "./") {
			n = strings.TrimPrefix(n, "./")
		}
		if n == "" || strings.HasPrefix(n, "/") || n == ".." || strings.HasPrefix(n, "../") || filepath.VolumeName(n) != "" {
			return nil, &globError{kind: globOutside, arg: g}
		}
		out = append(out, n)
	}
	return out, nil
}

// inputsParts is two hashes of root. inputs covers every file, tracked or
// untracked and not ignored, that one of globs selects (its path and content);
// locks covers the root's lockfiles and manifests. It fails, so the command
// runs, when a glob selects no file (a typo would otherwise hash to a constant
// for ever) or selects a git-ignored one (the merge checkout does not hold it
// the same way).
func inputsParts(root string, globs []string) (inputs, locks string, err error) {
	globs, err = normalizeInputGlobs(globs)
	if err != nil {
		return "", "", err
	}
	out, err := git(root, "-c", "core.quotepath=off", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return "", "", err
	}
	matched := make([]bool, len(globs))
	var picked []string
	seen := map[string]bool{}
	for _, p := range strings.Split(out, "\x00") {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		hit := false
		for i, g := range globs {
			if ratchet.MatchGlob(g, p) {
				matched[i], hit = true, true
			}
		}
		if hit {
			picked = append(picked, p)
		}
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\n", strings.Join(globs, "\x00"))
	sort.Strings(picked)
	for _, p := range picked {
		stamp, present, err := contentStamp(root, p)
		if err != nil {
			return "", "", err
		}
		if !present {
			continue // tracked but deleted: absent from the hash, as from the tree
		}
		fmt.Fprintf(h, "%s\x00%s\n", p, stamp)
	}
	for i, g := range globs {
		if !matched[i] {
			return "", "", &globError{kind: globNoMatch, arg: g}
		}
	}
	ignored, err := git(root, "-c", "core.quotepath=off", "ls-files", "-z", "--ignored", "--others", "--exclude-standard")
	if err != nil {
		return "", "", err
	}
	for _, p := range strings.Split(ignored, "\x00") {
		if inDependencyDir(p) {
			continue
		}
		for _, g := range globs {
			if p != "" && ratchet.MatchGlob(g, p) {
				return "", "", &globError{kind: globIgnored, arg: p}
			}
		}
	}
	lh := sha256.New()
	for _, name := range lockfiles {
		stamp, present, err := contentStamp(root, name)
		if err != nil {
			return "", "", err
		}
		fmt.Fprintf(lh, "lock %s\x00%v\x00%s\n", name, present, stamp)
	}
	return hex.EncodeToString(h.Sum(nil)), hex.EncodeToString(lh.Sum(nil)), nil
}

// contentStamp is the hash of the file at rel under root; present is false for
// a file that does not exist.
func contentStamp(root, rel string) (stamp string, present bool, err error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), true, nil
}

// declaredReuse answers whether the merge gate may skip c: it must be the
// merge, c must declare inputs, and the store must hold a green for exactly
// this command over exactly these inputs. It says so on the gate's stderr and
// records the seconds saved. When the merge runs a command that declares
// inputs instead, it says why (declaredMissReason); when the commit gate
// cannot key such a command, so that nothing is recorded for the merge, it
// says that.
func declaredReuse(gateName, root string, r Runner, k declaredKeyed) bool {
	if k.baselined {
		return linesBaseReuse(gateName, root, r, k.cmd)
	}
	if !k.takes {
		return false
	}
	if gateName != premergeDisplayName {
		if k.err != nil {
			fmt.Fprintf(stderrFor(root), "[note] %s: not recorded for reuse — %s\n", cmdString(r), keyErrReason(k.err))
		}
		return false
	}
	if k.err == nil {
		if v, found := declaredVerdictFor(k.key); found && v.Green {
			fmt.Fprintf(stderrFor(root), "[reuse] %s: inputs unchanged since the lane's green (%s)\n", cmdString(r), k.short)
			AppendGateLogDetail(gateName, root, cmdString(r), "declared-reuse", 0,
				map[string]string{"saved_secs": strconv.FormatFloat(v.Secs, 'f', -1, 64)})
			return true
		}
	}
	fmt.Fprintf(stderrFor(root), "[run] %s: no reuse — %s\n", cmdString(r), missReasonFor(declaredVerdictsPath(), k, time.Now()))
	return false
}

// recordDeclaredRun stores what a judged run of c left, provided the inputs are
// still what they were when it started.
func recordDeclaredRun(root string, c declaredCommand, key string, ok bool, res GateResult, last SuiteResult, ran bool) {
	if !ok || !ran || last.TimedOut || last.Inconclusive != "" {
		return
	}
	again := declaredKeying(root, c)
	if again.err != nil || again.key != key {
		return
	}
	recordDeclaredVerdict(key, declaredVerdictFrom(again, !res.Blocked && last.Passed, last.Duration.Seconds()))
}
