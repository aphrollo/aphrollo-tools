package report

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/measure"
)

// Tracker is the part of the code host the delivery uses.
type Tracker interface {
	host.Issues
	host.IssueCloser
}

// DeliverOptions say where and how the report is delivered. Dry previews: it
// lists the issues but opens and closes none.
type DeliverOptions struct {
	Repo   string
	Labels []string
	Dry    bool
}

// issueListLimit bounds the listing the delivery reads titles from.
const issueListLimit = 1000

var reportTitle = regexp.MustCompile(`^Report \d{4}-W\d{2}$`)

// ABReadyTitle is the title of the one issue that tells the owner both arms
// have the lanes the A/B needs.
func ABReadyTitle(repo string) string { return "A/B ready: " + repo }

// Deliver opens this week's report as one issue, closes the previous weeks'
// report issues with a comment linking it, and, the first time both A/B arms
// have MinABLanes lanes, opens the A/B ready issue. Each step is idempotent: a
// step already done is a [skip] and changes nothing. A listing that fails is an
// error before anything is opened, so a blind open cannot duplicate.
func Deliver(t Tracker, rep Report, o DeliverOptions) ([]string, error) {
	issues, err := t.ListIssues(host.IssueQuery{State: "all", Limit: issueListLimit, Fields: []string{"number", "title", "state"}})
	if err != nil {
		return nil, fmt.Errorf("listing issues: %w", err)
	}
	var out []string
	say := func(tag, format string, a ...any) { out = append(out, "["+tag+"] "+fmt.Sprintf(format, a...)) }
	find := func(title string) (host.Issue, bool) {
		for _, is := range issues {
			if is.Title == title {
				return is, true
			}
		}
		return host.Issue{}, false
	}
	open := func(title, body string) (ref string, err error) {
		if o.Dry {
			say("dry", "would open %q with %d lines of body", title, strings.Count(body, "\n")+1)
			return "", nil
		}
		for _, l := range o.Labels {
			_ = t.EnsureLabel(l, "0e8a16", "")
		}
		url, err := t.OpenIssue(host.IssueRequest{Title: title, Body: body, Labels: o.Labels})
		if err != nil {
			return "", err
		}
		say("ok", "opened %q: %s", title, url)
		return url, nil
	}

	ref := ""
	if is, ok := find(rep.Title); ok {
		say("skip", "%s (#%d exists)", rep.Title, is.Number)
		ref = "#" + strconv.Itoa(is.Number)
	} else if ref, err = open(rep.Title, rep.Text()); err != nil {
		return out, err
	}
	for _, is := range issues {
		if !reportTitle.MatchString(is.Title) || is.Title == rep.Title || !strings.EqualFold(is.State, "open") {
			continue
		}
		if o.Dry || ref == "" {
			say("dry", "would close #%d (%s)", is.Number, is.Title)
			continue
		}
		if err := t.CloseIssue(is.Number, "Superseded by "+ref+" ("+rep.Title+")."); err != nil {
			return out, err
		}
		say("ok", "closed #%d (%s)", is.Number, is.Title)
	}

	title := ABReadyTitle(o.Repo)
	switch is, exists := find(title); {
	case !rep.ABTotal.Decidable:
		say("skip", "%s: not reached (%s)", title, abLanes(rep.ABTotal))
	case exists:
		say("skip", "%s (#%d exists)", title, is.Number)
	default:
		body := "Both arms of the red-to-green A/B have " + strconv.Itoa(measure.MinABLanes) +
			" lanes or more: " + abLanes(rep.ABTotal) + ".\n\nThis is the signal to resume: read " +
			"`aphrollo stats --ab` and the report, then decide enforce or warn.\n"
		if _, err := open(title, body); err != nil {
			return out, err
		}
	}
	return out, nil
}
