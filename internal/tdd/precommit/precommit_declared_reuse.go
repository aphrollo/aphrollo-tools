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
// Commands that judge a failure against HEAD (baseline = "lines") never
// reuse: their pass is not a plain green.

const (
	declaredVerdictsName = "declared-verdicts.json"
	declaredVerdictsMax  = 200
	declaredKeyVersion   = "declared-reuse-v1"
)

// declaredVerdict is what one judged run of a command left: whether it passed,
// how long it took (the seconds a reuse saves), and when.
type declaredVerdict struct {
	Green bool    `json:"green"`
	Secs  float64 `json:"secs"`
	At    string  `json:"at"`
}

type declaredVerdictStore struct {
	Schema   int                        `json:"schema"`
	Verdicts map[string]declaredVerdict `json:"verdicts"`
	// newer marks a file written at a schema this binary does not know:
	// nothing is read from it and nothing is written back.
	newer bool
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
	if _, usable := readStateJSON(path, s); !usable {
		return &declaredVerdictStore{Verdicts: map[string]declaredVerdict{}, newer: true}
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
	s.Verdicts[key] = v
	for len(s.Verdicts) > max {
		oldest, oldestAt := "", ""
		for k, e := range s.Verdicts {
			if oldest == "" || e.At < oldestAt {
				oldest, oldestAt = k, e.At
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

// declaredReuseKey is the store key for c run in root, and the short form of
// the inputs' hash for the line a reuse prints. ok is false for a command
// that does not take part: no inputs, a baseline, or anything that could not
// be read.
func declaredReuseKey(root string, c declaredCommand) (key, short string, ok bool) {
	if len(c.Inputs) == 0 || (c.Baseline != "" && c.Baseline != baselineNone) {
		return "", "", false
	}
	tool, err := toolStamp(c.Argv[0])
	if err != nil {
		return "", "", false
	}
	inputs, err := inputsHash(root, c.Inputs)
	if err != nil {
		return "", "", false
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s", declaredKeyVersion, strings.Join(c.Argv, "\x00"), tool, inputs)
	state := hex.EncodeToString(h.Sum(nil))
	return mechKey(root, state, Runner{Cmd: c.Argv[0], Args: c.Argv[1:]}), inputs[:8], true
}

// toolStamp identifies the program a command runs: where it resolves and the
// size and modification time of the file there, so an upgraded tool is a
// different command.
func toolStamp(program string) (string, error) {
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
			return nil, fmt.Errorf("input glob \"%s\" starts outside the root", g)
		}
		out = append(out, n)
	}
	return out, nil
}

// inputsHash is the hash of every file under root, tracked or untracked and not
// ignored, that one of globs selects (its path and content), and of the root's
// lockfiles and manifests. It fails, so the command runs, when a glob selects
// no file (a typo would otherwise hash to a constant for ever) or selects a
// git-ignored one (the merge checkout does not hold it the same way).
func inputsHash(root string, globs []string) (string, error) {
	globs, err := normalizeInputGlobs(globs)
	if err != nil {
		return "", err
	}
	out, err := git(root, "-c", "core.quotepath=off", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return "", err
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
			return "", err
		}
		if !present {
			continue // tracked but deleted: absent from the hash, as from the tree
		}
		fmt.Fprintf(h, "%s\x00%s\n", p, stamp)
	}
	for i, g := range globs {
		if !matched[i] {
			return "", fmt.Errorf("input glob %q matches no file", g)
		}
	}
	ignored, err := git(root, "-c", "core.quotepath=off", "ls-files", "-z", "--ignored", "--others", "--exclude-standard")
	if err != nil {
		return "", err
	}
	for _, p := range strings.Split(ignored, "\x00") {
		if inDependencyDir(p) {
			continue
		}
		for _, g := range globs {
			if p != "" && ratchet.MatchGlob(g, p) {
				return "", fmt.Errorf("input glob %q selects the git-ignored %s", g, p)
			}
		}
	}
	for _, name := range lockfiles {
		stamp, present, err := contentStamp(root, name)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "lock %s\x00%v\x00%s\n", name, present, stamp)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
// records the seconds saved.
func declaredReuse(gateName, root string, r Runner, key, short string, ok bool) bool {
	if gateName != premergeDisplayName || !ok {
		return false
	}
	v, found := declaredVerdictFor(key)
	if !found || !v.Green {
		return false
	}
	fmt.Fprintf(stderrFor(root), "[reuse] %s: inputs unchanged since the lane's green (%s)\n", cmdString(r), short)
	AppendGateLogDetail(gateName, root, cmdString(r), "declared-reuse", 0,
		map[string]string{"saved_secs": strconv.FormatFloat(v.Secs, 'f', -1, 64)})
	return true
}

// recordDeclaredRun stores what a judged run of c left, provided the inputs are
// still what they were when it started.
func recordDeclaredRun(root string, c declaredCommand, key string, ok bool, res GateResult, last SuiteResult, ran bool) {
	if !ok || !ran || last.TimedOut || last.Inconclusive != "" {
		return
	}
	if again, _, same := declaredReuseKey(root, c); !same || again != key {
		return
	}
	recordDeclaredVerdict(key, declaredVerdict{Green: !res.Blocked && last.Passed, Secs: last.Duration.Seconds()})
}
