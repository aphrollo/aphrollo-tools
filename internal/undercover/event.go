package undercover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Client is the GitHub REST seam CheckEvent reads and edits through: a path
// relative to the API root, JSON in and out.
type Client interface {
	Get(ctx context.Context, path string) ([]byte, error)
	Patch(ctx context.Context, path string, fields map[string]string) error
}

// Result is what one run found: the footer lines it stripped (one entry per
// edited text) and the refusals it could not fix by stripping.
type Result struct {
	Stripped []string
	Failures []string
}

// pageSize is GitHub's largest page; maxPages bounds a run on
// a pathological thread at 3000 comments or commits.
const (
	pageSize = 100
	maxPages = 30
)

// event is the part of a pull_request, issue_comment or
// pull_request_review_comment payload the check reads.
type event struct {
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	PullRequest *struct {
		Number int `json:"number"`
	} `json:"pull_request"`
	Issue *struct {
		Number      int              `json:"number"`
		PullRequest *json.RawMessage `json:"pull_request"`
	} `json:"issue"`
}

// CheckEvent reads what landed on GitHub for the issue or PR the event names
// — the PR title and body, every issue comment and every review comment — and
// judges each against the list. A trailing footer that carries a tell is
// stripped and the text rewritten; any other hit is a failure, and its text
// is left exactly as it is.
func CheckEvent(ctx context.Context, payload []byte, tells List, c Client) (Result, error) {
	var ev event
	if err := json.Unmarshal(payload, &ev); err != nil {
		return Result{}, fmt.Errorf("reading the event: %w", err)
	}
	repo := ev.Repository.FullName
	number, isPR := 0, false
	switch {
	case ev.PullRequest != nil:
		number, isPR = ev.PullRequest.Number, true
	case ev.Issue != nil:
		number, isPR = ev.Issue.Number, ev.Issue.PullRequest != nil
	}
	if repo == "" || number == 0 {
		return Result{}, errors.New("the event names no repository and issue or pull request")
	}
	var res Result
	if isPR {
		if err := checkPR(ctx, c, tells, repo, number, &res); err != nil {
			return res, err
		}
	}
	if err := checkComments(ctx, c, tells, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, number),
		fmt.Sprintf("/repos/%s/issues/comments/", repo), "issue comment", &res); err != nil {
		return res, err
	}
	if isPR {
		if err := checkComments(ctx, c, tells, fmt.Sprintf("/repos/%s/pulls/%d/comments", repo, number),
			fmt.Sprintf("/repos/%s/pulls/comments/", repo), "review comment", &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

func checkPR(ctx context.Context, c Client, tells List, repo string, number int, res *Result) error {
	path := fmt.Sprintf("/repos/%s/pulls/%d", repo, number)
	raw, err := c.Get(ctx, path)
	if err != nil {
		return err
	}
	var pr struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(raw, &pr); err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if h, hit := tells.Text(pr.Title); hit {
		res.Failures = append(res.Failures, TextRefusal("PR title", h))
	}
	if err := judgeText(ctx, c, tells, path, "PR body", pr.Body, res); err != nil {
		return err
	}
	return checkCommits(ctx, c, tells, path+"/commits", res)
}

// checkCommits judges every commit on the PR — author, committer and
// Co-authored-by trailers — because a squash merge copies each commit's
// author into main as a co-author. A commit is history, not text: it is
// never edited here, only refused.
func checkCommits(ctx context.Context, c Client, tells List, listPath string, res *Result) error {
	return pages(ctx, c, listPath, func(path string, raw []byte) (int, error) {
		var commits []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Author    struct{ Name, Email string } `json:"author"`
				Committer struct{ Name, Email string } `json:"committer"`
				Message   string                       `json:"message"`
			} `json:"commit"`
		}
		if err := json.Unmarshal(raw, &commits); err != nil {
			return 0, fmt.Errorf("reading %s: %w", path, err)
		}
		for _, cm := range commits {
			author := cm.Commit.Author.Name + " <" + cm.Commit.Author.Email + ">"
			committer := cm.Commit.Committer.Name + " <" + cm.Commit.Committer.Email + ">"
			if h, hit := tells.CommitTell(author, committer, cm.Commit.Message); hit {
				res.Failures = append(res.Failures, fmt.Sprintf(
					"undercover: PR commit %.12s's %s %q carries %q; set your own git identity, rewrite the commit without it and force-push the branch.",
					cm.SHA, h.Field, h.Value, h.Tell))
			}
		}
		return len(commits), nil
	})
}

func checkComments(ctx context.Context, c Client, tells List, listPath, editPrefix, kind string, res *Result) error {
	return pages(ctx, c, listPath, func(path string, raw []byte) (int, error) {
		var comments []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		}
		if err := json.Unmarshal(raw, &comments); err != nil {
			return 0, fmt.Errorf("reading %s: %w", path, err)
		}
		for _, cm := range comments {
			label := fmt.Sprintf("%s %d", kind, cm.ID)
			if err := judgeText(ctx, c, tells, fmt.Sprintf("%s%d", editPrefix, cm.ID), label, cm.Body, res); err != nil {
				return 0, err
			}
		}
		return len(comments), nil
	})
}

// pages reads a list endpoint page by page, handing each page to read, which
// answers how many items it held; a short page is the last one, and
// maxPages bounds the rest.
func pages(ctx context.Context, c Client, listPath string, read func(path string, raw []byte) (int, error)) error {
	for i := range maxPages {
		path := fmt.Sprintf("%s?per_page=%d&page=%d", listPath, pageSize, i+1)
		raw, err := c.Get(ctx, path)
		if err != nil {
			return err
		}
		n, err := read(path, raw)
		if err != nil {
			return err
		}
		if n < pageSize {
			return nil
		}
	}
	return nil
}

// judgeText strips a trailing footer and rewrites the text only when what is
// left carries no tell; otherwise the first hit is a failure and nothing is
// edited, so a text is never half-fixed behind the author's back.
func judgeText(ctx context.Context, c Client, tells List, editPath, kind, body string, res *Result) error {
	kept, stripped := StripFooter(body, tells)
	if h, hit := tells.Text(kept); hit {
		res.Failures = append(res.Failures, TextRefusal(kind, h))
		return nil
	}
	if len(stripped) == 0 {
		return nil
	}
	if err := c.Patch(ctx, editPath, map[string]string{"body": kept}); err != nil {
		var he *HTTPError
		if errors.As(err, &he) && he.Status == http.StatusForbidden {
			res.Failures = append(res.Failures, fmt.Sprintf(
				"undercover: the %s ends in a tool footer, and GitHub refused to rewrite it (403): the workflow token is read-only, as it is on a fork PR. Remove it by hand:\n    %s",
				kind, strings.Join(stripped, "\n    ")))
			return nil
		}
		return fmt.Errorf("rewriting the %s: %w", kind, err)
	}
	res.Stripped = append(res.Stripped, fmt.Sprintf("%s: stripped %q", kind, stripped))
	return nil
}
