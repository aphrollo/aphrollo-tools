package tdd

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
	Green map[string]string `json:"green"` // mechKey → RFC3339 time of the green run
}

func mechCachePath() string {
	dir := stateDir()
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
func mechKey(root, stateHash string, r Runner) string {
	return mechKeyRepo(root) + "\x00" + stateHash + "\x00" + r.Cmd + " " + strings.Join(r.Args, " ")
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
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, c)
		if c.Green == nil {
			c.Green = map[string]string{}
		}
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
	_ = os.WriteFile(path, data, 0o600)
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
	changed, err := gitRead(root, "diff", "HEAD", "--name-only", "--no-color")
	if err != nil {
		return ""
	}
	untracked, err := gitRead(root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return ""
	}

	seen := map[string]bool{}
	var paths []string
	for _, out := range []string{changed, untracked, ignoredConfig(root)} {
		for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
			if line != "" && !seen[line] {
				seen[line] = true
				paths = append(paths, line)
			}
		}
	}
	sort.Strings(paths)

	// Batch-hash the paths that are regular files; anything else (deleted,
	// replaced by a directory) is stamped "gone" so its absence still shapes
	// the hash.
	var present []string
	for _, p := range paths {
		if fi, err := os.Lstat(filepath.Join(root, p)); err == nil && fi.Mode().IsRegular() {
			present = append(present, p)
		}
	}
	blobs, ok := blobHashes(root, present)
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
	out, err := gitRead(root, "ls-files", "--others", "--ignored", "--exclude-standard", "--",
		":(glob).env*", ":(glob)**/.env*", ":(glob)config/**", ":(glob)**/config/**",
		":(glob,exclude)**/target/**", ":(glob,exclude)**/node_modules/**")
	if err != nil {
		return ""
	}
	return out
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
