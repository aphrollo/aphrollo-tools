package merge

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Local CI is the merge gate run as CI: the same throwaway checkout of the
// lane merged into trunk, the same Mechanical stage (ratchet laws, docs check,
// every detected root's suites, and the mutation measurement a repo declares),
// but without the opt-in GatePRMerge keeps. A repo with no laws and no
// mutants-at-merge pays nothing at the merge gate because GitHub's CI judges
// it; when local CI stands in for GitHub there is nobody else to.
//
// The verdict is a gate.log line keyed by the merge result's tree hash, so
// `gate stats` and `gate output` show it like any gate result, and the same
// tree is never judged twice: a stored green stands in for the run.

// CI modes a repo or a merge can choose.
const (
	CIAuto   = "auto"
	CILocal  = "local"
	CIGithub = "github"
)

// ciStage is the gate.log stage local CI records under.
const ciStage = "ci"

// ciModeKey is the aphrollo.toml key naming the repo's CI choice.
const ciModeKey = "ci"

// ReadCIMode is the repo's declared CI choice, auto when it declares none. An
// unknown value is an error naming the allowed ones: a typo that quietly
// fell back to auto would send a merge to GitHub the repo meant to avoid.
func ReadCIMode(root string) (string, error) {
	v, set := tomlStringIn(root+"/aphrollo.toml", "[aphrollo]", ciModeKey)
	if !set || strings.TrimSpace(v) == "" {
		return CIAuto, nil
	}
	return NormalizeCIMode(v)
}

// NormalizeCIMode validates a CI mode, from the config or from --ci.
func NormalizeCIMode(v string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(v)); m {
	case CIAuto, CILocal, CIGithub:
		return m, nil
	}
	return "", fmt.Errorf("ci = %q is not a CI mode (want auto | local | github)", v)
}

// LocalCIVerdict is what a local CI run answered: the tree it judged and
// whether a stored green stood in for a run.
type LocalCIVerdict struct {
	Tree   string
	Reused bool
	Landed bool // trunk already holds the lane: nothing to judge
}

// LocalCI judges the merge of laneWorktree's HEAD into trunk. A stored green
// for the same merge-result tree is reused; otherwise the merged tree is
// judged in a throwaway checkout and the verdict, green or red, is recorded.
func LocalCI(laneWorktree string, run SuiteRunner, log io.Writer) (LocalCIVerdict, error) {
	if log == nil {
		log = io.Discard
	}
	tips, err := prGateTipsOf(laneWorktree, log)
	if err != nil {
		return LocalCIVerdict{}, err
	}
	if tips.landed {
		return LocalCIVerdict{Landed: true}, nil
	}
	// A merge git cannot even compute a tree for has no key; the checkout
	// below reports the conflict by name, so no tree is not refused here.
	tree := ""
	if out, err := git(laneWorktree, "merge-tree", "--write-tree", tips.trunk, tips.lane); err == nil {
		tree = strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	}
	if tree != "" && storedGreen(tree) {
		fmt.Fprintf(log, "ci local: tree %s already judged green — reusing that verdict\n", shortTree(tree))
		return LocalCIVerdict{Tree: tree, Reused: true}, nil
	}
	start := time.Now()
	err = judgeMergedTree(laneWorktree, run, log, &tips)
	if tree != "" {
		verdict := "green"
		if err != nil {
			verdict = "red"
		}
		AppendGateLog(ciStage, laneWorktree, "local-ci:"+tree, verdict, time.Since(start))
	}
	if err != nil {
		return LocalCIVerdict{Tree: tree}, err
	}
	return LocalCIVerdict{Tree: tree}, nil
}

func shortTree(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// storedGreen reports whether gate.log holds a green local CI verdict for tree.
func storedGreen(tree string) bool {
	path := GateLogPath()
	if path == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	want := "local-ci:" + tree
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if e, ok := parseGateLine(sc.Text()); ok && e.Stage == ciStage && e.Cmd == want && e.Verdict == "green" {
			return true
		}
	}
	return false
}
