package git

import (
	"os"
	"path/filepath"
	"strings"
)

// conventionalTrunks are the names tried last, each only when it resolves: a
// branch merely named `master` beside a real trunk of another name is not
// trunk.
var conventionalTrunks = []string{"main", "master"}

// Trunk names the branch a lane is measured against: what the remote calls its
// default (`origin/HEAD`, as `origin/<name>`), else the configured
// `init.defaultBranch`, else a conventional name; each candidate but the first
// must resolve. "" means it cannot tell, and no name is ever assumed. The
// answer is kept once it names a branch; a miss is asked again, so a
// remote that names its default later is seen. The first route reads a file; only the
// second asks git, and only when the remote names no default.
func (c *Client) Trunk() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.trunkDone {
		if name := c.resolveTrunk(); name != "" {
			c.trunk, c.trunkDone = name, true
		}
	}
	return c.trunk
}

func (c *Client) resolveTrunk() string {
	if name := c.originHead(); name != "" {
		return name
	}
	if out, err := c.Output("config", "--get", "init.defaultBranch"); err == nil {
		if name := strings.TrimSpace(out); name != "" && c.resolves(name) {
			return name
		}
	}
	for _, name := range conventionalTrunks {
		if c.resolves(name) {
			return name
		}
	}
	return ""
}

// originHead is `symbolic-ref --short refs/remotes/origin/HEAD`: "origin/main".
func (c *Client) originHead() string {
	if c.reftable {
		out, err := c.Output("symbolic-ref", "--short", "refs/remotes/origin/HEAD")
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}
	raw, err := os.ReadFile(filepath.Join(c.commonDir, "refs", "remotes", "origin", "HEAD"))
	if err != nil {
		return ""
	}
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "ref: refs/remotes/")
	if !ok {
		return ""
	}
	return ref
}

// resolves reports whether name is a ref git resolves by its short name, as
// `rev-parse --verify --quiet` would, for a name git would accept as a ref.
func (c *Client) resolves(name string) bool {
	if strings.Contains(name, "..") {
		return false
	}
	if c.reftable {
		_, err := c.Output("rev-parse", "--verify", "--quiet", name)
		return err == nil
	}
	for _, ref := range []string{name, "refs/" + name, "refs/tags/" + name, "refs/heads/" + name, "refs/remotes/" + name, "refs/remotes/" + name + "/HEAD"} {
		if c.readRef(ref) != "" {
			return true
		}
	}
	return false
}
