package install

import "github.com/aphrollo/aphrollo-tools/internal/ghtransport"

// doctorGHTransport checks what this box can actually reach through `gh`:
// present on PATH, authenticated for REST, and — reported separately,
// non-fatally — whether GraphQL is reachable too. Found on a cloud
// container (#880): gh installed and authenticated fine, but `gh pr
// view/create/merge` route through GraphQL, which that environment refused
// with a 403; `workspace pr` failed on its first call with no earlier
// warning from doctor or install. The workspace verbs now route those calls
// over REST instead (so GraphQL being unavailable no longer blocks them),
// but a missing or unauthenticated gh still blocks everything gh-dependent,
// so that half FAILS the report; the transport split is a WARN naming which
// half is available.
func doctorGHTransport() DoctorCheck {
	c := DoctorCheck{Name: "gh transport"}
	p := ghtransport.Run()
	if !p.Ready() {
		c.Detail = p.FixLine()
		return c
	}
	c.OK = true
	c.Detail = p.TransportLine()
	if !p.GraphQLOK {
		c.Warn = true
	}
	return c
}
