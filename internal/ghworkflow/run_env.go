package ghworkflow

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// errEmptyMatrix marks a matrix axis with nothing in it: the job is skipped.
var errEmptyMatrix = errors.New("the matrix has no combinations")

// initEnv builds the job's environment: the base environment, the variables
// GitHub sets on a runner, then the workflow's and the job's env, each value
// evaluated with the ones before it in scope.
func (r *jobRun) initEnv() error {
	ev := r.opt.Event
	r.env = append([]string{}, r.opt.Env...)
	for _, kv := range [][2]string{
		{"CI", "true"}, {"GITHUB_ACTIONS", "true"}, {"GITHUB_EVENT_NAME", "pull_request"},
		{"GITHUB_WORKSPACE", r.opt.Dir}, {"GITHUB_RUN_ID", "1"}, {"GITHUB_RUN_ATTEMPT", "1"},
		{"RUNNER_OS", hostOS()}, {"RUNNER_TEMP", r.tmp},
		{"GITHUB_SHA", text(ev["sha"])}, {"GITHUB_REPOSITORY", text(ev["repository"])},
		{"GITHUB_BASE_REF", text(ev["base_ref"])}, {"GITHUB_HEAD_REF", text(ev["head_ref"])},
	} {
		r.env = append(r.env, kv[0]+"="+kv[1])
	}
	r.envCtx = map[string]any{}
	for _, layer := range [][]KV{r.wf.Env, r.job.Env} {
		for _, kv := range layer {
			sc := r.ctxFor(ResultSuccess, r.envCtx)
			v, err := sc.Interpolate(kv.Val)
			if err != nil {
				return fmt.Errorf("env %s: %w", kv.Key, err)
			}
			r.setEnv(kv.Key, v)
		}
	}
	return nil
}

// setEnv sets one variable for this and every later step.
func (r *jobRun) setEnv(key, val string) {
	r.envCtx[key] = val
	r.env = append(r.env, key+"="+val)
}

func (r *jobRun) envMap() map[string]any { return r.envCtx }

// stepEnv is the environment of one step: the job's, then the step's own env.
func (r *jobRun) stepEnv(sc *Scope, st *Step) ([]string, error) {
	env := append([]string{}, r.env...)
	local := map[string]any{}
	for k, v := range r.envCtx {
		local[k] = v
	}
	for _, kv := range st.Env {
		sc.Root["env"] = local
		v, err := sc.Interpolate(kv.Val)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", kv.Key, err)
		}
		local[kv.Key] = v
		env = append(env, kv.Key+"="+v)
	}
	return env, nil
}

// recordStep stores a step's results where later expressions read them.
func (r *jobRun) recordStep(st *Step, outcome, conclusion string, outputs map[string]any) {
	if st.ID == "" {
		return
	}
	if outputs == nil {
		outputs = map[string]any{}
	}
	r.steps[st.ID] = map[string]any{"outcome": outcome, "conclusion": conclusion, "outputs": outputs}
}

// stepFileSet are the files a step writes its outputs, env and path to.
type stepFileSet struct{ output, env, path, summary string }

func (r *jobRun) stepFiles() (*stepFileSet, error) {
	f := &stepFileSet{}
	for name, dst := range map[string]*string{"output": &f.output, "env": &f.env, "path": &f.path, "summary": &f.summary} {
		file, err := os.CreateTemp(r.tmp, name+"-*")
		if err != nil {
			return nil, fmt.Errorf("the step's %s file could not be made: %w", name, err)
		}
		*dst = file.Name()
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("the step's %s file could not be closed: %w", name, err)
		}
	}
	return f, nil
}

func (f *stepFileSet) env2() []string {
	return []string{
		"GITHUB_OUTPUT=" + f.output, "GITHUB_ENV=" + f.env,
		"GITHUB_PATH=" + f.path, "GITHUB_STEP_SUMMARY=" + f.summary,
	}
}

// collect reads what the step wrote: its outputs are returned, its env and
// path additions take effect for the steps after it.
func (f *stepFileSet) collect(r *jobRun) map[string]any {
	outputs := map[string]any{}
	if data, err := os.ReadFile(f.output); err == nil {
		for _, kv := range parseCommandFile(string(data)) {
			outputs[kv.Key] = kv.Val
		}
	}
	if data, err := os.ReadFile(f.env); err == nil {
		for _, kv := range parseCommandFile(string(data)) {
			r.setEnv(kv.Key, kv.Val)
		}
	}
	if data, err := os.ReadFile(f.path); err == nil {
		for _, dir := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if d := strings.TrimSpace(dir); d != "" {
				r.env = append(r.env, "PATH="+d+string(os.PathListSeparator)+r.pathNow())
			}
		}
	}
	return outputs
}

// pathNow is the PATH the next step would see.
func (r *jobRun) pathNow() string { return pathIn(r.env) }

// parseCommandFile reads a GITHUB_OUTPUT or GITHUB_ENV file: name=value lines
// and name<<DELIMITER blocks.
func parseCommandFile(data string) []KV {
	var out []KV
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		head, value, hasEq := strings.Cut(line, "=")
		if key, delim, isBlock := strings.Cut(head, "<<"); isBlock {
			var body []string
			i++
			for ; i < len(lines) && lines[i] != delim; i++ {
				body = append(body, lines[i])
			}
			out = append(out, KV{key, strings.Join(body, "\n")})
		} else if hasEq && head != "" {
			out = append(out, KV{head, value})
		}
	}
	return out
}

// expandMatrix picks the job's matrix values: the first of each axis, or the
// first include entry when there are no axes. It says so in the output.
func (r *jobRun) expandMatrix() error {
	r.matrix = map[string]any{}
	m := r.job.Strategy.Get("matrix")
	if m == nil {
		return nil
	}
	if m.Kind != KindMap {
		return fmt.Errorf("a matrix that is not a map of axes (line %d) is not supported", m.Line)
	}
	sc := r.ctxFor(ResultSuccess, r.envCtx)
	for i, k := range m.Keys {
		if k == "include" || k == "exclude" {
			continue
		}
		v, err := firstOf(sc, m.Vals[i])
		if err != nil {
			return fmt.Errorf("axis %s: %w", k, err)
		}
		r.matrix[k] = v
	}
	if inc := m.Get("include"); len(r.matrix) == 0 && inc != nil && inc.Kind == KindList && len(inc.Items) > 0 {
		first := inc.Items[0]
		for i, k := range first.Keys {
			v, err := sc.Interpolate(first.Vals[i].Str)
			if err != nil {
				return fmt.Errorf("include %s: %w", k, err)
			}
			r.matrix[k] = v
		}
	}
	if len(r.matrix) == 0 {
		return errEmptyMatrix
	}
	keys := make([]string, 0, len(r.matrix))
	for k := range r.matrix {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + text(r.matrix[k])
	}
	fmt.Fprintf(r.opt.Out, "  [note] matrix: ran the first combination only (%s)\n", strings.Join(parts, ", "))
	return nil
}

// firstOf is the first value of a matrix axis: the first list item, or the
// first element of the list an expression yields.
func firstOf(sc *Scope, n *Node) (any, error) {
	switch {
	case n.Kind == KindList && len(n.Items) > 0:
		return sc.Interpolate(n.Items[0].Str)
	case n.Kind == KindList:
		return nil, errEmptyMatrix
	case n.Kind != KindScalar:
		return nil, fmt.Errorf("line %d: an axis is a list", n.Line)
	}
	e := strings.TrimSpace(n.Str)
	if !strings.HasPrefix(e, "${{") || !strings.HasSuffix(e, "}}") {
		return n.Str, nil
	}
	v, _, err := sc.Eval(strings.TrimSpace(e[3 : len(e)-2]))
	if err != nil {
		return nil, err
	}
	if list, ok := v.([]any); ok {
		if len(list) == 0 {
			return nil, errEmptyMatrix
		}
		return list[0], nil
	}
	return v, nil
}
