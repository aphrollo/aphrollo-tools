package ghworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// KV is one ordered key/value pair: an env entry or a job output.
type KV struct{ Key, Val string }

// Workflow is one workflow file as local CI needs it.
type Workflow struct {
	File    string
	Name    string
	Env     []KV
	WorkDir string // defaults.run.working-directory
	Shell   string // defaults.run.shell
	Jobs    []*Job
	// Notes are things the file declares that local CI does not evaluate, such
	// as a paths filter on the pull_request trigger.
	Notes []string
}

// Job is one job. Unsupported is set for a job local CI cannot run here
// (services, a container, a reusable workflow); it is skipped and said so.
type Job struct {
	ID          string
	Name        string
	If          string
	Needs       []string
	Env         []KV
	WorkDir     string
	Shell       string
	Steps       []*Step
	TimeoutMin  int
	Strategy    *Node
	Outputs     []KV
	Unsupported string
	Line        int
}

// Step is one step: a run script or a uses action.
type Step struct {
	ID              string
	Name            string
	If              string
	Run             string
	Uses            string
	WorkDir         string
	Shell           string
	Env             []KV
	ContinueOnError string
	Line            int
}

// Label is how the step is named in output: its name, else what it does.
func (s *Step) Label() string {
	switch {
	case s.Name != "":
		return s.Name
	case s.Uses != "":
		return s.Uses
	case s.Run != "":
		first, _, _ := strings.Cut(strings.TrimSpace(s.Run), "\n")
		return first
	}
	return "step at line " + strconv.Itoa(s.Line)
}

var prMention = regexp.MustCompile(`(?m)\bpull_request\b`)

// LoadDir reads every workflow under repo/.github/workflows that runs on a
// pull_request, in file-name order. skipped names each file it set aside and
// why. A file this reader cannot parse is refused when it mentions
// pull_request at all, since it might be one that runs.
func LoadDir(repo string) (flows []*Workflow, skipped []string, err error) {
	dir := filepath.Join(repo, ".github", "workflows")
	var files []string
	for _, pat := range []string{"*.yml", "*.yaml"} {
		m, _ := filepath.Glob(filepath.Join(dir, pat))
		files = append(files, m...)
	}
	sort.Strings(files)
	for _, f := range files {
		data, rerr := os.ReadFile(f)
		if rerr != nil {
			return nil, nil, fmt.Errorf("reading %s: %w", f, rerr)
		}
		wf, onPR, perr := Parse(filepath.Base(f), string(data))
		switch {
		case perr != nil && prMention.Match(data):
			return nil, nil, fmt.Errorf("%s: %w", filepath.Base(f), perr)
		case perr != nil:
			skipped = append(skipped, fmt.Sprintf("%s: unreadable (%v), and it never mentions pull_request", filepath.Base(f), perr))
		case !onPR:
			skipped = append(skipped, filepath.Base(f)+": does not run on pull_request")
		default:
			flows = append(flows, wf)
		}
	}
	return flows, skipped, nil
}

// Parse reads one workflow file and reports whether it runs on pull_request.
func Parse(file, src string) (wf *Workflow, onPR bool, err error) {
	root, err := ParseYAML(src)
	if err != nil {
		return nil, false, err
	}
	if root.Kind != KindMap {
		return nil, false, fmt.Errorf("a workflow file is a map at the top level")
	}
	wf = &Workflow{File: file, Name: root.Get("name").Text()}
	if wf.Name == "" {
		wf.Name = file
	}
	onPR, wf.Notes = pullRequestTrigger(root.Get("on"))
	if wf.Env, err = readEnv(root.Get("env")); err != nil {
		return nil, false, fmt.Errorf("env: %w", err)
	}
	wf.WorkDir, wf.Shell = readDefaults(root.Get("defaults"))
	jobs := root.Get("jobs")
	if jobs == nil || jobs.Kind != KindMap {
		return nil, false, fmt.Errorf("no jobs")
	}
	for i, id := range jobs.Keys {
		j, err := readJob(id, jobs.Vals[i])
		if err != nil {
			return nil, false, fmt.Errorf("job %s: %w", id, err)
		}
		wf.Jobs = append(wf.Jobs, j)
	}
	return wf, onPR, nil
}

// pullRequestTrigger reports whether `on` includes pull_request, and notes the
// filters on it that local CI does not evaluate.
func pullRequestTrigger(on *Node) (bool, []string) {
	switch {
	case on == nil:
		return false, nil
	case on.Kind == KindScalar:
		return on.Str == "pull_request", nil
	case on.Kind == KindList:
		for _, it := range on.Items {
			if it.Text() == "pull_request" {
				return true, nil
			}
		}
	case on.Kind == KindMap:
		pr := on.Get("pull_request")
		if !containsKey(on.Keys, "pull_request") {
			return false, nil
		}
		var notes []string
		for _, f := range []string{"branches", "branches-ignore", "paths", "paths-ignore", "types"} {
			if pr.Get(f) != nil {
				notes = append(notes, "on.pull_request."+f+" is not evaluated: every job runs")
			}
		}
		return true, notes
	}
	return false, nil
}

func readDefaults(d *Node) (workDir, shell string) {
	run := d.Get("run")
	return run.Get("working-directory").Text(), run.Get("shell").Text()
}

func readEnv(n *Node) ([]KV, error) {
	if n == nil || n.Kind == KindNull {
		return nil, nil
	}
	if n.Kind == KindScalar {
		return nil, fmt.Errorf("line %d: env as an expression is not supported", n.Line)
	}
	if n.Kind != KindMap {
		return nil, fmt.Errorf("line %d: env must be a map", n.Line)
	}
	var kvs []KV
	for i, k := range n.Keys {
		v := n.Vals[i]
		if v.Kind == KindMap || v.Kind == KindList {
			return nil, fmt.Errorf("line %d: env %s must be a scalar", v.Line, k)
		}
		kvs = append(kvs, KV{k, v.Str})
	}
	return kvs, nil
}

func readJob(id string, n *Node) (*Job, error) {
	if n.Kind != KindMap {
		return nil, fmt.Errorf("line %d: a job is a map", n.Line)
	}
	j := &Job{ID: id, Name: n.Get("name").Text(), If: n.Get("if").Text(), Strategy: n.Get("strategy"), Line: n.Line}
	var err error
	if j.Needs, err = readNeeds(n.Get("needs")); err != nil {
		return nil, err
	}
	if j.Env, err = readEnv(n.Get("env")); err != nil {
		return nil, fmt.Errorf("env: %w", err)
	}
	j.WorkDir, j.Shell = readDefaults(n.Get("defaults"))
	if t := n.Get("timeout-minutes").Text(); t != "" {
		if j.TimeoutMin, err = strconv.Atoi(t); err != nil {
			return nil, fmt.Errorf("timeout-minutes %q is not a whole number", t)
		}
	}
	if out := n.Get("outputs"); out != nil && out.Kind == KindMap {
		for i, k := range out.Keys {
			j.Outputs = append(j.Outputs, KV{k, out.Vals[i].Text()})
		}
	}
	switch {
	case n.Get("uses") != nil:
		j.Unsupported = "a reusable workflow (uses: " + n.Get("uses").Text() + ")"
	case n.Get("container") != nil:
		j.Unsupported = "a container job"
	case n.Get("services") != nil:
		j.Unsupported = "a job with services"
	}
	steps := n.Get("steps")
	if steps == nil || steps.Kind != KindList {
		if j.Unsupported == "" {
			return nil, fmt.Errorf("no steps")
		}
		return j, nil
	}
	for _, sn := range steps.Items {
		s, err := readStep(sn)
		if err != nil {
			return nil, err
		}
		j.Steps = append(j.Steps, s)
	}
	return j, nil
}

func readNeeds(n *Node) ([]string, error) {
	switch {
	case n == nil || n.Kind == KindNull:
		return nil, nil
	case n.Kind == KindScalar:
		return []string{n.Str}, nil
	case n.Kind == KindList:
		var ids []string
		for _, it := range n.Items {
			ids = append(ids, it.Text())
		}
		return ids, nil
	}
	return nil, fmt.Errorf("line %d: needs is a job id or a list of them", n.Line)
}

func readStep(n *Node) (*Step, error) {
	if n.Kind != KindMap {
		return nil, fmt.Errorf("line %d: a step is a map", n.Line)
	}
	s := &Step{
		ID: n.Get("id").Text(), Name: n.Get("name").Text(), If: n.Get("if").Text(),
		Run: n.Get("run").Text(), Uses: n.Get("uses").Text(),
		WorkDir: n.Get("working-directory").Text(), Shell: n.Get("shell").Text(),
		ContinueOnError: n.Get("continue-on-error").Text(), Line: n.Line,
	}
	if s.Run == "" && s.Uses == "" {
		return nil, fmt.Errorf("line %d: a step needs run or uses", n.Line)
	}
	if s.Run != "" && s.Uses != "" {
		return nil, fmt.Errorf("line %d: a step has run or uses, not both", n.Line)
	}
	env, err := readEnv(n.Get("env"))
	if err != nil {
		return nil, fmt.Errorf("step at line %d: env: %w", n.Line, err)
	}
	s.Env = env
	return s, nil
}
