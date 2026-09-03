package clauderun

import (
	"bytes"
	"strings"
	"testing"
)

// The following tests exercise renderStream, the stream-json log formatter
// used by Run under CI_REGENERATION. This is the third cross-cutting
// concern (after claudeAvailable/claudeArgs) whose duplication across all 14
// client Magefiles the extraction was meant to prevent -- see this
// package's doc comment. renderStream is a pure function over an
// io.Reader/io.Writer pair with no dependency on the claude binary, so it is
// tested directly, in-package, rather than through Run.

// TestRenderStreamAssistantText covers an "assistant" event carrying a
// "text" content block: the text itself must appear in the rendered output.
func TestRenderStreamAssistantText(t *testing.T) {
	input := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello there."}]}}` + "\n"

	var out bytes.Buffer
	renderStream(strings.NewReader(input), &out)

	if got := out.String(); !strings.Contains(got, "Hello there.") {
		t.Fatalf("renderStream(%q) = %q, want it to contain the assistant text", input, got)
	}
}

// TestRenderStreamAssistantToolUse covers an "assistant" event carrying a
// "tool_use" content block: both the tool name and a short hint drawn from
// its input (here, "command") must appear.
func TestRenderStreamAssistantToolUse(t *testing.T) {
	input := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls -la /tmp"}}]}}` + "\n"

	var out bytes.Buffer
	renderStream(strings.NewReader(input), &out)

	got := out.String()
	if !strings.Contains(got, "Bash") {
		t.Fatalf("renderStream(%q) = %q, want it to name the tool", input, got)
	}
	if !strings.Contains(got, "ls -la /tmp") {
		t.Fatalf("renderStream(%q) = %q, want it to include the command hint", input, got)
	}
}

// TestRenderStreamResultSummary covers the terminal "result" event: the
// summary line must report the subtype, turn count, duration, and cost --
// cost in particular, since a previous incident in this pipeline made a
// failed regeneration look successful (see Run's doc comment).
func TestRenderStreamResultSummary(t *testing.T) {
	input := `{"type":"result","subtype":"success","num_turns":2,"duration_ms":7000,"total_cost_usd":0.2222,"is_error":false}` + "\n"

	var out bytes.Buffer
	renderStream(strings.NewReader(input), &out)

	got := out.String()
	for _, want := range []string{"success", "2 turns", "7s", "$0.2222"} {
		if !strings.Contains(got, want) {
			t.Fatalf("renderStream(%q) = %q, want it to contain %q", input, got, want)
		}
	}
}

// TestRenderStreamMalformedLineEchoedVerbatim pins the rule that a line
// renderStream cannot parse is printed as-is rather than dropped. A previous
// fix in this repo piped stderr to /dev/null and hid the exact error that
// would have diagnosed a failure in seconds; this is the opposite of that.
func TestRenderStreamMalformedLineEchoedVerbatim(t *testing.T) {
	input := "not json at all\n"

	var out bytes.Buffer
	renderStream(strings.NewReader(input), &out)

	if got := strings.TrimSpace(out.String()); got != "not json at all" {
		t.Fatalf("renderStream(%q) = %q, want the malformed line echoed verbatim", input, got)
	}
}

// TestRenderStreamSkipsSystemAndRateLimitEvents covers the two event types
// that carry no useful information for a human watching CI logs: "system"
// (init/hook events, which are enormous) and "rate_limit_event".
func TestRenderStreamSkipsSystemAndRateLimitEvents(t *testing.T) {
	input := `{"type":"system","subtype":"init","tools":["a","b","c"]}` + "\n" +
		`{"type":"rate_limit_event","detail":"..."}` + "\n"

	var out bytes.Buffer
	renderStream(strings.NewReader(input), &out)

	if got := out.String(); got != "" {
		t.Fatalf("renderStream(%q) = %q, want no output for system/rate_limit_event lines", input, got)
	}
}
