package precommit

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// A run of consecutive commands marked parallel = true is a group, and a
// command that is not marked is a barrier: it runs alone, in declared order,
// after everything before it has ended. Within a group a command starts, in
// declared order, only while the weights of the commands running (weight
// defaults to 1) plus its own fit the repo's parallel-budget, so a repo whose
// commands share a database says how many of them may be open at once. A
// command heavier than the whole budget runs alone.
//
//	[aphrollo.precommit]
//	parallel-budget = 4
//	"api" = [["make", "generate"], { argv = ["make", "lint"], parallel = true }, { argv = ["make", "db-a"], parallel = true, weight = 2 }]
//
// parallel-budget is the box's job count when it is not set. Every command of
// a group runs to its end: nothing is cancelled when a sibling goes red, so
// the refusal lists every red. Each command's lines are held and written whole
// in declared order, so the output reads as a run one after another would.

const parallelBudgetKey = "parallel-budget"

// parallelAdmits is whether a command of this weight may start when running
// commands hold used of the budget: it fits, or nothing else runs.
func parallelAdmits(used, running, weight, budget int) bool {
	return running == 0 || used+weight <= budget
}

// declaredParallelBudget is the repo's parallel-budget, or boxJobs when it
// sets none. One that is not a positive whole number is an error: a budget
// the gate guessed at could open more database connections than the repo
// allowed.
func declaredParallelBudget(repoRoot string, boxJobs int) (int, error) {
	// An absent aphrollo.toml reads as empty: no budget set.
	data, _ := os.ReadFile(filepath.Join(repoRoot, "aphrollo.toml"))
	for _, e := range tomlTableEntries(string(data), declaredPrecommitTable) {
		if e.key != parallelBudgetKey {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(e.value))
		if err != nil || n < 1 {
			return 0, fmt.Errorf("%s = %s, want a positive whole number", parallelBudgetKey, strings.TrimSpace(e.value))
		}
		return n, nil
	}
	return max(boxJobs, 1), nil
}

// bareLiteral is the TOML boolean at the start of rs, with its length, when
// it stands as a value; a bare key spelled the same way, which a '=' follows,
// is no literal.
func bareLiteral(rs []rune) (string, int) {
	n := 0
	for n < len(rs) && unicode.IsLower(rs[n]) {
		n++
	}
	word := string(rs[:n])
	if word != "true" && word != "false" {
		return "", 0
	}
	k := n
	for k < len(rs) && unicode.IsSpace(rs[k]) {
		k++
	}
	if k < len(rs) && rs[k] == '=' {
		return "", 0
	}
	return word, n
}

// declaredJob is one declared command on its way through the gate.
type declaredJob struct {
	c      declaredCommand
	r      Runner
	k      declaredKeyed
	weight int
	out    bytes.Buffer
	res    GateResult
	last   SuiteResult
	ran    bool
	done   chan struct{}
}

// judge runs the command and writes the lines it earns to w.
func (j *declaredJob) judge(gateName, repoRoot, root string, run SuiteRunner, w io.Writer) {
	judged := func(rr Runner, dir string) SuiteResult {
		j.last, j.ran = run(rr, dir), true
		return j.last
	}
	if j.c.Baseline == baselineLines {
		j.res = declaredLinesStageTo(w, gateName, repoRoot, root, j.r, judged)
	} else {
		j.res = goCheckStageTo(w, gateName, "declared", root, j.r, judged)
	}
}

// declaredGroup runs one barrier, or one group of parallel commands, and is
// the refusal over every command in it that went red.
func declaredGroup(gateName, repoRoot, root string, cmds []declaredCommand, budget int, run SuiteRunner) GateResult {
	var jobs []*declaredJob
	for _, c := range cmds {
		r := Runner{Cmd: c.Argv[0], Args: c.Argv[1:]}
		k := declaredKeying(root, c)
		if declaredReuse(gateName, root, r, k) {
			continue
		}
		jobs = append(jobs, &declaredJob{
			c: c, r: r, k: k,
			weight: max(c.Weight, defaultDeclaredWeight), done: make(chan struct{}),
		})
	}
	started := time.Now()
	if len(jobs) == 1 && !jobs[0].c.Parallel {
		// A barrier, or the lone command of a repo with no parallel key: its
		// lines are written as they happen, as ever.
		j := jobs[0]
		j.judge(gateName, repoRoot, root, run, stderrFor(root))
	} else {
		runDeclaredGroup(gateName, repoRoot, root, jobs, budget, run)
	}
	var sum float64
	var reds []*declaredJob
	for _, j := range jobs {
		recordDeclaredRun(root, j.c, j.k.key, j.k.takes && j.k.err == nil, j.res, j.last, j.ran)
		sum += j.last.Duration.Seconds()
		if j.res.Blocked {
			reds = append(reds, j)
		}
	}
	if len(jobs) > 1 {
		AppendGateLogDetail(gateName, root, "parallel group", "declared-parallel", time.Since(started), map[string]string{
			"commands":  strconv.Itoa(len(jobs)),
			"sum_secs":  strconv.FormatFloat(sum, 'f', -1, 64),
			"wall_secs": strconv.FormatFloat(time.Since(started).Seconds(), 'f', -1, 64),
		})
	}
	switch len(reds) {
	case 0:
		return GateResult{}
	case 1:
		return reds[0].res
	}
	var msg strings.Builder
	for _, j := range reds {
		msg.WriteString(j.res.Message)
		if !strings.HasSuffix(j.res.Message, "\n") {
			msg.WriteString("\n")
		}
	}
	return GateResult{Blocked: true, Message: msg.String()}
}

// runDeclaredGroup runs jobs at once within budget and returns when every one
// has ended. Jobs start in declared order, so what runs together is the same
// whatever the timings; each job's lines are written to root's stderr whole,
// in declared order, the moment it and every job before it are done.
func runDeclaredGroup(gateName, repoRoot, root string, jobs []*declaredJob, budget int, run SuiteRunner) {
	var (
		mu            sync.Mutex
		changed       = sync.NewCond(&mu)
		used, running int
		wg            sync.WaitGroup
	)
	flushed := make(chan struct{})
	go func() {
		defer close(flushed)
		for _, j := range jobs {
			<-j.done
			_, _ = stderrFor(root).Write(j.out.Bytes())
		}
	}()
	for _, j := range jobs {
		mu.Lock()
		for !parallelAdmits(used, running, j.weight, budget) {
			changed.Wait()
		}
		used, running = used+j.weight, running+1
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			j.judge(gateName, repoRoot, root, run, &j.out)
			mu.Lock()
			used, running = used-j.weight, running-1
			changed.Broadcast()
			mu.Unlock()
			close(j.done)
		}()
	}
	wg.Wait()
	<-flushed
}
