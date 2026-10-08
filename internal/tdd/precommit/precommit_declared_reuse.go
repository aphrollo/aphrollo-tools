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
	path := declaredVerdictsPath()
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
	v.At = time.Now().UTC().Format(time.RFC3339)
	s.Verdicts[key] = v
	for len(s.Verdicts) > declaredVerdictsMax {
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
	path := declaredVerdictsPath()
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

// inputsHash is the hash of every file under root, tracked or untracked and not
// ignored, that one of globs selects: its path and the hash of its content.
func inputsHash(root string, globs []string) (string, error) {
	out, err := git(root, "-c", "core.quotepath=off", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return "", err
	}
	var picked []string
	seen := map[string]bool{}
	for _, p := range strings.Split(out, "\x00") {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		for _, g := range globs {
			if ratchet.MatchGlob(g, p) {
				picked = append(picked, p)
				break
			}
		}
	}
	sort.Strings(picked)
	h := sha256.New()
	fmt.Fprintf(h, "%s\n", strings.Join(globs, "\x00"))
	for _, p := range picked {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue // tracked but deleted: it is absent from the hash, as from the tree
		case err != nil:
			return "", err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(h, "%s\x00%x\n", p, sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
