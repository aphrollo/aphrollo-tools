package shadow

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

func recorded(t *testing.T, name string) Payload {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := ParsePayload(raw)
	if !ok {
		t.Fatalf("%s is not a payload", name)
	}
	return p
}

func TestPayload_NamesTheActorAndTheFilesTheCallWrites(t *testing.T) {
	cases := []struct {
		file      string
		actor     string
		targets   []string
		isShell   bool
		hasWrites bool
	}{
		{"pretooluse_edit.json", "s1/a1", []string{"/w/repo/internal/lane/lane.go"}, false, true},
		{"pretooluse_write.json", "s1", []string{"/w/repo/internal/lane/lane_test.go"}, false, true},
		{"pretooluse_bash_write.json", "s1", []string{"/w/repo/internal/lane/lane.go"}, true, true},
		{"pretooluse_doc.json", "s1", []string{"/w/repo/README.md"}, false, false},
	}
	for _, c := range cases {
		p := recorded(t, c.file)
		got := p.Targets()
		for i := range got {
			got[i] = filepath.ToSlash(got[i])
		}
		if p.Actor() != c.actor || !reflect.DeepEqual(got, c.targets) || p.IsShell() != c.isShell || hasWrites(got) != c.hasWrites {
			t.Errorf("%s: actor=%q targets=%v shell=%v writes=%v, want %q %v %v %v",
				c.file, p.Actor(), got, p.IsShell(), hasWrites(got), c.actor, c.targets, c.isShell, c.hasWrites)
		}
	}
}

func TestPreEvent_IsAWriteQuestionAboutALaneUnitFromTheRecordedPayload(t *testing.T) {
	unit := Unit{ID: "internal/lane"}
	cases := []struct {
		file string
		want kernel.Event
	}{
		{"pretooluse_edit.json", kernel.Event{Kind: kernel.KindPreTool, Claude: true, Lane: "lane/x", Actor: "s1/a1",
			Tool: kernel.ToolWrite, Target: kernel.PathLane, Unit: "internal/lane", File: kernel.ClassCode, Covered: true}},
		{"pretooluse_bash_write.json", kernel.Event{Kind: kernel.KindPreTool, Claude: true, Lane: "lane/x", Actor: "s1",
			Tool: kernel.ToolBash, Target: kernel.PathLane, Unit: "internal/lane", File: kernel.ClassCode, Covered: true}},
	}
	for _, c := range cases {
		got := PreEvent(recorded(t, c.file), "lane/x", Edit{Class: kernel.ClassCode, Unit: unit, Covered: true})
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: event = %+v, want %+v", c.file, got, c.want)
		}
	}
}

func TestStopEvent_CarriesTheActorAndStopHookActiveFromTheRecordedPayload(t *testing.T) {
	cases := []struct {
		file string
		red  bool
		want kernel.Event
	}{
		{"stop.json", true, kernel.Event{Kind: kernel.KindStop, Claude: true, Lane: "lane/x", Actor: "s1", UnseenRed: true}},
		{"subagentstop_active.json", false, kernel.Event{Kind: kernel.KindStop, Claude: true, Lane: "lane/x", Actor: "s1/a1", StopActive: true}},
	}
	for _, c := range cases {
		if got := StopEvent(recorded(t, c.file), "lane/x", c.red); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: event = %+v, want %+v", c.file, got, c.want)
		}
	}
}

func TestEditAndRunEvents_AreFactsAboutATreeAndAUnit(t *testing.T) {
	edit := EditEvent("s1", "lane/x", Edit{Class: kernel.ClassTest, Unit: Unit{ID: "internal/lane"}, Tree: "k1", Covered: true})
	wantEdit := kernel.Event{Kind: kernel.KindEdit, Claude: true, Lane: "lane/x", Actor: "s1", Unit: "internal/lane", Tree: "k1", File: kernel.ClassTest, Covered: true}
	if !reflect.DeepEqual(edit, wantEdit) {
		t.Errorf("edit event = %+v, want %+v", edit, wantEdit)
	}
	run := RunEvent("s1", "lane/x", "internal/lane", "k1", "run-7", kernel.VerdictRed, "")
	wantRun := kernel.Event{Kind: kernel.KindRunResult, Claude: true, Lane: "lane/x", Actor: "s1", Unit: "internal/lane", Tree: "k1", Job: "run-7", Verdict: kernel.VerdictRed}
	if !reflect.DeepEqual(run, wantRun) {
		t.Errorf("run event = %+v, want %+v", run, wantRun)
	}
	if edit.Kind.Question() || run.Kind.Question() {
		t.Error("an edit and a run are facts, not questions")
	}
}

func TestFileClassOf_SeparatesCodeTestAndEverythingElse(t *testing.T) {
	for file, want := range map[string]kernel.FileClass{
		"/w/repo/internal/lane/lane.go":      kernel.ClassCode,
		"/w/repo/internal/lane/lane_test.go": kernel.ClassTest,
		"/w/repo/README.md":                  kernel.ClassOther,
		"/w/repo/vendor/x/x.go":              kernel.ClassOther,
	} {
		if got := FileClassOf(file); got != want {
			t.Errorf("FileClassOf(%s) = %q, want %q", file, got, want)
		}
	}
}

// The harness's own recordings (internal/tdd/internal/tddtest/testdata/hooks) are
// read as the adapters expect: the actor from session and agent, the write target of
// a Write, none for a Bash `ls`, and a Stop's flags.
func TestRecordedPayloads_AreReadByTheAdapters(t *testing.T) {
	const dir = "../tdd/internal/tddtest/testdata/hooks"
	read := func(name string) Payload {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		p, ok := ParsePayload(raw)
		if !ok {
			t.Fatalf("%s is not a payload", name)
		}
		return p
	}
	const session = "00000000-0000-4000-8000-000000000001"
	cases := []struct {
		file    string
		actor   string
		targets int
		isShell bool
	}{
		{"pretooluse_write.json", session, 1, false},
		{"pretooluse_subagent_write.json", session + "/a26c10f2ce9a15725", 1, false},
		{"pretooluse_bash.json", session, 0, true},
	}
	for _, c := range cases {
		p := read(c.file)
		if p.Actor() != c.actor || len(p.Targets()) != c.targets || p.IsShell() != c.isShell {
			t.Errorf("%s: actor %q targets %v shell %v, want %q, %d targets, shell %v", c.file, p.Actor(), p.Targets(), p.IsShell(), c.actor, c.targets, c.isShell)
		}
	}
	stop := StopEvent(read("stop.json"), "lane/x", true)
	if stop.Kind != kernel.KindStop || stop.Actor != session || !stop.UnseenRed || stop.StopActive {
		t.Errorf("Stop event = %+v", stop)
	}
	sub := StopEvent(read("subagentstop.json"), "lane/x", false)
	if sub.Actor != session+"/a26c10f2ce9a15725" || sub.UnseenRed {
		t.Errorf("SubagentStop event = %+v", sub)
	}
}
