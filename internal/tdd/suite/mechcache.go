package suite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	out, err := gitRead(root, "rev-parse", "--show-prefix")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// mechKeyRepo resolves root to the identity the cache keys on: the repo's
// git common dir, or root itself when git cannot answer.
func mechKeyRepo(root string) string {
	out, err := git(root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return root
	}
	dir := strings.TrimSpace(out)
	if dir == "" {
		return root
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
// sees: HEAD plus the blob content of every tracked file that differs from
// HEAD and every untracked (non-ignored) file. It hashes path+blob pairs, not
// diff text, so staging (`git add`) does not move the hash — a green recorded
// right after an edit is still valid at the commit that follows. Only the
// delta files are content-hashed, so the cost scales with the change, not the
// repo. Any git failure returns "", which callers treat as "no caching".
func worktreeStateHash(root string) string {
	head, err := gitRead(root, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	// `git diff --name-only` prints REPO-ROOT-relative paths by default,
	// regardless of cwd; `--full-name` makes `ls-files` match that base
	// instead of its natural cwd-relative one. Both must agree, because
	// `git hash-object --stdin-paths` below ALSO always resolves its input
	// relative to the repo root, ignoring cwd entirely (unlike a bare path
	// argument) — verified empirically, undocumented quirk. root routinely
	// lands on a Cargo workspace member crate's own subdirectory
	// (FindProjectRoot finds the nearest Cargo.toml, not the workspace
	// root), so anchoring the Lstat/hash step at root itself either doubled
	// a repo-root-relative path into a nonexistent one, or fed a
	// cwd-relative path to hash-object where it silently resolved against
	// the wrong file (#175).
	// -z: git quotes a path containing a byte >= 0x80 (or other "unusual"
	// bytes) as a C-quoted string by default (core.quotePath defaults to
	// true, git-config(1)) — e.g. `"caf\303\251.rs"`, surrounding quotes and
	// octal escapes literal. `-z` makes git emit raw, unquoted, NUL-separated
	// paths regardless of core.quotePath, so the Lstat/hash-object join below
	// never has to un-quote. Without it a quoted literal never matches a real
	// file, os.Lstat fails, and the path is stamped "gone" no matter what its
	// content changes to — the same cache-poisoning shape #175 fixed, reached
	// by this route instead (#175).
	changed, err := gitRead(root, "diff", "HEAD", "--name-only", "--no-color", "-z")
	if err != nil {
		return ""
	}
	// Every path here is repo-root-relative, so the join and the
	// hash-object call below anchor there too, not at root.
	base := RepoRoot(root)
	if base == "" {
		base = root
	}
	// Untracked and ignored-config files are listed across the WHOLE repo,
	// like the diff above, and not from root: `ls-files` lists only what
	// sits under the directory it runs in, and the command this hash keys
	// reaches past root — a cargo run from the workspace names every crate
	// downstream of the touched one and compiles their tests/*.rs whether
	// git tracks them or not (#813).
	untracked, err := gitRead(base, "ls-files", "--others", "--exclude-standard", "--full-name", "-z")
	if err != nil {
		return ""
	}

	seen := map[string]bool{}
	var paths []string
	for _, out := range []string{changed, untracked, ignoredConfig(base)} {
		for _, p := range splitNulPaths(out) {
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	sort.Strings(paths)

	// Batch-hash the paths that are regular files; anything else (deleted,
	// replaced by a directory) is stamped "gone" so its absence still shapes
	// the hash.
	var present []string
	for _, p := range paths {
		if fi, err := os.Lstat(filepath.Join(base, p)); err == nil && fi.Mode().IsRegular() {
			present = append(present, p)
		}
	}
	blobs, ok := blobHashes(base, present)
	if !ok {
		return ""
	}

	h := sha256.New()
	fmt.Fprintf(h, "%s\n", strings.TrimSpace(head))
	for _, p := range paths {
		blob, ok := blobs[p]
		if !ok {
			blob = "gone"
		}
		fmt.Fprintf(h, "%s\x00%s\n", p, blob)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ignoredConfig lists the IGNORED files a suite genuinely reads: dotenv
// files and anything under a config/ directory. The cache is shared across a
// repo's worktrees, so tracked content alone is not the whole fact — two
// lanes with the same sources and different .env are not the same proven
// green. Build output is deliberately excluded: hashing target/ would cost
// minutes per commit for something that changes on every build.
// bound: pathspec-limited to dotenv + config/ trees, target/ and
// node_modules/ excluded.
func ignoredConfig(root string) string {
	out, err := gitRead(root, "ls-files", "--others", "--ignored", "--exclude-standard", "--full-name", "-z", "--",
		":(glob).env*", ":(glob)**/.env*", ":(glob)config/**", ":(glob)**/config/**",
		":(glob,exclude)**/target/**", ":(glob,exclude)**/node_modules/**")
	if err != nil {
		return ""
	}
	return out
}

// splitNulPaths splits a `-z`-terminated git path list. Each path (including
// the last) is followed by a trailing NUL, so a naive split on "\x00" leaves
// one empty trailing field; dropped here rather than left for a caller to
// forget, since an empty-string path joined against a base directory would
// Lstat the base itself and stamp IT "gone" in the hash. Empty input yields
// an empty path set, never a slice holding one empty string.
func splitNulPaths(s string) []string {
	if s == "" {
		return nil
	}
	fields := strings.Split(s, "\x00")
	if n := len(fields); n > 0 && fields[n-1] == "" {
		fields = fields[:n-1]
	}
	return fields
}

// blobHashes returns the git blob hash of each file via one batched
// `hash-object --stdin-paths` call. files are root-relative regular files.
func blobHashes(root string, files []string) (map[string]string, bool) {
	out := map[string]string{}
	if len(files) == 0 {
		return out, true
	}
	raw, err := gitReadStdin(root, strings.NewReader(strings.Join(files, "\n")+"\n"),
		"hash-object", "--stdin-paths")
	if err != nil {
		return nil, false
	}
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) != len(files) {
		return nil, false
	}
	for i, f := range files {
		out[f] = strings.TrimSpace(lines[i])
	}
	return out, true
}

// gitRead runs git in dir with a scrubbed environment and returns STDOUT only
// — unlike git() it never mixes stderr noise into a value used for hashing.
func gitRead(dir string, args ...string) (string, error) {
	return gitReadStdin(dir, nil, args...)
}

func gitReadStdin(dir string, stdin *strings.Reader, args ...string) (string, error) {
	cmd := exec.Command(gitBinary(), args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.Output()
	return string(out), err
}
