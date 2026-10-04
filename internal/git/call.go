package git

import (
	"bytes"
	"io"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// Call shapes one invocation beyond what Output answers: a deadline of its own
// (a fetch or a push outlasts the client's), and where the child's output goes
// (a push's progress meter streams to the operator). A nil writer discards
// that stream.
type Call struct {
	Timeout        time.Duration
	Stdout, Stderr io.Writer
}

// Do runs git with the call's deadline and writers and answers how it ended.
func (c *Client) Do(call Call, args ...string) error {
	c.spawns.Add(1)
	slots <- struct{}{}
	defer func() { <-slots }()
	return run.LightRun(c.spec(c.root, call, args))
}

// Combined runs git and answers its stdout and stderr together, in the order
// the child wrote them: the text a refusal or a conflict puts in front of the
// operator.
func (c *Client) Combined(args ...string) (string, error) {
	return c.CombinedFor(0, args...)
}

// CombinedFor is Combined with a deadline of its own, the client's when zero.
func (c *Client) CombinedFor(timeout time.Duration, args ...string) (string, error) {
	var out bytes.Buffer
	err := c.Do(Call{Timeout: timeout, Stdout: &out, Stderr: &out}, args...)
	return out.String(), err
}

// ResolveRef is the commit a full ref name (refs/heads/x, HEAD, a remote
// branch) names, read from the ref files, or "" when it names none. A
// repository whose refs are not files is asked.
func (c *Client) ResolveRef(ref string) string {
	if c.reftable {
		out, err := c.Output("rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}
	if ref == "HEAD" {
		h, err := c.Head()
		if err != nil {
			return ""
		}
		return h.SHA
	}
	return c.readRef(ref)
}

// RemoteURL is the URL of the named remote, or "" when there is none. The
// answer is kept: the URL does not change in the life of a verb.
func (c *Client) RemoteURL(name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if url, ok := c.remotes[name]; ok {
		return url
	}
	url := ""
	if out, err := c.Output("remote", "get-url", name); err == nil {
		url = strings.TrimSpace(out)
	}
	if c.remotes == nil {
		c.remotes = map[string]string{}
	}
	c.remotes[name] = url
	return url
}

// Resolves reports whether name is a ref git resolves by its short name
// (main, origin/main, a tag), as `rev-parse --verify --quiet` would, read from
// the ref files. A name that is not a plain ref name (a revision expression, an
// abbreviated commit) is not answered here.
func (c *Client) Resolves(name string) bool { return c.resolves(name) }
