package undercover

import "strings"

// RefArgs lists the ref names a git invocation would create or push. rest
// is the verb and its arguments, git and its global options already read out
// of the way.
// pushesCurrent is a push that names no refspec, or HEAD as one: git sends
// the current branch, which only the repo can name.
func RefArgs(rest []string) (kind string, names []string, pushesCurrent bool) {
	if len(rest) == 0 {
		return "", nil, false
	}
	args := rest[1:]
	switch rest[0] {
	case "checkout":
		return "branch", flagValues(args, map[string]bool{"-b": true, "-B": true, "--orphan": true}), false
	case "switch":
		return "branch", flagValues(args, map[string]bool{"-c": true, "-C": true, "--create": true, "--force-create": true, "--orphan": true}), false
	case "worktree":
		if len(args) > 0 && args[0] == "add" {
			return "branch", flagValues(args[1:], map[string]bool{"-b": true, "-B": true}), false
		}
	case "branch":
		return "branch", branchCreates(args), false
	case "push":
		names, current := pushTargets(args)
		return "pushed ref", names, current
	}
	return "", nil, false
}

// flagValues answers the value of every flag in set, spelled `-b name` or
// `--create=name`, up to a `--`.
func flagValues(args []string, set map[string]bool) []string {
	var out []string
	take := false
	for _, a := range args {
		if take {
			out = append(out, a)
			take = false
			continue
		}
		if a == "--" {
			break
		}
		if set[a] {
			take = true
			continue
		}
		if name, val, ok := strings.Cut(a, "="); ok && set[name] {
			out = append(out, val)
		}
	}
	return out
}

// branchNonCreating are the `git branch` modes that create nothing: list,
// delete, and the upstream and description edits.
var branchNonCreating = []string{
	"-d", "-D", "--delete", "-l", "--list", "-a", "--all", "-r", "--remotes",
	"--show-current", "--contains", "--no-contains", "--merged", "--no-merged",
	"--points-at", "--edit-description", "--unset-upstream", "-u",
	"--set-upstream-to", "-v", "-vv", "--verbose", "--format", "--sort",
}

// branchRenaming are the modes whose LAST operand is the new name.
var branchRenaming = map[string]bool{"-m": true, "-M": true, "--move": true, "-c": true, "-C": true, "--copy": true}

// branchCreates answers the name a `git branch` invocation creates: the new
// name of a rename or copy, else the first operand.
func branchCreates(args []string) []string {
	var operands []string
	renaming := false
	for _, a := range args {
		for _, flag := range branchNonCreating {
			if a == flag || strings.HasPrefix(a, flag+"=") {
				return nil
			}
		}
		if branchRenaming[a] {
			renaming = true
		}
		if !strings.HasPrefix(a, "-") {
			operands = append(operands, a)
		}
	}
	if len(operands) == 0 {
		return nil
	}
	if renaming {
		return operands[len(operands)-1:]
	}
	return operands[:1]
}

// pushTargets answers the destination ref of every refspec a push names.
// A delete (`--delete`, `-d`, `:ref`) sends nothing, and `--all`, `--mirror`
// and `--tags` are left to the pre-push hook, which sees each ref git sends.
func pushTargets(args []string) (names []string, current bool) {
	var operands []string
	skip := false
	for _, a := range args {
		if skip {
			skip = false
			continue
		}
		switch {
		case a == "--delete" || a == "-d" || a == "--all" || a == "--mirror" || a == "--tags":
			return nil, false
		case pushValueFlags[a]:
			skip = true
		case !strings.HasPrefix(a, "-"):
			operands = append(operands, a)
		}
	}
	if len(operands) < 2 {
		return nil, true
	}
	for _, spec := range operands[1:] {
		spec = strings.TrimPrefix(spec, "+")
		if strings.HasPrefix(spec, ":") {
			continue
		}
		dst := spec
		if _, after, ok := strings.Cut(spec, ":"); ok {
			dst = after
		}
		if dst == "HEAD" {
			current = true
			continue
		}
		names = append(names, dst)
	}
	return names, current
}

// pushValueFlags are the `git push` options whose value is a separate word
// and never a refspec.
var pushValueFlags = map[string]bool{
	"-o":                   true,
	"--push-option":        true,
	"--receive-pack":       true,
	"--exec":               true,
	"--recurse-submodules": true,
	"--repo":               true,
}
