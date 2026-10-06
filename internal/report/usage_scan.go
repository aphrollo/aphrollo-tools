package report

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/measure"
)

// The transcripts are the agent harness's own files, and their format is the
// harness's: this reads only the few fields it needs, ignores the rest, counts
// a line it cannot read and goes on. Nothing of a record's content is kept: the
// scan leaves numbers (tokens per day, lane, model, session) and the estimated
// token count of what the hooks injected, classified, never quoted.

// maxLineBytes bounds one transcript line held in memory; a longer line is
// counted unreadable and skipped, so a huge tool result cannot exhaust it.
const maxLineBytes = 64 << 20

// UsageTokens are the tokens of a cell. Fresh, CacheWrite and CacheRead are the
// three parts of the input the model was submitted.
type UsageTokens struct {
	Fresh, CacheWrite, CacheRead, Output, Thinking, Turns int64
}

func (a *UsageTokens) add(b UsageTokens) {
	a.Fresh += b.Fresh
	a.CacheWrite += b.CacheWrite
	a.CacheRead += b.CacheRead
	a.Output += b.Output
	a.Thinking += b.Thinking
	a.Turns += b.Turns
}

// Submitted is the whole input of the turns: what was sent, fresh or cached.
func (a UsageTokens) Submitted() int64 { return a.Fresh + a.CacheWrite + a.CacheRead }

// UsageCell is the finest grain of the usage facts: one model's turns in one
// session, on one lane and day, as the coordinator or a subagent. Every table
// of the report regroups cells; a derived number is computed when read.
type UsageCell struct {
	Day, Lane, Session, Model string
	Subagent                  bool
}

// InjectKey is the finest grain of the injected text: a hook event's text of
// one kind in one session on one day.
type InjectKey struct{ Day, Session, Event, Kind string }

// InjectTokens is the estimated tokens of the injected text and how many
// injections held it.
type InjectTokens struct{ Tokens, Count int64 }

// UsageFacts is what a scan of the transcripts leaves for one repository.
type UsageFacts struct {
	Repo   string
	Cells  map[UsageCell]UsageTokens
	Inject map[InjectKey]InjectTokens
	// Unreadable is the lines that were not JSON of the shape read, and
	// UnreadableFiles the files that could not be opened: counted, never hidden.
	Unreadable, UnreadableFiles int
}

// ScanOptions say what to read: the harness's config dir, the repository by
// name, and the span of record times kept.
type ScanOptions struct {
	ConfigDir  string
	Repo       string
	Since, Now time.Time
}

// transcriptLine is the part of a record the scan reads; unknown fields are
// ignored, so a newer harness's extra fields cost nothing.
type transcriptLine struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	SessionID   string `json:"sessionId"`
	RequestID   string `json:"requestId"`
	Cwd         string `json:"cwd"`
	IsSidechain bool   `json:"isSidechain"`
	Message     *struct {
		Model string `json:"model"`
		Usage *struct {
			Input   int64 `json:"input_tokens"`
			CCreate int64 `json:"cache_creation_input_tokens"`
			CRead   int64 `json:"cache_read_input_tokens"`
			Output  int64 `json:"output_tokens"`
			Details *struct {
				Thinking int64 `json:"thinking_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	} `json:"message"`
	Attachment *struct {
		Type      string          `json:"type"`
		HookEvent string          `json:"hookEvent"`
		Content   json.RawMessage `json:"content"`
	} `json:"attachment"`
}

// turn is one reply as the scan holds it until its requestId is settled.
type turn struct {
	cell UsageCell
	tok  UsageTokens
}

// ScanUsage reads every transcript under <config>/projects (the sessions and
// their subagents) and keeps the records of the repository named, within the
// window. A reply split over several records shares a requestId and is counted
// once, by its record with the most output tokens.
func ScanUsage(o ScanOptions) UsageFacts {
	f := UsageFacts{Repo: o.Repo, Cells: map[UsageCell]UsageTokens{}, Inject: map[InjectKey]InjectTokens{}}
	turns := map[string]turn{}
	resolver := newCwdResolver()
	for _, path := range transcriptFiles(o.ConfigDir, o.Since) {
		scanFile(path, o, resolver, &f, turns)
	}
	for _, t := range turns {
		c := f.Cells[t.cell]
		c.add(t.tok)
		f.Cells[t.cell] = c
	}
	return f
}

// transcriptFiles are the session files and the subagent files, in a stable
// order, without those last written before since (they hold nothing newer).
func transcriptFiles(configDir string, since time.Time) []string {
	var out []string
	for _, pattern := range []string{"*.jsonl", filepath.Join("*", "subagents", "*.jsonl")} {
		matches, _ := filepath.Glob(filepath.Join(configDir, "projects", "*", pattern))
		for _, m := range matches {
			if info, err := os.Stat(m); err == nil && !since.IsZero() && info.ModTime().Before(since) {
				continue
			}
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

func scanFile(path string, o ScanOptions, resolver *cwdResolver, f *UsageFacts, turns map[string]turn) {
	file, err := os.Open(path)
	if err != nil {
		f.UnreadableFiles++
		return
	}
	defer file.Close()
	subagent := strings.Contains(filepath.ToSlash(path), "/subagents/")
	r := bufio.NewReaderSize(file, 1<<20)
	for n := 0; ; n++ {
		line, tooLong, err := readLine(r)
		if len(line) > 0 || tooLong {
			if tooLong {
				f.Unreadable++
			} else {
				scanLine(line, path+"#"+strconv.Itoa(n), subagent, o, resolver, f, turns)
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				f.UnreadableFiles++
			}
			return
		}
	}
}

// readLine is the next line without its newline. A line past maxLineBytes is
// read through and dropped (tooLong), so memory stays bounded.
func readLine(r *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		part, isPrefix, e := r.ReadLine()
		if e != nil {
			return line, tooLong, e
		}
		if !tooLong {
			if len(line)+len(part) > maxLineBytes {
				tooLong, line = true, nil
			} else {
				line = append(line, part...)
			}
		}
		if !isPrefix {
			return line, tooLong, nil
		}
	}
}

func scanLine(line []byte, key string, subagent bool, o ScanOptions, resolver *cwdResolver, f *UsageFacts, turns map[string]turn) {
	var rec transcriptLine
	if err := json.Unmarshal(line, &rec); err != nil {
		f.Unreadable++
		return
	}
	at, err := time.Parse(time.RFC3339, rec.Timestamp)
	if err != nil || (!o.Since.IsZero() && at.Before(o.Since)) || (!o.Now.IsZero() && at.After(o.Now)) {
		return
	}
	repo, lane := resolver.resolve(rec.Cwd)
	if repo != o.Repo {
		return
	}
	day := at.UTC().Format("2006-01-02")
	switch {
	case rec.Type == "assistant" && rec.Message != nil && rec.Message.Usage != nil:
		u := rec.Message.Usage
		t := turn{
			cell: UsageCell{Day: day, Lane: lane, Session: rec.SessionID, Model: rec.Message.Model, Subagent: subagent || rec.IsSidechain},
			tok:  UsageTokens{Fresh: u.Input, CacheWrite: u.CCreate, CacheRead: u.CRead, Output: u.Output, Turns: 1},
		}
		if u.Details != nil {
			t.tok.Thinking = u.Details.Thinking
		}
		id := rec.RequestID
		if id == "" {
			id = key
		}
		if prev, ok := turns[id]; !ok || t.tok.Output > prev.tok.Output {
			turns[id] = t
		}
	case rec.Type == "attachment" && rec.Attachment != nil && rec.Attachment.Type == "hook_additional_context":
		countInjection(f, day, rec.SessionID, rec.Attachment.HookEvent, rec.Attachment.Content)
	}
}

// countInjection adds the estimated tokens of one hook injection, line by
// line, to the kind of each line. The text is classified and measured and then
// dropped.
func countInjection(f *UsageFacts, day, session, event string, content json.RawMessage) {
	if event == "" {
		event = "other"
	}
	counted := false
	for _, text := range injectedTexts(content) {
		for _, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			k := InjectKey{Day: day, Session: session, Event: event, Kind: injectKind(event, line)}
			v := f.Inject[k]
			v.Tokens += int64(measure.Tokens(len(line) + 1))
			if !counted {
				v.Count++
				counted = true
			}
			f.Inject[k] = v
		}
	}
}

// injectedTexts is the text parts of a hook attachment's content, which is a
// string, a list of strings or a list of objects with a text.
func injectedTexts(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	var out []string
	for _, item := range list {
		var str string
		var obj struct {
			Text string `json:"text"`
		}
		switch {
		case json.Unmarshal(item, &str) == nil:
			out = append(out, str)
		case json.Unmarshal(item, &obj) == nil && obj.Text != "":
			out = append(out, obj.Text)
		}
	}
	return out
}

// injectKind classifies one injected line by what it says, never by quoting it:
// the gate's verdict kinds, the session-start and reply-style texts, the rest.
func injectKind(event, line string) string {
	l := strings.ToLower(line)
	has := func(parts ...string) bool {
		for _, p := range parts {
			if strings.Contains(l, p) {
				return true
			}
		}
		return false
	}
	switch {
	case has("reply style", "reply_style", "terse"):
		return "reply-style"
	case has("not run", "timeout", "skipped", "not measured", "not tested", "not-tested"):
		return "not-tested"
	case has("deferred", "building"):
		return "deferred"
	case has("red-missing-impl"):
		return "red-missing-impl"
	case has("red-bogus"):
		return "red-bogus"
	case has("reject", "refus", "blocked", "denied", "deny"):
		return "refused"
	case has("outcome=red", " red", "red:", "→ red"):
		return "red"
	case has("green"):
		return "green"
	case strings.EqualFold(event, "SessionStart"):
		return "session-start"
	case has("gate"):
		return "gate-other"
	}
	return "other"
}

// cwdResolver maps the working directory a record names to the repository and
// lane it is in, by reading the checkout itself and not by guessing from the
// transcript directory's name.
type cwdResolver struct{ seen map[string][2]string }

func newCwdResolver() *cwdResolver { return &cwdResolver{seen: map[string][2]string{}} }

func (c *cwdResolver) resolve(cwd string) (repo, lane string) {
	if v, ok := c.seen[cwd]; ok {
		return v[0], v[1]
	}
	repo, lane = resolveCwd(cwd)
	c.seen[cwd] = [2]string{repo, lane}
	return repo, lane
}

const maxWalk = 16

// resolveCwd finds the checkout's .git: a directory is the main checkout (lane
// main); a file names a linked worktree, whose common dir's parent is the repo
// and whose own directory name is the lane. A directory that no longer exists
// (a removed lane) is read from the path's own .worktrees/<repo>/<lane> shape.
func resolveCwd(cwd string) (repo, lane string) {
	if cwd == "" {
		return "", ""
	}
	dir := cwd
	for i := 0; i < maxWalk; i++ {
		if repo, lane, ok := checkoutAt(dir); ok {
			return repo, lane
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return resolveByPath(cwd)
}

func checkoutAt(dir string) (repo, lane string, ok bool) {
	gitPath := filepath.Join(dir, ".git")
	fi, err := os.Lstat(gitPath)
	if err != nil {
		return "", "", false
	}
	if fi.IsDir() {
		return filepath.Base(dir), "main", true
	}
	data, err := os.ReadFile(gitPath)
	gitdir, found := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if err != nil || !found {
		return "", "", false
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	common := filepath.Join(gitdir, "..", "..")
	if rel, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		common = strings.TrimSpace(string(rel))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitdir, common)
		}
	}
	return filepath.Base(filepath.Dir(filepath.Clean(common))), "lane/" + filepath.Base(dir), true
}

func resolveByPath(cwd string) (repo, lane string) {
	parts := strings.Split(strings.ReplaceAll(cwd, "\\", "/"), "/")
	for i, p := range parts {
		if p == ".worktrees" && i+2 < len(parts) {
			return parts[i+1], "lane/" + parts[i+2]
		}
	}
	return parts[len(parts)-1], "main"
}
