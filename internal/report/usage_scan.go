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
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// The transcripts are the agent harness's own files, and their format is the
// harness's: this reads only the few fields it needs, ignores the rest, counts
// a line it cannot read and goes on. Nothing of a record's content is kept: the
// scan leaves numbers (tokens per day, lane, model, session) and the estimated
// token count of what aphrollo's hooks injected, classified, never quoted.

// maxLineBytes bounds one transcript line held in memory; a longer line is
// counted unreadable and skipped, so a huge tool result cannot exhaust it. The
// records this reads are a few kilobytes: it is the tool results that grow.
const maxLineBytes = 8 << 20

// syntheticModel is the harness's own marker on a record it wrote itself, not a
// model's reply: it carries no usage and is not a turn.
const syntheticModel = "<synthetic>"

// UsageTokens are the tokens of a cell. Fresh, CacheWrite and CacheRead are the
// three parts of the input the model was submitted; CacheWrite1h is the part of
// CacheWrite written to the 1-hour cache, which costs more than the 5-minute one.
type UsageTokens struct {
	Fresh, CacheWrite, CacheWrite1h, CacheRead, Output, Thinking, Turns int64
}

func (a *UsageTokens) add(b UsageTokens) {
	a.Fresh += b.Fresh
	a.CacheWrite += b.CacheWrite
	a.CacheWrite1h += b.CacheWrite1h
	a.CacheRead += b.CacheRead
	a.Output += b.Output
	a.Thinking += b.Thinking
	a.Turns += b.Turns
}

// Submitted is the whole input of the turns: what was sent, fresh or cached.
func (a UsageTokens) Submitted() int64 { return a.Fresh + a.CacheWrite + a.CacheRead }

// UsageCell is the finest grain of the usage facts: one model's turns in one
// session, on one lane and day, as the coordinator or a subagent. Every table
// of the report regroups cells; a derived number is computed when read. Lane is
// attributed from the event log and the transcripts' worktree calls (see
// usage_lane.go), since a record's cwd names the primary checkout.
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
	// Unreadable is the lines that were not JSON of the shape read or were too
	// long, and UnreadableFiles the files that could not be opened: counted,
	// never hidden.
	Unreadable, UnreadableFiles int
}

// ScanOptions say what to read: the harness's config dir, the repository by
// name, the span of record times kept, and the repo's event log, which says
// which lane each session and agent worked on.
type ScanOptions struct {
	ConfigDir  string
	Repo       string
	Since, Now time.Time
	Events     []tdd.Event
}

// transcriptLine is the part of a record the scan reads; unknown fields are
// ignored, so a newer harness's extra fields cost nothing.
type transcriptLine struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	SessionID   string `json:"sessionId"`
	AgentID     string `json:"agentId"`
	RequestID   string `json:"requestId"`
	Cwd         string `json:"cwd"`
	IsSidechain bool   `json:"isSidechain"`
	Message     *struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			Input         int64 `json:"input_tokens"`
			CCreate       int64 `json:"cache_creation_input_tokens"`
			CRead         int64 `json:"cache_read_input_tokens"`
			Output        int64 `json:"output_tokens"`
			CacheCreation *struct {
				Hour int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
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

// turn is one reply as the scan holds it until its requestId is settled and its
// lane is attributed.
type turn struct {
	cell  UsageCell
	tok   UsageTokens
	actor actorKey
	at    time.Time
}

// scan is the state of one ScanUsage run.
type scan struct {
	o        ScanOptions
	f        UsageFacts
	turns    map[string]turn
	lanes    map[actorKey][]laneEvent
	resolver *cwdResolver
}

// ScanUsage reads every transcript under <config>/projects (the sessions and
// their subagents) and keeps the records of the repository named, within the
// window. A reply split over several records shares a requestId and is counted
// once, by its record with the most output tokens.
func ScanUsage(o ScanOptions) UsageFacts {
	s := &scan{o: o, turns: map[string]turn{}, lanes: map[actorKey][]laneEvent{}, resolver: newCwdResolver(),
		f: UsageFacts{Repo: o.Repo, Cells: map[UsageCell]UsageTokens{}, Inject: map[InjectKey]InjectTokens{}}}
	for _, path := range transcriptFiles(o.ConfigDir, o.Since) {
		s.file(path)
	}
	s.addLogEvents()
	for _, t := range s.turns {
		t.cell.Lane = s.laneOf(t)
		c := s.f.Cells[t.cell]
		c.add(t.tok)
		s.f.Cells[t.cell] = c
	}
	return s.f
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

func (s *scan) file(path string) {
	file, err := os.Open(path)
	if err != nil {
		s.f.UnreadableFiles++
		return
	}
	defer file.Close()
	slashed := filepath.ToSlash(path)
	subagent := strings.Contains(slashed, "/subagents/")
	fileAgent := ""
	if subagent {
		fileAgent = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "agent-"), ".jsonl")
	}
	r := bufio.NewReaderSize(file, 1<<20)
	for n := 0; ; n++ {
		line, tooLong, err := readLine(r)
		switch {
		case tooLong:
			s.f.Unreadable++
		case len(line) > 0:
			s.line(line, path+"#"+strconv.Itoa(n), fileAgent)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.f.UnreadableFiles++
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

func (s *scan) line(raw []byte, key, fileAgent string) {
	var rec transcriptLine
	if err := json.Unmarshal(raw, &rec); err != nil {
		s.f.Unreadable++
		return
	}
	at, err := time.Parse(time.RFC3339, rec.Timestamp)
	if err != nil || (!s.o.Since.IsZero() && at.Before(s.o.Since)) || (!s.o.Now.IsZero() && at.After(s.o.Now)) {
		return
	}
	repo, cwdLane := s.resolver.resolve(rec.Cwd)
	if repo != s.o.Repo {
		return
	}
	agent := rec.AgentID
	if agent == "" {
		agent = fileAgent
	}
	if agent == "" && rec.IsSidechain {
		agent = "sidechain"
	}
	actor := actorKey{rec.SessionID, agent}
	day := at.UTC().Format("2006-01-02")
	switch {
	case rec.Type == "assistant" && rec.Message != nil && rec.Message.Usage != nil:
		if rec.Message.Model == syntheticModel {
			return // the harness's own record: no model replied, no tokens were spent
		}
		s.turn(rec, key, actor, day, at)
		if cwdLane != "main" {
			s.lanes[actor] = append(s.lanes[actor], laneEvent{at, cwdLane})
		}
		s.worktreeCalls(rec, actor, at)
	case rec.Type == "attachment" && rec.Attachment != nil && rec.Attachment.Type == "hook_additional_context":
		countInjection(&s.f, day, rec.SessionID, rec.Attachment.HookEvent, rec.Attachment.Content)
	}
}

func (s *scan) turn(rec transcriptLine, key string, actor actorKey, day string, at time.Time) {
	u := rec.Message.Usage
	t := turn{
		cell:  UsageCell{Day: day, Session: rec.SessionID, Model: rec.Message.Model, Subagent: actor.agent != ""},
		tok:   UsageTokens{Fresh: u.Input, CacheWrite: u.CCreate, CacheRead: u.CRead, Output: u.Output, Turns: 1},
		actor: actor, at: at,
	}
	if u.CacheCreation != nil {
		t.tok.CacheWrite1h = min(u.CacheCreation.Hour, u.CCreate)
	}
	if u.Details != nil {
		t.tok.Thinking = u.Details.Thinking
	}
	id := rec.RequestID
	if id == "" {
		id = key
	}
	if prev, ok := s.turns[id]; !ok || t.tok.Output > prev.tok.Output {
		s.turns[id] = t
	}
}

// countInjection adds the estimated tokens of one hook injection to the kind of
// each line, counting only text aphrollo's hooks wrote: from the first line that
// names the gate or the tool on, since one hook's output follows its first line.
// The text is classified and measured and then dropped.
func countInjection(f *UsageFacts, day, session, event string, content json.RawMessage) {
	if event == "" {
		event = "other"
	}
	counted := false
	for _, text := range injectedTexts(content) {
		marked := false
		for _, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if !marked && !aphrolloLine(line) {
				continue
			}
			marked = true
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

// aphrolloLine is whether a line is one of aphrollo's own: the gate's lines
// start with "gate", and its other texts name the tool or its skill.
func aphrolloLine(line string) bool {
	l := strings.ToLower(strings.TrimSpace(line))
	return strings.HasPrefix(l, "gate:") || strings.HasPrefix(l, "gate ") || strings.Contains(l, "aphrollo") || strings.Contains(l, "/tdd")
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
