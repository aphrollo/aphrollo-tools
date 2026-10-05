package config

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// EnvAlias is an APHROLLO_* variable that set a key before the key had a home
// in a file. It still works for one more release, above every file and below a
// flag, and says so once per process. Every other APHROLLO_* variable is a test
// seam or a process marker the gate sets for its own children, not a setting.
type EnvAlias struct {
	Var string
	Key string
}

// EnvAliases is the table of deprecated variables and the key each one became.
var EnvAliases = []EnvAlias{
	{"APHROLLO_POSTEDIT_BUDGET_SECS", "budgets.edit_s"},
	{"APHROLLO_LOCK_WAIT_SECS", "budgets.lock_wait_s"},
	{"APHROLLO_CARGO_WAIT_SECS", "budgets.cargo_wait_s"},
	{"APHROLLO_GIT_WAIT_SECS", "budgets.git_wait_s"},
	{"APHROLLO_LINT_WAIT_SECS", "budgets.lint_wait_s"},
	{"APHROLLO_DEFERRED_MAX_SECS", "budgets.deferred_max_s"},
	{"APHROLLO_MECH_TOTAL_SECS", "budgets.mech_total_s"},
	{"APHROLLO_MECH_PARALLEL", "box.mech_parallel"},
	{"APHROLLO_BUILD_SLOTS", "box.build_slots"},
	{"APHROLLO_REPLY_STYLE", "reply_style"},
}

var (
	noticed    sync.Map
	noticeMu   sync.Mutex
	noticeSink io.Writer = os.Stderr
)

// SetNoticeSink redirects the deprecation notices, and answers the undo.
func SetNoticeSink(w io.Writer) (restore func()) {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	prev := noticeSink
	noticeSink = w
	return func() {
		noticeMu.Lock()
		defer noticeMu.Unlock()
		noticeSink = prev
	}
}

// envs reads the deprecated variables as claims above the files. An empty value
// is unset, and one the key cannot read is ignored, as the variables always
// were: neither is worth a notice.
func (r *reader) envs() {
	for _, e := range EnvAliases {
		raw := strings.TrimSpace(os.Getenv(e.Var))
		if raw == "" {
			continue
		}
		k, ok := Lookup(e.Key)
		if !ok {
			continue
		}
		v, err := k.Parse(raw)
		if err != nil {
			continue
		}
		notice(e)
		r.claims = append(r.claims, claim{key: k, layer: Env, rank: rankEnv, value: v, alias: e.Var, reported: true})
	}
}

// notice says once per process that a variable is deprecated, and what to set
// instead.
func notice(e EnvAlias) {
	if _, seen := noticed.LoadOrStore(e.Var, true); seen {
		return
	}
	noticeMu.Lock()
	defer noticeMu.Unlock()
	fmt.Fprintf(noticeSink, "aphrollo: %s is deprecated and read for one more release; set %s instead (aphrollo config set %s <value> --user)\n", e.Var, e.Key, e.Key)
}

// Box reads the layers that hold a box's own settings: the user's config, the
// deprecated variables, no repo. The budgets and slot counts are read here.
func Box() *Config { return Load(Options{}) }
