package cireuse

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	runner "github.com/aphrollo/aphrollo-tools/internal/run"
)

// treeArtifact is the artifact the pipeline's `changes` job uploads on a pull
// request and on a merge group: one file, `tree`, holding the tree its
// checkout tested.
const treeArtifact = "tested-tree"

// ghSource is Source over the gh CLI, which carries the job's GH_TOKEN.
type ghSource struct {
	Repo     string
	Workflow string
	// Run is gh itself; tests answer for it.
	Run func(args ...string) ([]byte, error)
	// TempDir is where an artifact is downloaded.
	TempDir func() string
}

func newGHSource(repo, workflow string) *ghSource {
	return &ghSource{Repo: repo, Workflow: workflow, Run: runGH, TempDir: os.TempDir}
}

func runGH(args ...string) ([]byte, error) {
	var stderr strings.Builder
	out, err := runner.LightOutput(runner.Spec{Name: "gh", Args: args, Stderr: &stderr, Timeout: ghTimeout})
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// ghTimeout bounds one gh call; a run download is the largest.
const ghTimeout = 2 * time.Minute

func (g *ghSource) get(dst any, format string, args ...any) error {
	body, err := g.Run("api", fmt.Sprintf(format, args...))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("the answer is not the JSON expected: %w", err)
	}
	return nil
}

func (g *ghSource) Pulls(sha string) ([]Pull, error) {
	var raw []struct {
		Number         int    `json:"number"`
		MergedAt       string `json:"merged_at"`
		MergeCommitSHA string `json:"merge_commit_sha"`
		Head           struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := g.get(&raw, "repos/%s/commits/%s/pulls?per_page=100", g.Repo, sha); err != nil {
		return nil, err
	}
	pulls := make([]Pull, 0, len(raw))
	for _, p := range raw {
		pulls = append(pulls, Pull{Number: p.Number, MergedAt: p.MergedAt, MergeCommitSHA: p.MergeCommitSHA, HeadSHA: p.Head.SHA})
	}
	return pulls, nil
}

func (g *ghSource) Pull(number int) (Pull, error) {
	var raw struct {
		Number         int    `json:"number"`
		MergedAt       string `json:"merged_at"`
		MergeCommitSHA string `json:"merge_commit_sha"`
		Head           struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := g.get(&raw, "repos/%s/pulls/%d", g.Repo, number); err != nil {
		return Pull{}, err
	}
	return Pull{Number: raw.Number, MergedAt: raw.MergedAt, MergeCommitSHA: raw.MergeCommitSHA, HeadSHA: raw.Head.SHA}, nil
}

func (g *ghSource) Runs(event, headSHA string) ([]Run, error) {
	var raw struct {
		Runs []struct {
			ID             int64  `json:"id"`
			Number         int    `json:"run_number"`
			Attempt        int    `json:"run_attempt"`
			Event          string `json:"event"`
			Path           string `json:"path"`
			Status         string `json:"status"`
			Conclusion     string `json:"conclusion"`
			HeadSHA        string `json:"head_sha"`
			URL            string `json:"html_url"`
			HeadRepository struct {
				FullName string `json:"full_name"`
			} `json:"head_repository"`
		} `json:"workflow_runs"`
	}
	if err := g.get(&raw, "repos/%s/actions/workflows/%s/runs?event=%s&head_sha=%s&per_page=100", g.Repo, path.Base(g.Workflow), event, headSHA); err != nil {
		return nil, err
	}
	runs := make([]Run, 0, len(raw.Runs))
	for _, r := range raw.Runs {
		runs = append(runs, Run{ID: r.ID, Number: r.Number, Attempt: r.Attempt, Event: r.Event, Path: r.Path,
			Status: r.Status, Conclusion: r.Conclusion, HeadSHA: r.HeadSHA, HeadRepo: r.HeadRepository.FullName, URL: r.URL})
	}
	return runs, nil
}

func (g *ghSource) Jobs(runID int64) ([]Job, error) {
	var raw struct {
		Jobs []struct {
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
			Steps      []struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
			} `json:"steps"`
		} `json:"jobs"`
	}
	if err := g.get(&raw, "repos/%s/actions/runs/%d/attempts/1/jobs?per_page=100", g.Repo, runID); err != nil {
		return nil, err
	}
	jobs := make([]Job, 0, len(raw.Jobs))
	for _, j := range raw.Jobs {
		job := Job{Name: j.Name, Conclusion: j.Conclusion}
		for _, s := range j.Steps {
			job.Steps = append(job.Steps, Step{Name: s.Name, Conclusion: s.Conclusion})
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (g *ghSource) TestedTree(runID int64) (string, error) {
	dir := filepath.Join(g.TempDir(), fmt.Sprintf("%s-%d", treeArtifact, runID))
	if _, err := g.Run("run", "download", fmt.Sprint(runID), "-R", g.Repo, "-n", treeArtifact, "-D", dir); err != nil {
		return "", err
	}
	body, err := os.ReadFile(filepath.Join(dir, "tree"))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}
