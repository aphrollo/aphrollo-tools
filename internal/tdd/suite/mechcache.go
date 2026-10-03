package suite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	igit "github.com/aphrollo/aphrollo-tools/internal/git"
	"github.com/aphrollo/aphrollo-tools/internal/run"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
)

// The mechanical green cache remembers which exact (worktree content, test
// command) pairs have already proven green, so the gate never charges the
// suite twice for the same state. The big win is runners with no related-tests
// mode (cargo, pytest, zig, a generic npm script): their mechanical stage is
// the FULL suite, and without the cache a green PostToolUse run is re-run
// wholesale seconds later at commit. Only green results are cached — a red
// must re-run so the block always carries fresh output.

// mechCacheMax bounds the cache file; the oldest entries are pruned beyond it.
const mechCacheMax = 200

type mechCacheFile struct {
	Schema int               `json:"schema"`
	Green  map[string]string `json:"green"` // mechKey → RFC3339 time of the green run
	// newer records that the file on disk was written at a schema this binary
	// does not know: nothing is read from it and nothing is written back, so
	// the gate simply re-runs the suite rather than trusting or clobbering a
	// record it cannot interpret.
	newer bool
}

func mechCachePath() string {
	dir := StateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "mech-cache.json")
}

// mechKey identifies one provably-green state: the REPO (its git common dir,
// so every linked worktree of one repo shares the cache — identical content
// under an identical command is the same proven fact wherever it is checked
// out), the worktree content hash, and the exact command that went green. A
// different runner argv (a differently-scoped run) never satisfies a lookup
// for the full suite. A root outside any git repo keys on itself, so
// unrelated non-repo projects never collapse into one key.
//
// The project root's place in the repo is part of the key too. The state
// hash is taken over the repo-wide diff and a command names paths relative
// to the root it runs in, so two roots of one repo running one command (two
// Go modules each running `go test ./pkg`) otherwise share a key, and one
// root's green answered the other's lookup: a red suite read as cache-hit.
func mechKey(root, stateHash string, r Runner) string {
	return mechKeyPrefix(root, stateHash) + r.Cmd + " " + strings.Join(r.Args, " ")
}

// mechKeyPrefix is every mechKey for root at stateHash, up to the command:
// the part a lookup that accepts any command matches on.
func mechKeyPrefix(root, stateHash string) string {
	return mechKeyRepo(root) + "\x00" + mechKeyRoot(root) + "\x00" + stateHash + "\x00"
}

// mechKeyRoot is root's path inside its worktree (`a/` for module a, "" at
// the top), which is the same in every linked worktree of the repo, so the
// cache stays shared across them. Outside a repo it is "": mechKeyRepo
// already keys on root itself there.
func mechKeyRoot(root string) string {
	c := gitx.HookClient(root)
	if c == nil {
		return ""
	}
	rel, err := filepath.Rel(c.Root(), root)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel) + "/"
}

// mechKeyRepo resolves root to the identity the cache keys on: the repo's
// git common dir, or root itself when git cannot answer.
func mechKeyRepo(root string) string {
	c := gitx.HookClient(root)
	if c == nil {
		return root
	}
	dir := c.CommonDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return filepath.Clean(dir)
}

func loadMechCache(path string) *mechCacheFile {
	c := &mechCacheFile{Green: map[string]string{}}
	if _, usable := readStateJSON(path, c); !usable {
		return &mechCacheFile{Green: map[string]string{}, newer: true}
	}
	if c.Green == nil {
		c.Green = map[string]string{}
	}
	return c
}

// mechCacheHit reports whether key is a recorded green. An empty key (no
// state hash, or no state dir) never hits.
func mechCacheHit(key string) bool {
	if key == "" {
		return false
	}
	path := mechCachePath()
	if path == "" {
		return false
	}
	_, ok := loadMechCache(path).Green[key]
	return ok
}

// mechCacheCovers reports whether the cache proves want green at root in
// stateHash: want's own mechKey, or greens recorded at that state whose
// scopes together cover want's (provenCovers, the claim ledger's rule). A
// filtered run covers only its own filter, so a scoped edit-time green never
// answers for a package's suite. A command the scope classifier cannot read
// is answered by its exact key alone.
func mechCacheCovers(root, stateHash string, want Runner) bool {
	if stateHash == "" {
		return false
	}
	if mechCacheHit(mechKey(root, stateHash, want)) {
		return true
	}
	wantScope, ok := runnerScope(want)
	path := mechCachePath()
	if !ok || path == "" {
		return false
	}
	prefix := mechKeyPrefix(root, stateHash)
	var have []runScope
	for key := range loadMechCache(path).Green {
		cmd, found := strings.CutPrefix(key, prefix)
		if !found {
			continue
		}
		if s, readable := scopeOfSuiteCommand(strings.Fields(cmd)); readable {
			have = append(have, s)
		}
	}
	return provenCovers(have, wantScope)
}

// mechCacheWrite serialises mechCacheAdd's read-modify-write of the cache file.
var mechCacheWrite sync.Mutex

// mechCacheAdd records a green run under key, pruning the oldest entries
// beyond mechCacheMax. Best-effort: any I/O failure just loses the cache win.
func mechCacheAdd(key string) {
	if key == "" {
		return
	}
	path := mechCachePath()
	if path == "" {
		return
	}
	// One writer at a time within this process: two gates recording at once
	// would each read the file before the other's rename and the later write
	// would drop the earlier green.
	mechCacheWrite.Lock()
	defer mechCacheWrite.Unlock()
	c := loadMechCache(path)
	if c.newer {
		return
	}
	c.Schema = StateSchema
	c.Green[key] = time.Now().UTC().Format(time.RFC3339)
	for len(c.Green) > mechCacheMax {
		oldestKey, oldestTS := "", ""
		for k, ts := range c.Green {
			if oldestKey == "" || ts < oldestTS {
				oldestKey, oldestTS = k, ts
			}
		}
		delete(c.Green, oldestKey)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	// By rename: a reader that caught this mid-truncate would quarantine a
	// live cache as corrupt and re-run every suite it was answering for.
	_ = writeFileAtomic(path, data)
}

// mechCacheAddUnmoved records a green run of r at root under before, the
// state hash taken BEFORE the run started, and only when the worktree still
// hashes to it afterwards. A green is a fact about the tree the run compiled.
// A tree that moved while the run was going (a `git merge` landing from
// another shell, a mutation written and restored mid-run) was not the one it
// compiled, whichever end of the run is hashed: hashing after names content
// the run never saw (#813: a merge's four new test files read cache-hit at
// that merge's own gate), and hashing before alone names content the run did
// not finish on. Unmoved, both ends agree and the green is recorded; moved,
// nothing is, and the next lookup runs the suite.
func mechCacheAddUnmoved(root, before string, r Runner) {
	if before == "" || worktreeStateHash(root) != before {
		return
	}
	mechCacheAdd(mechKey(root, before, r))
}

// worktreeStateHash fingerprints the content the mechanical suite actually
// sees: HEAD plus the content of every path that differs from HEAD or the
// index, every untracked (non-ignored) file, and every configuration file the
// suites read (see configFiles). It hashes path+content pairs, not diff text,
// so staging (`git add`) does not move the hash: a green recorded right after
// an edit is still valid at the commit that follows. Only those files are
// content-hashed, so the cost scales with the change, not the repo. Any git
// failure returns "", which callers treat as "no caching".
//
// The dirty set is one `git status` read fresh now: a caller that asks twice,
// to learn whether the tree moved between, gets two reads.
func worktreeStateHash(root string) string {
	c, st := gitx.FreshStatus(root)
	return stateHash(c, st)
}

// worktreeStateHashInBatch is worktreeStateHash over the dirty set the hook's
// batch has read already (gitx.BeginHook): the same bytes for the same tree,
// and no second status for a hook that asks several times in one instant.
func worktreeStateHashInBatch(root string) string {
	c, st := gitx.HookStatus(root)
	return stateHash(c, st)
}

// stateHash is the hash of the tree c sits in, for the dirty set st read from
// it; "" when either is missing or the repository has no commit.
func stateHash(c *igit.Client, st *igit.Status) string {
	if c == nil || st == nil {
		return ""
	}
	head, err := c.Head()
	if err != nil || head.SHA == "" {
		return ""
	}
	// Every path here is repo-root-relative, which is what status prints, and
	// is joined to the worktree's top directory, not to root: root routinely
	// lands on a Cargo workspace member crate's own subdirectory
	// (FindProjectRoot finds the nearest Cargo.toml, not the workspace root),
	// and the command this hash keys reaches past root, naming every crate
	// downstream of the touched one (#175, #813).
	base := c.Root()
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for _, e := range st.Entries {
		if e.Kind != igit.Ignored {
			add(e.Path)
			add(e.From)
		}
	}
	for _, p := range configFiles(base) {
		add(p)
	}
	sort.Strings(paths)

	h := sha256.New()
	fmt.Fprintf(h, "%s\n", head.SHA)
	for _, p := range paths {
		fmt.Fprintf(h, "%s\x00%s\n", p, fileContentStamp(filepath.Join(base, filepath.FromSlash(p))))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// fileContentStamp is the hex SHA-256 of a regular file's bytes; anything else
// (deleted, replaced by a directory, unreadable) stamps "gone" so its absence
// still shapes the hash.
func fileContentStamp(path string) string {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return "gone"
	}
	f, err := os.Open(path)
	if err != nil {
		return "gone"
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "gone"
	}
	return hex.EncodeToString(h.Sum(nil))
}

// configFiles lists, repo-relative and slash-separated, the files a suite
// genuinely reads that git may not track: dotenv files and anything under a
// config/ directory. The cache is shared across a repo's worktrees, so tracked
// content alone is not the whole fact: two lanes with the same sources and
// different .env are not the same proven green. A tracked one is listed too,
// which leaves the hash as sensitive and no less stable. Build output is
// deliberately excluded: hashing target/ would cost minutes per commit for
// something that changes on every build. A nested repository, which a lane
// kept under the tree is, has its own state and is not walked.
// bound: one walk of the worktree, target/, node_modules/ and .git skipped.
func configFiles(base string) []string {
	var out []string
	// absence-ok: an unreadable directory contributes no config file, as git's own listing skips it
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(base, path)
		if relErr != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "target", "node_modules":
				return filepath.SkipDir
			}
			if rel != "." {
				if _, statErr := os.Lstat(filepath.Join(path, ".git")); statErr == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		slash := filepath.ToSlash(rel)
		if strings.HasPrefix(d.Name(), ".env") || strings.HasPrefix(slash, "config/") || strings.Contains(slash, "/config/") {
			out = append(out, slash)
		}
		return nil
	})
	return out
}

// gitRead runs git in dir with a scrubbed environment and returns STDOUT only
// — unlike git() it never mixes stderr noise into a value used for hashing.
func gitRead(dir string, args ...string) (string, error) {
	out, err := lightOutput(run.Spec{Name: gitBinary(), Args: args, Dir: dir, Env: cleanGitEnvFor(dir, args...)})
	return string(out), err
}
