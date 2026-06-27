package tdd

import (
	"encoding/json"
	"testing"
)

func decide(t *testing.T, payload string) Decision {
	t.Helper()
	d, err := DecidePreEdit([]byte(payload))
	if err != nil {
		t.Fatalf("DecidePreEdit(%s): %v", payload, err)
	}
	return d
}

func TestDecidePreEdit(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    Action
	}{
		{
			name:    "non-edit tool allowed",
			payload: `{"tool_name":"Bash","tool_input":{"command":"it.only()"}}`,
			want:    Allow,
		},
		{
			name:    "source edit flows even with a smell",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"src/widget.go","new_string":"time.Sleep(2)"}}`,
			want:    Allow,
		},
		{
			name:    "test edit with sleep blocks",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"src/widget_test.go","new_string":"time.Sleep(2)"}}`,
			want:    Block,
		},
		{
			name:    "clean test edit flows",
			payload: `{"tool_name":"Write","tool_input":{"file_path":"src/widget_test.go","content":"assert x == y"}}`,
			want:    Allow,
		},
		{
			name:    "tautology in test blocks",
			payload: `{"tool_name":"Write","tool_input":{"file_path":"a.test.ts","content":"expect(x).toBe(x)"}}`,
			want:    Block,
		},
		{
			name:    "MultiEdit edits[] are inspected",
			payload: `{"tool_name":"MultiEdit","tool_input":{"file_path":"a_test.py","edits":[{"new_string":"assert x == x"}]}}`,
			want:    Block,
		},
		{
			name:    "edit without a path is allowed",
			payload: `{"tool_name":"Edit","tool_input":{"new_string":"it.only()"}}`,
			want:    Allow,
		},
		{
			name:    "disabled test blocks",
			payload: `{"tool_name":"Write","tool_input":{"file_path":"a.test.ts","content":"it.skip('x', () => {})"}}`,
			want:    Block,
		},
		{
			name:    "go t.Skip in test blocks",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"w_test.go","new_string":"func TestX(t *testing.T){ t.Skip() }"}}`,
			want:    Block,
		},
		{
			name:    "suppression in SOURCE file warns (not block)",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"src/widget.go","new_string":"x := f() //nolint:errcheck"}}`,
			want:    Warn,
		},
		{
			name:    "suppression in TEST file warns (smells would block, this is just a warn)",
			payload: `{"tool_name":"Write","tool_input":{"file_path":"a.test.ts","content":"const x = y // @ts-ignore"}}`,
			want:    Warn,
		},
		{
			name:    "clean source edit allowed",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"src/widget.go","new_string":"return x + 1"}}`,
			want:    Allow,
		},
		{
			name:    "suppression quoted in a string does not trip",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"src/widget.go","new_string":"msg := \"use //nolint to skip\""}}`,
			want:    Allow,
		},
		{
			// In JS/TS, `#` is a private field, NOT a comment — the masker must
			// not skip the rest of the line, or the quoted directive leaks.
			name:    "JS private field with directive-in-string does not trip",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"src/widget.ts","new_string":"this.#count = \"use // nolint maybe\""}}`,
			want:    Allow,
		},
		{
			// In Python, `#` IS a comment, so a real directive there warns.
			name:    "python type-ignore comment warns",
			payload: `{"tool_name":"Edit","tool_input":{"file_path":"app.py","new_string":"x = legacy()  # type: ignore"}}`,
			want:    Warn,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decide(t, c.payload).Action; got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestDecidePreEdit_ParseError(t *testing.T) {
	if _, err := DecidePreEdit([]byte("{not json")); err == nil {
		t.Fatal("expected a parse error for malformed JSON")
	}
}

// zigEdit builds a Write payload for a .zig file with the given content,
// json-encoding so multi-line zig source with quotes survives intact.
func zigEdit(t *testing.T, path, content string) string {
	t.Helper()
	in := map[string]any{
		"tool_name":  "Write",
		"tool_input": map[string]any{"file_path": path, "content": content},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(b)
}

func TestDecidePreEdit_Zig(t *testing.T) {
	// A zeta-style src file: production code that legitimately sleeps, plus an
	// inline test. The production sleep must NOT block; only the test body is
	// gated. We vary the inline test's body to exercise each oracle smell.
	srcWith := func(testBody string) string {
		return "const std = @import(\"std\");\n" +
			"\n" +
			"pub fn backoff() void {\n" +
			"    std.time.sleep(50 * std.time.ns_per_ms); // real production wait — fine\n" +
			"}\n" +
			"\n" +
			"test \"backoff retries\" {\n" +
			testBody +
			"}\n"
	}

	cases := []struct {
		name    string
		path    string
		content string
		want    Action
	}{
		{
			name:    "inline test sleep blocks (src file, block-scoped to the test)",
			path:    "src/backoff.zig",
			content: srcWith("    std.time.sleep(10 * std.time.ns_per_ms);\n"),
			want:    Block,
		},
		{
			name:    "inline test self-compare expectEqual blocks",
			path:    "src/backoff.zig",
			content: srcWith("    try std.testing.expectEqual(got, got);\n"),
			want:    Block,
		},
		{
			name:    "inline test SkipZigTest blocks",
			path:    "src/backoff.zig",
			content: srcWith("    return error.SkipZigTest;\n"),
			want:    Block,
		},
		{
			name: "production sleep with a clean inline test flows",
			path: "src/backoff.zig",
			// The only sleep is in production code; the test is clean.
			content: srcWith("    try std.testing.expectEqual(@as(u8, 3), tries());\n"),
			want:    Allow,
		},
		{
			name: "src file with NO test block: production smells never gate",
			path: "src/backoff.zig",
			content: "pub fn backoff() void {\n" +
				"    std.time.sleep(50 * std.time.ns_per_ms);\n" +
				"}\n",
			want: Allow,
		},
		{
			name: "explicit _test.zig is gated whole-file",
			path: "src/backoff_test.zig",
			// The sleep is NOT inside a test block, yet a *_test.zig is all test,
			// so it is gated whole-file and blocks.
			content: "const std = @import(\"std\");\n" +
				"fn helper() void {\n" +
				"    std.time.sleep(10);\n" +
				"}\n",
			want: Block,
		},
		{
			name: "tests/ dir .zig is gated whole-file",
			path: "tests/integration.zig",
			content: "fn h() void {\n" +
				"    return error.SkipZigTest;\n" +
				"}\n",
			want: Block,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decide(t, zigEdit(t, c.path, c.content)).Action; got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestRenderPreToolUse(t *testing.T) {
	// Block renders a deny envelope and exits 2.
	body, code := RenderPreToolUse(Decision{Action: Block, Reason: "bad"})
	if code != 2 {
		t.Fatalf("block exit = %d, want 2", code)
	}
	var out preToolUseOutput
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("block payload not JSON: %v", err)
	}
	if out.Decision != "block" || out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("unexpected block envelope: %+v", out)
	}

	// Allow is silent: no payload, exit 0.
	if body, code := RenderPreToolUse(Decision{Action: Allow}); body != nil || code != 0 {
		t.Fatalf("allow = (%q, %d), want (nil, 0)", body, code)
	}
}
