package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseRatchet(t *testing.T) {
	tests := []struct {
		name    string
		res     result
		want    hits
		wantErr string
	}{
		{name: "clean tree", res: result{Stdout: `{"laws":3,"findings":null}`}, want: hits{}},
		{
			name: "a hit counts what is over its baseline",
			res:  result{Exit: 1, Stdout: `{"findings":[{"law":"module_size","file":"web/a.ts","key":"web/a.ts","baseline":2,"measured":5}]}`},
			want: hits{{"ratchet", "web/a.ts", "module_size: web/a.ts"}: 3},
		},
		{
			name: "a hit at its baseline is still one hit",
			res:  result{Exit: 1, Stdout: `{"findings":[{"law":"x","file":"a.py","key":"k","baseline":0,"measured":0}]}`},
			want: hits{{"ratchet", "a.py", "x: k"}: 1},
		},
		{
			name: "excess wins over the measured gap",
			res:  result{Exit: 1, Stdout: `{"findings":[{"law":"x","file":"a.py","key":"k","baseline":1,"measured":2,"excess":4}]}`},
			want: hits{{"ratchet", "a.py", "x: k"}: 4},
		},
		{
			name: "Windows separators read as slashes",
			res:  result{Exit: 1, Stdout: `{"findings":[{"law":"x","file":"web\\src\\a.ts","key":"k","baseline":0,"measured":2}]}`},
			want: hits{{"ratchet", "web/src/a.ts", "x: k"}: 2},
		},
		{
			name: "a law the binary does not judge is a finding",
			res:  result{Stdout: `{"findings":null,"skipped_laws":[{"law":"no_any","kind":"new-kind"}]}`},
			want: hits{{"ratchet", "", "law not judged: no_any (new-kind)"}: 1},
		},
		{name: "exit 2 is no verdict", res: result{Exit: 2, Stderr: "usage"}, wantErr: "exit 2"},
		{name: "a recovered panic is no verdict", res: result{Exit: 1, Stdout: `{}`, Stderr: "panic: nil map\ngoroutine 1"}, wantErr: "panic"},
		{name: "prose is no verdict", res: result{Stdout: "ratchet: 3 laws"}, wantErr: "no verdict"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRatchet(tc.res)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("hits = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseDocs(t *testing.T) {
	tests := []struct {
		name    string
		res     result
		want    hits
		wantErr string
	}{
		{name: "clean", res: result{}, want: hits{}},
		{
			name: "a miss is keyed by file and path, never by line",
			res:  result{Exit: 1, Stdout: "docs/a.md:12: unresolved reference: gone/x.go\ndocs/a.md:30: unresolved reference: gone/x.go\n"},
			want: hits{{"docs", "docs/a.md", "gone/x.go"}: 2},
		},
		{
			name: "a line it does not know is kept, so a reworded message shows",
			res:  result{Exit: 1, Stdout: "docs: broken link in a.md\n"},
			want: hits{{"docs", "", "docs: broken link in a.md"}: 1},
		},
		{name: "exit 3 is no verdict", res: result{Exit: 3}, wantErr: "exit 3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDocs(tc.res)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("hits = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseGate(t *testing.T) {
	const tree = `D:\a\replay\tree`
	deny := `{"decision":"block","reason":"ratchet: comment_hygiene D:\\a\\replay\\tree\\web\\replaynew_a.ts:4 TODO","hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"ratchet: comment_hygiene D:\\a\\replay\\tree\\web\\replaynew_a.ts:4 TODO"}}`
	warn := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"gate: a suppression on D:/a/replay/tree/web/replaynew_a.ts"}}`
	tests := []struct {
		name    string
		res     result
		want    hits
		wantErr string
	}{
		{name: "allowed says nothing", res: result{}, want: hits{}},
		{
			name: "a deny names the file as the tree has it, whatever the host's separators",
			res:  result{Exit: 2, Stdout: deny},
			want: hits{{"gate", "web/a.ts", "deny: ratchet: comment_hygiene <tree>/web/a.ts:4 TODO"}: 1},
		},
		{
			name: "a warning is a finding of its own kind",
			res:  result{Stdout: warn},
			want: hits{{"gate", "web/a.ts", "warn: gate: a suppression on <tree>/web/a.ts"}: 1},
		},
		{name: "exit 1 is no verdict of a hook", res: result{Exit: 1, Stderr: "boom"}, wantErr: "exit 1"},
		{name: "a deny with no reason is no verdict", res: result{Exit: 2, Stdout: "nope"}, wantErr: "no verdict"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseGate("web/a.ts", tree, tc.res)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("hits = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	a := finding{"ratchet", "a.ts", "law: k"}
	b := finding{"ratchet", "b.ts", "law: k"}
	c := finding{"docs", "c.md", "gone/x"}
	tests := []struct {
		name                string
		prev, cand          hits
		wantAdded, wantGone []delta
	}{
		{name: "same", prev: hits{a: 2}, cand: hits{a: 2}},
		{name: "a hit only the candidate has", prev: hits{}, cand: hits{a: 3}, wantAdded: []delta{{a, 3}}},
		{name: "more of a hit the previous release had", prev: hits{a: 1}, cand: hits{a: 4}, wantAdded: []delta{{a, 3}}},
		{name: "fewer is dropped, not added", prev: hits{a: 4}, cand: hits{a: 1}, wantGone: []delta{{a, 3}}},
		{name: "a hit only the previous release had", prev: hits{b: 2}, cand: hits{}, wantGone: []delta{{b, 2}}},
		{
			name:      "sorted by leg, file, what",
			prev:      hits{},
			cand:      hits{b: 1, c: 1, a: 1},
			wantAdded: []delta{{c, 1}, {a, 1}, {b, 1}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			added, gone := compare(tc.prev, tc.cand)
			if !reflect.DeepEqual(added, tc.wantAdded) {
				t.Errorf("added = %v, want %v", added, tc.wantAdded)
			}
			if !reflect.DeepEqual(gone, tc.wantGone) {
				t.Errorf("dropped = %v, want %v", gone, tc.wantGone)
			}
		})
	}
}

func TestSettle(t *testing.T) {
	a := finding{"ratchet", "a.ts", "law: k"}
	boom := errors.New("exit 2")
	tests := []struct {
		name       string
		mustRead   bool
		prev, cand legOut
		wantFail   bool
		wantNote   string
		wantAdded  int
	}{
		{name: "equal readings pass", mustRead: true, prev: legOut{Hits: hits{a: 1}}, cand: legOut{Hits: hits{a: 1}}},
		{name: "a new hit fails", mustRead: true, prev: legOut{Hits: hits{}}, cand: legOut{Hits: hits{a: 2}}, wantFail: true, wantAdded: 1},
		{name: "the candidate failing to read fails", prev: legOut{Hits: hits{}}, cand: legOut{Err: boom}, wantFail: true},
		{
			name:     "the previous release failing a tree it must read fails: it cannot read what the candidate wrote",
			mustRead: true, prev: legOut{Err: boom}, cand: legOut{Hits: hits{}}, wantFail: true,
		},
		{
			name: "the previous release failing a tree it need not read is a note, not a failure",
			prev: legOut{Err: boom}, cand: legOut{Hits: hits{a: 9}}, wantNote: "previous release cannot read",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := settle("self", "ratchet", tc.mustRead, tc.prev, tc.cand)
			if rep.failed() != tc.wantFail {
				t.Fatalf("failed() = %v, want %v: %+v", rep.failed(), tc.wantFail, rep)
			}
			if len(rep.Added) != tc.wantAdded {
				t.Errorf("added = %v, want %d", rep.Added, tc.wantAdded)
			}
			if tc.wantNote != "" && !strings.Contains(rep.Note, tc.wantNote) {
				t.Errorf("note = %q, want it to contain %q", rep.Note, tc.wantNote)
			}
		})
	}
}

func TestRender_NamesTheDifferingFindingsAndCapsThem(t *testing.T) {
	var added []delta
	for i := range 45 {
		added = append(added, delta{finding{"ratchet", fmt.Sprintf("f%02d.ts", i), "law: k"}, 1})
	}
	out := render([]legReport{
		{Tree: "synthetic", Leg: "ratchet", Added: added, PrevTotal: 10, CandTotal: 55},
		{Tree: "self", Leg: "docs"},
	})
	for _, want := range []string{
		"FAIL synthetic ratchet: the candidate adds 45 hit(s)",
		"+ ratchet f00.ts law: k (x1)",
		"+ ratchet f39.ts law: k (x1)",
		"... and 5 more",
		"ok   self docs",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "f40.ts") {
		t.Errorf("report prints past its cap:\n%s", out)
	}
}
