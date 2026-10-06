package report

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

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
	// Now dates the week's issue: its title is Title(Now).
	Now time.Time
	// Author is the login the host acts as: an earlier report issue is closed
	// only if it carries the report label or was opened by this account.
	Author string
	// Refuse is the undercover check of the repo: it returns a refusal line for
	// a title and body that may not be published, "" when they may.
	Refuse func(title, body string) string
}

// issueListLimit bounds the listing the delivery reads titles from.
const issueListLimit = 1000

// reportLabel is the label the report issues carry, and the one an earlier
// report issue must carry to be closed by a later one.
const reportLabel = "report"

var reportTitle = regexp.MustCompile(`^Report \d{4}-W\d{2}$`)

// ABReadyTitle is the title of the one issue that tells the owner both arms
// have the lanes the A/B needs.
func ABReadyTitle(repo string) string { return "A/B ready: " + repo }

// ErrRefused is a title or body the undercover check will not let out.
var ErrRefused = errors.New("refused by the undercover check")

// Deliver opens this week's report as one issue, closes the previous weeks'
// report issues with a comment linking it, and, the first time both A/B arms
// have MinABLanes lanes, opens the A/B ready issue. Each step is idempotent: a
// step already done is a [skip] and changes nothing. The issues are listed
// first and the report is built only when something is to be opened, so a
// week already filed costs one listing. A listing that fails is an error before
// anything is opened, so a blind open cannot duplicate.
//
// What is published is Published(): model ids are replaced by their size, and
// a text the undercover check refuses is retried without the session usage and
// then refused, with its line, never skipped without a word.
func Deliver(t Tracker, build func(abReady bool) Report, o DeliverOptions) ([]string, error) {
	issues, err := t.ListIssues(host.IssueQuery{State: "all", Limit: issueListLimit, Fields: []string{"number", "title", "state", "labels", "author"}})
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
	title, abTitle := Title(o.Now), ABReadyTitle(o.Repo)
	weekIssue, weekDone := find(title)
	abIssue, abDone := find(abTitle)

	var rep Report
	if !weekDone || !abDone {
		rep = build(abDone)
	}
	open := func(title, body string, withheld func() string) (string, error) {
		body, err := cleared(o, title, body, withheld)
		if err != nil {
			return "", err
		}
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
	if weekDone {
		say("skip", "%s (#%d exists)", title, weekIssue.Number)
		ref = "#" + strconv.Itoa(weekIssue.Number)
	} else {
		rep.Title = title
		var err error
		ref, err = open(title, rep.Published().Text(), func() string { return rep.Published().WithoutUsage().Text() })
		if err != nil {
			return out, err
		}
	}
	for _, is := range issues {
		if !reportTitle.MatchString(is.Title) || is.Title == title || !strings.EqualFold(is.State, "open") || !ours(is, o.Author) {
			continue
		}
		if o.Dry || ref == "" {
			say("dry", "would close #%d (%s)", is.Number, is.Title)
			continue
		}
		if err := t.CloseIssue(is.Number, "Superseded by "+ref+" ("+title+")."); err != nil {
			return out, err
		}
		say("ok", "closed #%d (%s)", is.Number, is.Title)
	}

	switch {
	case abDone:
		say("skip", "%s (#%d exists)", abTitle, abIssue.Number)
	case !rep.ABTotal.Decidable:
		say("skip", "%s: not reached (%s)", abTitle, abLanes(rep.ABTotal))
	default:
		body := "Both arms of the red-to-green A/B have " + strconv.Itoa(measure.MinABLanes) +
			" lanes or more: " + abLanes(rep.ABTotal) + ".\n\nThis is the signal to resume: read " +
			"`aphrollo stats --ab` and the report, then decide enforce or warn.\n"
		if _, err := open(abTitle, body, nil); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ours is whether an earlier report issue is this tool's: it carries the report
// label or was opened by the account the host acts as. Another person's issue
// that happens to be titled like a report is left alone.
func ours(is host.Issue, author string) bool {
	return slices.Contains(is.Labels, reportLabel) || (author != "" && is.Author == author)
}

// cleared runs the undercover check on a title and body. A refused body is
// retried through withheld, the same text without its session usage, and said
// so in the text; a title, or a body still refused, is an error naming the
// refusal. The unattended path therefore redacts or fails, and never opens text
// the check would refuse, nor drops it without a line.
func cleared(o DeliverOptions, title, body string, withheld func() string) (string, error) {
	if o.Refuse == nil {
		return body, nil
	}
	if line := o.Refuse(title, ""); line != "" {
		return "", fmt.Errorf("%w: %s", ErrRefused, line)
	}
	line := o.Refuse("", body)
	if line == "" {
		return body, nil
	}
	if withheld != nil {
		body = withheld()
		if l := o.Refuse("", body); l == "" {
			return body, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrRefused, line)
}
