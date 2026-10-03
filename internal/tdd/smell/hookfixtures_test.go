package smell

import (
	"encoding/json"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// The edit-time reader decodes the recorded Write payloads of the real harness,
// the main session's and a subagent's, through the struct the hook uses. A
// field the gate reads that the recording does not carry shows as an empty
// value, and the test names it.
func TestRecordedWritePayloads_DecodeThroughThePreEditSmellReader(t *testing.T) {
	for _, file := range []string{"pretooluse_write.json", "pretooluse_subagent_write.json"} {
		t.Run(file, func(t *testing.T) {
			var c tddtest.HookCase
			for _, known := range tddtest.HookCases {
				if known.File == file {
					c = known
				}
			}
			var in preToolUseInput
			if err := json.Unmarshal(tddtest.HookFixture(t, file), &in); err != nil {
				t.Fatal(err)
			}

			if in.ToolName != "Write" || in.ToolInput.FilePath != c.FilePath || in.ToolInput.Content != c.Content {
				t.Fatalf("decoded %+v, want Write of %q with content %q", in, c.FilePath, c.Content)
			}
		})
	}
}
