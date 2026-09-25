package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/undercover"
)

const ciUndercoverUsage = `usage: aphrollo ci undercover-text [--event <path>] [--repo <dir>]

The undercover-text CI job: reads the pull_request, issue_comment or
pull_request_review_comment event at --event (default $GITHUB_EVENT_PATH),
then the PR title and body, every issue comment and every review comment as
they landed on GitHub. A trailing footer carrying a tell is stripped and the
text rewritten with $GITHUB_TOKEN; any other hit fails the job, quoting the
line, and is left unedited. Inert, and never reaches GitHub, unless --repo
(default .) sets undercover = true.
`

// ciUndercoverTimeout bounds the whole run: a handful of GETs and PATCHes.
const ciUndercoverTimeout = 5 * time.Minute

// ciUndercoverClient builds the REST client; tests replace it.
var ciUndercoverClient = func(token string) undercover.Client {
	base := strings.TrimSpace(os.Getenv("GITHUB_API_URL"))
	if base == "" {
		base = "https://api.github.com"
	}
	return undercover.RESTClient{BaseURL: base, Token: token, HTTP: &http.Client{Timeout: time.Minute}}
}

func runCIUndercoverText(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci undercover-text", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, ciUndercoverUsage) }
	eventPath := fs.String("event", os.Getenv("GITHUB_EVENT_PATH"), "the GitHub event JSON")
	repo := fs.String("repo", ".", "the checkout whose manifest says whether undercover is on")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	tells, on := undercover.Load(*repo)
	if !on {
		fmt.Fprintf(stdout, "undercover-text: %s does not set undercover = true; nothing to check\n", *repo)
		return 0
	}
	if *eventPath == "" {
		fmt.Fprint(stderr, "aphrollo ci undercover-text: no event: pass --event or set GITHUB_EVENT_PATH\n\n"+ciUndercoverUsage)
		return 2
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Fprintln(stderr, "aphrollo ci undercover-text: GITHUB_TOKEN is not set")
		return 2
	}
	payload, err := os.ReadFile(*eventPath)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo ci undercover-text: %v\n", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), ciUndercoverTimeout)
	defer cancel()
	res, err := undercover.CheckEvent(ctx, payload, tells, ciUndercoverClient(token))
	for _, s := range res.Stripped {
		fmt.Fprintln(stdout, "undercover-text: "+s)
	}
	for _, f := range res.Failures {
		fmt.Fprintln(stderr, f)
	}
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo ci undercover-text: %v\n", err)
		return 1
	}
	if len(res.Failures) > 0 {
		return 1
	}
	return 0
}
