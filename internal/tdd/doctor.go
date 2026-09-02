package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Every check here exists because the state it names was reached on a real box
// and was invisible until something failed for an unrelated-looking reason:
// hooks left pointing at a moved binary, a shim dir no longer first on PATH, a
// batch shim that mangles arguments, a hook timeout below the budget the hook
// itself is given. Each check reports its OWN fix, because "something is wrong
// with the install" is not actionable.

// DoctorCheck is one check's verdict. Warn marks a finding the report shows
// but does not fail on: a fact about the repo rather than a broken install.
type DoctorCheck struct {
	Name   string
	OK     bool
	Warn   bool
	Detail string
}

// DoctorInput is everything the checks read that is not a file: the config dir
// they judge, the binary they judge against, and the PATH they were resolved
// from. Injected rather than looked up, so a test drives every branch without
// touching the box's own registry or environment.
type DoctorInput struct {
	ConfigDir string
	Bin       string
	ShimDir   string
	Repo      string
	PathDirs  []string
}

// Doctor runs every check and returns the verdicts in a fixed order, so two
// runs read the same way.
func Doctor(in DoctorInput) []DoctorCheck {
	checks := []DoctorCheck{
		doctorHookBinary(in),
		doctorHookTimeouts(in),
		doctorShimPath(in),
		doctorShimExes(in),
		doctorBatchShims(in),
		doctorLockDirs(),
		doctorRetiredCommand(in),
		doctorManagedFiles(in),
	}
	if c, ok := doctorPrimaryCheckout(in); ok {
		checks = append(checks, c)
	}
	if c, ok := doctorCIClippyList(in); ok {
		checks = append(checks, c)
	}
	return checks
}

// doctorPrimaryCheckout checks the primary checkout still holds main. It
// receives merges for every lane in the repo, so one parked on a lane branch
// puts the next merge on the wrong base — and nothing says so until the merge
// lands. ok=false means the check does not apply: a linked worktree, or a
// clone with no lanes to keep separate.
func doctorPrimaryCheckout(in DoctorInput) (DoctorCheck, bool) {
	c := DoctorCheck{Name: "primary checkout on main"}
	if in.Repo == "" {
		return c, false
	}
	root, branch, applies := PrimaryCheckoutState(in.Repo)
	if !applies {
		return c, false
	}
	if branch != primaryBranch {
		c.Detail = fmt.Sprintf("%s is on %s — it receives merges and must hold %s; run `git checkout %s`",
			root, branch, primaryBranch, primaryBranch)
		return c, true
	}
	c.OK = true
	return c, true
}

// RenderDoctor prints one line per check and returns the exit code: 1 when any
// check FAILED, 0 when the only findings are warnings.
func RenderDoctor(checks []DoctorCheck) (string, int) {
	var b strings.Builder
	code := 0
	for _, c := range checks {
		switch {
		case c.OK && c.Detail == "":
			fmt.Fprintf(&b, "ok    %s\n", c.Name)
		case c.OK:
			fmt.Fprintf(&b, "ok    %s — %s\n", c.Name, c.Detail)
		default:
			fmt.Fprintf(&b, "FAIL  %s — %s\n", c.Name, c.Detail)
			code = 1
		}
	}
	return b.String(), code
}

// doctorShimExeNames is the exe-shim set a doctor run expects to find. Same
// rule as the installer: only Windows resolves a command by extension.
func doctorShimExeNames() []string { return shimExeNames() }

// doctorHookBinary checks that every managed hook runs the SAME binary, and
// that it is the binary running this check. Half the hooks pointing at a moved
// copy is a gate that behaves differently per event.
func doctorHookBinary(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "hook binary"}
	paths := managedHookBinaries(in.ConfigDir)
	if len(paths) == 0 {
		c.Detail = "no aphrollo hooks in settings.json — run `aphrollo gate init`"
		return c
	}
	if len(paths) > 1 {
		c.Detail = "hooks point at " + strings.Join(paths, " and ") + " — run `aphrollo gate init` to repoint them"
		return c
	}
	installed := paths[0]
	want, err := os.Stat(in.Bin)
	if err != nil {
		c.Detail = fmt.Sprintf("cannot read this executable %s (%v)", in.Bin, err)
		return c
	}
	have, err := os.Stat(fromShellPath(installed))
	if err != nil {
		c.Detail = fmt.Sprintf("the hooks run %s, which does not exist — run `aphrollo gate init`", installed)
		return c
	}
	if have.Size() != want.Size() || !have.ModTime().Equal(want.ModTime()) {
		c.Detail = fmt.Sprintf("the hooks run %s, which is not this build (%s) — run `aphrollo gate init`", installed, in.Bin)
		return c
	}
	c.OK = true
	return c
}

// doctorHookTimeouts checks each managed hook's settings.json timeout against
// the one init writes. The harness kills the hook PROCESS from outside when
// its timeout elapses, before the hook's own deadline and cleanup can fire: no
// verdict is returned, no state is stamped, and a spawned build is orphaned.
func doctorHookTimeouts(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "hook timeouts"}
	root, err := readSettings(in.ConfigDir)
	if err != nil {
		c.Detail = err.Error()
		return c
	}
	hooks, _ := root["hooks"].(map[string]any)
	var short []string
	for _, me := range managedEvents {
		for _, h := range managedHookEntries(hooks[me.event]) {
			got, ok := h["timeout"].(float64)
			if !ok || int(got) < me.timeout {
				short = append(short, fmt.Sprintf("%s=%v (want >= %ds)", me.event, h["timeout"], me.timeout))
			}
		}
	}
	if len(short) > 0 {
		c.Detail = strings.Join(short, ", ") + " — run `aphrollo gate init`"
		return c
	}
	c.OK = true
	return c
}

// doctorShimPath checks the queue dir is the FIRST PATH entry. Behind any
// other entry it shadows nothing, and a direct `cargo` builds without ever
// taking a slot.
func doctorShimPath(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "shim dir on PATH"}
	if len(in.PathDirs) == 0 {
		c.Detail = "could not read the user PATH"
		return c
	}
	if !samePath(in.PathDirs[0], in.ShimDir) {
		c.Detail = fmt.Sprintf("PATH starts with %s, not %s — put the queue dir first", in.PathDirs[0], in.ShimDir)
		return c
	}
	c.OK = true
	return c
}

// doctorShimExes checks the queue dir actually holds the shims. A dir first on
// PATH with nothing in it queues nothing.
func doctorShimExes(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "queue shims installed"}
	want := append([]string{"cargo", "git"}, doctorShimExeNames()...)
	var missing []string
	for _, name := range want {
		if _, err := os.Stat(filepath.Join(in.ShimDir, name)); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		c.Detail = "missing " + strings.Join(missing, ", ") + " in " + in.ShimDir + " — run `aphrollo gate init`"
		return c
	}
	c.OK = true
	return c
}

// doctorBatchShims checks the retired .cmd shims are gone. cmd.exe strips `^`
// from an argument and re-splits quoted ones, so one left in place is a wrong
// answer rather than a slow path.
func doctorBatchShims(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "batch shims removed"}
	var found []string
	for _, name := range cmdShimNames {
		if _, err := os.Stat(filepath.Join(in.ShimDir, name)); err == nil {
			found = append(found, name)
		}
	}
	if len(found) > 0 {
		c.Detail = strings.Join(found, ", ") + " still in " + in.ShimDir + " — run `aphrollo gate init`"
		return c
	}
	c.OK = true
	return c
}

// doctorLockDirs checks this account can create the machine-wide slot locks.
// A box that cannot serialises builds per user only, which is the failure the
// shared lock dir exists to prevent.
func doctorLockDirs() DoctorCheck {
	c := DoctorCheck{Name: "lock dirs writable"}
	dir := lockDir()
	if err := ensureSharedDir(dir); err != nil {
		c.Detail = fmt.Sprintf("%s is not writable (%v) — builds serialise per user only", dir, err)
		return c
	}
	c.OK = true
	c.Detail = dir
	return c
}

// doctorRetiredCommand checks the slash-command stub the skill replaced is
// gone: the skill carries the same name, so leaving both defines /tdd twice.
func doctorRetiredCommand(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "retired /tdd command"}
	path := filepath.Join(in.ConfigDir, "commands", "tdd.md")
	if _, err := os.Stat(path); err == nil {
		c.Detail = path + " still defines /tdd beside the skill — run `aphrollo gate init`"
		return c
	}
	c.OK = true
	return c
}

// doctorManagedFiles checks every skill and agent this binary ships is present
// and byte-identical to the template it carries. A drifted copy means a
// session follows rules the gate does not enforce.
func doctorManagedFiles(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "managed skills and agents"}
	want := map[string]string{
		tddSkillPath(in.ConfigDir): TDDSkill(),
		sddSkillPath(in.ConfigDir): SDDSkill(),
	}
	for _, name := range managedAgentNames {
		body, ok := ManagedAgent(name)
		if !ok {
			continue
		}
		want[agentPath(in.ConfigDir, name)] = body
	}
	var stale []string
	for path, body := range want {
		have, err := os.ReadFile(path)
		switch {
		case err != nil:
			stale = append(stale, filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path)+" (missing)")
		case string(have) != body:
			stale = append(stale, filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path)+" (edited)")
		}
	}
	if len(stale) > 0 {
		sortStrings(stale)
		c.Detail = strings.Join(stale, ", ") + " — run `aphrollo gate init`"
		return c
	}
	c.OK = true
	return c
}

// doctorCIClippyList checks a cargo workspace's CI derives its clippy list
// from the manifest rather than naming crates by hand: a hand-written list
// gates nothing the day a crate is added to clippy-clean. ok=false means the
// check does not apply here — this is not a cargo workspace.
func doctorCIClippyList(in DoctorInput) (DoctorCheck, bool) {
	c := DoctorCheck{Name: "CI clippy list"}
	if in.Repo == "" || len(cargoClippyCleanPackages(in.Repo)) == 0 {
		return c, false
	}
	workflows, _ := filepath.Glob(filepath.Join(in.Repo, ".github", "workflows", "*.yml"))
	more, _ := filepath.Glob(filepath.Join(in.Repo, ".github", "workflows", "*.yaml"))
	workflows = append(workflows, more...)
	if len(workflows) == 0 {
		c.OK, c.Warn = true, true
		c.Detail = "no CI workflow in this repo — nothing derives the clippy-clean list"
		return c, true
	}
	for _, wf := range workflows {
		data, err := os.ReadFile(wf)
		if err == nil && strings.Contains(string(data), clippyCleanListScript) {
			c.OK = true
			c.Detail = filepath.Base(wf) + " derives the list"
			return c, true
		}
	}
	c.Detail = "no workflow calls " + clippyCleanListScript +
		" — the CI clippy list must come from [workspace.metadata.aphrollo] clippy-clean, not a hand-written list"
	return c, true
}

// clippyCleanListScript is the script a workflow calls to read the crates from
// the manifest, so the list and the declaration cannot drift.
const clippyCleanListScript = "tools/clippy_clean_list.sh"

// managedHookBinaries is the set of DISTINCT binary paths the managed hooks
// invoke, sorted. More than one means the install is split across builds.
func managedHookBinaries(configDir string) []string {
	root, err := readSettings(configDir)
	if err != nil {
		return nil
	}
	hooks, _ := root["hooks"].(map[string]any)
	seen := map[string]bool{}
	var out []string
	for _, me := range managedEvents {
		for _, h := range managedHookEntries(hooks[me.event]) {
			cmd, _ := h["command"].(string)
			bin := quotedBinary(cmd)
			if bin != "" && !seen[bin] {
				seen[bin] = true
				out = append(out, bin)
			}
		}
	}
	sortStrings(out)
	return out
}

// managedHookEntries flattens one event's groups to the hook entries this tool
// owns, so a foreign hook beside ours is never judged.
func managedHookEntries(groups any) []map[string]any {
	var out []map[string]any
	for _, g := range toGroups(groups) {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		hs, _ := gm["hooks"].([]any)
		for _, h := range hs {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if cmd, ok := hm["command"].(string); ok && isManagedCmd(cmd) {
				out = append(out, hm)
			}
		}
	}
	return out
}

// quotedBinary is the path a hook command runs: the first double-quoted run,
// which is how every command this tool writes is spelled.
func quotedBinary(cmd string) string {
	open := strings.IndexByte(cmd, '"')
	if open < 0 {
		return ""
	}
	rest := cmd[open+1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// fromShellPath undoes shellPath for a stat: the hooks embed forward slashes
// even on Windows, which os.Stat accepts, so this only cleans the path.
func fromShellPath(p string) string { return filepath.FromSlash(p) }

// samePath compares two directory paths the way the OS resolves them:
// case-insensitively on Windows, and with separators and trailing slashes
// normalized everywhere.
func samePath(a, b string) bool {
	clean := func(p string) string {
		p = filepath.Clean(filepath.FromSlash(strings.Trim(p, `"`)))
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if runtime.GOOS == "windows" {
			p = strings.ToLower(p)
		}
		return p
	}
	return clean(a) == clean(b)
}

// readSettings parses a config dir's settings.json, naming the fix when it is
// missing or unreadable rather than reporting a healthy install.
func readSettings(configDir string) (map[string]any, error) {
	path := filepath.Join(configDir, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s — run `aphrollo gate init`", path)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%v)", path, err)
	}
	return root, nil
}

// sortStrings sorts in place; the report has to read the same way twice.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
