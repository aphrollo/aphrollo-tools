// Package ghtransport probes what this box can actually reach through the
// `gh` CLI: whether the binary is on PATH at all, whether it is authenticated
// for REST (`gh api user`), and whether GraphQL is reachable too (`gh api
// graphql`) — some environments proxy or block GraphQL specifically while
// REST works fine (issue #880), so the two are probed separately rather than
// inferred from one call. Every probe is bounded: a hung credential prompt
// or network stall fails fast and readably instead of hanging the caller.
package ghtransport

import (
	"os/exec"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	childrun "github.com/aphrollo/aphrollo-tools/internal/run"
)

// probeTimeout bounds each gh call this package makes. A var, not a const,
// so a test can shrink it and prove the deadline actually fires.
var probeTimeout = 10 * time.Second

// Probe is what this box can reach right now.
type Probe = host.Probe

// Run probes gh's presence and both transports, each under probeTimeout —
// the full picture a `gate doctor` report wants. It looks gh up on PATH
// itself (never a caller-supplied path) so it reflects exactly what an
// ordinary gh-dependent verb would resolve.
func Run() Probe { return run(true) }

// RunRESTOnly probes presence and REST auth only, skipping the GraphQL
// round trip — what a verb's own preflight (requireGH) actually needs: every
// gh call the workspace verbs make now goes over REST (#880's cold-review
// round), so a verb has no reason to spend a probe, or wait out a timeout,
// on a transport it never uses. Doctor still probes both, since reporting
// GraphQL's own reachability is useful independent of what today's verbs use.
func RunRESTOnly() Probe { return run(false) }

func run(checkGraphQL bool) Probe {
	var p Probe
	path, err := exec.LookPath("gh")
	if err != nil {
		return p
	}
	p.Present = true
	out, err := runGH(path, "api", "user")
	if err != nil {
		p.Detail = strings.TrimSpace(string(out))
		return p
	}
	p.RESTOK = true
	if !checkGraphQL {
		return p
	}
	if _, err := runGH(path, "api", "graphql", "-f", "query=query{viewer{login}}"); err == nil {
		p.GraphQLOK = true
	}
	return p
}

// runGH runs one gh subcommand under probeTimeout, returning combined
// stdout+stderr for the diagnostic — never parsed as data, only ever shown
// or compared to "" for presence.
func runGH(bin string, args ...string) ([]byte, error) {
	return childrun.LightCombined(childrun.Spec{Name: bin, Args: args, Timeout: probeTimeout})
}
