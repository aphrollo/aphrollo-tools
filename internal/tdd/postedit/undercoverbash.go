package postedit

import (
	"encoding/json"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/undercover"
)

// undercoverBashPolicy names this wall's refusals in gate.log.
const undercoverBashPolicy = "undercover"

// The text kinds a refusal names.
const (
	undercoverTitle = "PR or issue title"
	undercoverBody  = "PR or issue body"
)

// undercoverCandidate is one name or text a command would create or post:
// ref names are judged token by token, text line by line.
type undercoverCandidate struct {
	kind string
	text string
	ref  bool
}

// UndercoverBashDecision refuses a Bash/PowerShell command that would create
// or push a ref whose name carries a tell, or hand gh a PR or issue title,
// body or comment that carries one, in a repo declaring `undercover = true`
// (issue #879). It reads the same tell table as the commit-msg gate, the git
// shim and pre-push, which stay the walls for whatever this hook never sees.
// The command is read as real words, so the words of a quoted argument or a
// comment are data; a heredoc body is judged as text only when the same
// command posts through gh, since that is how a long body reaches it. The
// repo is the one the shell's cwd sits in; with the key off the wall is inert.
func UndercoverBashDecision(raw []byte) Decision {
	var in bashSuiteInput
	if err := json.Unmarshal(raw, &in); err != nil || !bashLikeTools[in.ToolName] {
		return Decision{}
	}
	var found []undercoverCandidate
	posts := false
	scanCommand(in.ToolInput.Command, func(words []string) bool {
		c, p := segmentUndercoverCandidates(words, in.Cwd)
		found = append(found, c...)
		posts = posts || p
		return false
	})
	if posts {
		found = append(found, undercoverCandidate{kind: undercoverBody, text: heredocBodies(in.ToolInput.Command)})
	}
	if len(found) == 0 {
		return Decision{}
	}
	root := RepoRoot(in.Cwd)
	if root == "" {
		return Decision{}
	}
	tells, on := undercover.Load(root)
	if !on {
		return Decision{}
	}
	for _, c := range found {
		if reason, hit := judgeUndercover(tells, c); hit {
			return Decision{Action: Block, Reason: reason, Policy: undercoverBashPolicy}
		}
	}
	return Decision{}
}

// judgeUndercover is the refusal one candidate earns, if any.
func judgeUndercover(tells undercover.List, c undercoverCandidate) (string, bool) {
	if c.ref {
		if tell, hit := tells.RefName(c.text); hit {
			return undercover.RefRefusal(c.kind, c.text, tell), true
		}
		return "", false
	}
	if h, hit := tells.Text(c.text); hit {
		return undercover.TextRefusal(c.kind, h), true
	}
	return "", false
}

// segmentUndercoverCandidates lists what one simple command would name or
// post, and whether it posts PR or issue text through gh at all.
func segmentUndercoverCandidates(words []string, cwd string) ([]undercoverCandidate, bool) {
	if verb, rest, isGit := gitVerb(words); isGit {
		kind, names, _ := undercover.RefArgs(append([]string{verb}, rest...))
		var out []undercoverCandidate
		for _, n := range names {
			out = append(out, undercoverCandidate{kind: kind, text: n, ref: true})
		}
		return out, false
	}
	if len(words) == 0 || baseCommand(words[0]) != "gh" {
		return nil, false
	}
	cmd := ghCommandWords(words)
	switch wordAt(cmd, 0) {
	case "pr", "issue":
		if !ghPostingVerbs[wordAt(cmd, 0)][wordAt(cmd, 1)] {
			return nil, false
		}
		return ghFlagCandidates(words, cwd), true
	case "api":
		return ghFieldCandidates(words, cwd), true
	}
	return nil, false
}

// ghPostingVerbs are the gh pr and issue subcommands that send a title, a
// body or a comment.
var ghPostingVerbs = map[string]map[string]bool{
	"pr":    {"create": true, "new": true, "edit": true, "comment": true, "review": true, "merge": true},
	"issue": {"create": true, "new": true, "edit": true, "comment": true},
}

// ghTitleFlags, ghBodyFlags, ghBodyFileFlags and ghHeadFlags are the gh pr
// and issue options that carry a title (or a squash subject), a body, a file
// to read the body from, and the PR's head branch.
var (
	ghTitleFlags    = map[string]bool{"-t": true, "--title": true, "--subject": true}
	ghBodyFlags     = map[string]bool{"-b": true, "--body": true}
	ghBodyFileFlags = map[string]bool{"-F": true, "--body-file": true}
	ghHeadFlags     = map[string]bool{"-H": true, "--head": true}
)

// ghFlagCandidates reads the title, body, body file and head of a gh pr or
// issue invocation. A body file of `-` is stdin, which a heredoc feeds and
// the caller judges; a file that cannot be read judges as no text.
func ghFlagCandidates(words []string, cwd string) []undercoverCandidate {
	var out []undercoverCandidate
	for i := range words {
		name, value := optionAt(words, i)
		switch {
		case ghTitleFlags[name]:
			out = append(out, undercoverCandidate{kind: undercoverTitle, text: value})
		case ghBodyFlags[name]:
			out = append(out, undercoverCandidate{kind: undercoverBody, text: value})
		case ghBodyFileFlags[name] && value != "-":
			out = append(out, undercoverCandidate{kind: undercoverBody, text: queryFromFile("@"+value, cwd)})
		case ghHeadFlags[name]:
			out = append(out, undercoverCandidate{kind: "PR head", text: value, ref: true})
		}
	}
	return out
}

// ghFieldCandidates reads the title, body and head fields of a `gh api`
// request, given as fields or as the top-level keys of an --input JSON file;
// a typed field's `@file` value is the file's text. A GraphQL mutation sends
// its text in the query or in any variable, so there every field is judged
// as text; a query that only reads posts nothing.
func ghFieldCandidates(words []string, cwd string) []undercoverCandidate {
	var out []undercoverCandidate
	fields := map[string]string{}
	var order []string
	for i := range words {
		name, value := optionAt(words, i)
		if name == "--input" && value != "-" {
			out = append(out, jsonFieldCandidates(queryFromFile("@"+value, cwd))...)
		}
		if !ghGraphQLQueryFlags[name] {
			continue
		}
		key, text, _ := strings.Cut(value, "=")
		if ghTypedFieldFlags[name] {
			text = queryFromFile(text, cwd)
		}
		fields[key] = text
		order = append(order, key)
	}
	mutation := graphQLMutation.MatchString(graphQLNoise.ReplaceAllString(fields["query"], " "))
	for _, key := range order {
		out = append(out, fieldCandidate(key, fields[key], mutation)...)
	}
	return out
}

// fieldCandidate is what one request field posts: a title, a body or a head
// by its key, and any field at all of a GraphQL mutation as text.
func fieldCandidate(key, text string, mutation bool) []undercoverCandidate {
	switch {
	case key == "title":
		return []undercoverCandidate{{kind: undercoverTitle, text: text}}
	case key == "head":
		return []undercoverCandidate{{kind: "PR head", text: text, ref: true}}
	case key == "body" || mutation:
		return []undercoverCandidate{{kind: undercoverBody, text: text}}
	}
	return nil
}

// jsonFieldCandidates reads the title, body and head of a JSON request body.
// Text that is not a JSON object posts no field this wall can name.
func jsonFieldCandidates(raw string) []undercoverCandidate {
	var obj map[string]any
	if json.Unmarshal([]byte(raw), &obj) != nil {
		return nil
	}
	var out []undercoverCandidate
	for _, key := range []string{"title", "body", "head"} {
		if text, ok := obj[key].(string); ok {
			out = append(out, fieldCandidate(key, text, false)...)
		}
	}
	return out
}

// heredocBodies is the text of every heredoc body cmd carries: the lines
// stripHeredocBodies drops, which it keeps in order otherwise.
func heredocBodies(cmd string) string {
	kept := strings.Split(stripHeredocBodies(cmd), "\n")
	var body []string
	k := 0
	for _, line := range strings.Split(cmd, "\n") {
		if k < len(kept) && line == kept[k] {
			k++
			continue
		}
		body = append(body, line)
	}
	return strings.Join(body, "\n")
}
