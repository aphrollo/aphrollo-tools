package github

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// The reads the escape verbs, the session-start line and the mutation gate
// make. Each keeps the argv those callers have always sent gh, so what gh is
// asked is unchanged; what moved is who builds the line, in what environment it
// runs and how its answer is read.

// read runs one gh call and answers its stdout. A failure carries gh's own
// words (its stderr, capped), not "exit status 1": that names none of the things
// that go wrong here — a label that does not exist, no auth, no network.
func (g *GitHub) read(args ...string) (string, error) {
	out, err := g.gh(args...)
	if err != nil {
		if said := strings.TrimSpace(string(out)); said != "" {
			return "", fmt.Errorf("gh %s: %w: %s", args[0], err, fit(said, 400))
		}
		return "", fmt.Errorf("gh %s: %w", args[0], err)
	}
	return string(out), nil
}

// payload is the JSON between the first open and the last close of a pair, the
// way gh prints it: a notice before it or a newline after it is not part of it.
func payload(out, open, closing string) (string, bool) {
	start, end := strings.Index(out, open), strings.LastIndex(out, closing)
	if start < 0 || end < start {
		return "", false
	}
	return out[start : end+1], true
}

type issueDoc struct {
	Number   int    `json:"number"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	State    string `json:"state"`
	ClosedAt string `json:"closedAt"`
	Author   struct {
		Login string `json:"login"`
	} `json:"author"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (d issueDoc) issue() host.Issue {
	is := host.Issue{Number: d.Number, Title: d.Title, Body: d.Body, State: d.State, Author: d.Author.Login}
	if t, err := time.Parse(time.RFC3339, d.ClosedAt); err == nil {
		is.ClosedAt = t
	}
	for _, l := range d.Labels {
		is.Labels = append(is.Labels, l.Name)
	}
	return is
}

// ListIssues lists the issues matching q.
func (g *GitHub) ListIssues(q host.IssueQuery) ([]host.Issue, error) {
	args := []string{"issue", "list"}
	if q.Label != "" {
		args = append(args, "--label", q.Label)
	}
	args = append(args, "--state", q.State)
	if q.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(q.Limit))
	}
	args = append(args, "--json", strings.Join(q.Fields, ","))
	out, err := g.read(args...)
	if err != nil {
		return nil, err
	}
	doc, ok := payload(out, "[", "]")
	if !ok {
		return nil, fmt.Errorf("reading gh issue list: no JSON list in %q", fit(strings.TrimSpace(out), 200))
	}
	var docs []issueDoc
	if err := json.Unmarshal([]byte(doc), &docs); err != nil {
		return nil, fmt.Errorf("reading gh issue list: %w", err)
	}
	issues := make([]host.Issue, 0, len(docs))
	for _, d := range docs {
		issues = append(issues, d.issue())
	}
	return issues, nil
}

// Issue reads one issue's labels and body.
func (g *GitHub) Issue(number string) (*host.Issue, error) {
	out, err := g.read("issue", "view", number, "--json", "labels,body")
	if err != nil {
		return nil, err
	}
	var d issueDoc
	if err := unmarshalObject(out, &d); err != nil {
		return nil, fmt.Errorf("reading issue #%s: %w", number, err)
	}
	is := d.issue()
	return &is, nil
}

// PRBody reads one PR's body.
func (g *GitHub) PRBody(ref string) (string, error) {
	out, err := g.read("pr", "view", ref, "--json", "body")
	if err != nil {
		return "", err
	}
	var doc struct {
		Body string `json:"body"`
	}
	if err := unmarshalObject(out, &doc); err != nil {
		return "", fmt.Errorf("reading PR #%s: %w", ref, err)
	}
	return doc.Body, nil
}

// PRClosure reads what a PR closes and the two ends of its diff. A commit
// message closes an issue as the body does, so all of them are read.
func (g *GitHub) PRClosure(ref string) (*host.PRFacts, error) {
	out, err := g.read("pr", "view", ref, "--json", "body,commits,baseRefOid,headRefOid")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Body    string `json:"body"`
		Commits []struct {
			MessageHeadline string `json:"messageHeadline"`
			MessageBody     string `json:"messageBody"`
		} `json:"commits"`
		BaseRefOid string `json:"baseRefOid"`
		HeadRefOid string `json:"headRefOid"`
	}
	if err := unmarshalObject(out, &doc); err != nil {
		return nil, fmt.Errorf("reading PR #%s: %w", ref, err)
	}
	facts := &host.PRFacts{Body: doc.Body, Base: doc.BaseRefOid, Head: doc.HeadRefOid}
	for _, c := range doc.Commits {
		facts.Commits = append(facts.Commits, host.CommitText{Headline: c.MessageHeadline, Body: c.MessageBody})
	}
	return facts, nil
}

// PRDiff is the PR's full patch.
func (g *GitHub) PRDiff(ref string) (string, error) {
	return g.read("pr", "diff", ref)
}

// unmarshalObject reads the JSON object out of what gh printed.
func unmarshalObject(out string, v any) error {
	doc, ok := payload(out, "{", "}")
	if !ok {
		doc = strings.TrimSpace(out)
	}
	return json.Unmarshal([]byte(doc), v)
}

// CheckState is the newest check run called name on the commit.
func (g *GitHub) CheckState(sha, name string) (status, conclusion string, found bool, err error) {
	out, err := g.read("api",
		"repos/{owner}/{repo}/commits/"+sha+"/check-runs?per_page=100&filter=latest",
		"--jq", `[.check_runs[] | select(.name=="`+name+`")] | first | if . == null then "" else .status + "/" + (.conclusion // "") end`)
	if err != nil {
		return "", "", false, err
	}
	state := strings.TrimSpace(out)
	if state == "" {
		return "", "", false, nil
	}
	status, conclusion, _ = strings.Cut(state, "/")
	return status, conclusion, true, nil
}

// ArtifactRun is the run that published the newest artifact called name. The
// artifacts endpoint filters on the name itself, so the lookup is one request
// however many reports the repository holds, and the first is the newest.
func (g *GitHub) ArtifactRun(name string) (int64, bool, error) {
	out, err := g.read("api",
		"repos/{owner}/{repo}/actions/artifacts?per_page=1&name="+name,
		"--jq", ".artifacts[0].workflow_run.id // empty")
	if err != nil {
		return 0, false, err
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return 0, false, nil
	}
	run, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("gh api answered %q, not a run id", fit(id, 80))
	}
	return run, true, nil
}

// DownloadArtifact puts the artifact called name that run published into dir.
func (g *GitHub) DownloadArtifact(run int64, name, dir string) error {
	_, err := g.read("run", "download", strconv.FormatInt(run, 10), "-n", name, "-D", dir)
	return err
}
