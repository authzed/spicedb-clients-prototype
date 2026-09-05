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

// TestRenderStreamDropsSystemEventBulk covers what is still skipped from a
// system event. The tool list, working directory and hook wiring are large and
// say nothing about what a run did; only the model is kept, by
// TestRenderStreamRecordsTheModel.
//
// This test previously asserted that "rate_limit_event" was skipped too. That
// was wrong, and the cost was concrete: run 33810252060 hit its quota as seven
// simultaneous failures with nothing in the log leading up to them, because the
// only warning had been dropped here.
func TestRenderStreamDropsSystemEventBulk(t *testing.T) {
	input := `{"type":"system","subtype":"init","tools":["a","b","c"],"cwd":"/w","model":"m"}` + "\n"

	var out bytes.Buffer
	renderStream(strings.NewReader(input), &out)

	got := out.String()
	for _, unwanted := range []string{`"a"`, `"b"`, `"c"`, "/w", "tools"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("renderStream leaked %q from a system event: %q", unwanted, got)
		}
	}
}

// The "system" and "rate_limit_event" types were both dropped wholesale. That
// cost two things worth having: the model a run used, which nothing else
// records, and any warning that a quota window was about to end. These pin the
// narrow slice of each that is now kept.

func TestRenderStreamRecordsTheModel(t *testing.T) {
	input := `{"type":"system","subtype":"init","model":"claude-opus-5","tools":["Bash","Read"],"cwd":"/w"}` + "\n"

	var out bytes.Buffer
	renderStream(strings.NewReader(input), &out)

	got := out.String()
	if !strings.Contains(got, "claude-opus-5") {
		t.Fatalf("renderStream(%q) = %q, want the model recorded", input, got)
	}
	// The rest of an init event is large and says nothing about the run.
	for _, unwanted := range []string{"Bash", "/w"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("renderStream leaked %q from the init event: %q", unwanted, got)
		}
	}
}

func TestRenderStreamIgnoresNonInitSystemEvents(t *testing.T) {
	input := `{"type":"system","subtype":"hook","hook":"PreToolUse","payload":"noise"}` + "\n"

	var out bytes.Buffer
	renderStream(strings.NewReader(input), &out)

	if got := strings.TrimSpace(out.String()); got != "" {
		t.Fatalf("a non-init system event should render nothing, got %q", got)
	}
}

// A system init that carries no model must print nothing rather than a bare
// "model:" with an empty value.
func TestRenderStreamOmitsAnAbsentModel(t *testing.T) {
	input := `{"type":"system","subtype":"init","tools":[]}` + "\n"

	var out bytes.Buffer
	renderStream(strings.NewReader(input), &out)

	if got := strings.TrimSpace(out.String()); got != "" {
		t.Fatalf("an init event without a model should render nothing, got %q", got)
	}
}

// Rate-limit events are the only advance warning that a quota window is
// running out. Dropping them is why run 33810252060 hit the limit as seven
// simultaneous failures with nothing in the log leading up to them.
func TestRenderStreamSurfacesRateLimitEvents(t *testing.T) {
	input := `{"type":"rate_limit_event","status":"approaching","resets_at":"2026-09-04T02:50:00Z"}` + "\n"

	var out bytes.Buffer
	renderStream(strings.NewReader(input), &out)

	got := out.String()
	for _, want := range []string{"rate_limit_event", "approaching", "2026-09-04T02:50:00Z"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rate-limit event must reach the log; %q missing from %q", want, got)
		}
	}
}

// Nothing is pinned by default -- the CLI picks a model per task -- but the
// choice lives in one place and CLAUDE_MODEL can pin it for a run. These cover
// both states, including that an unpinned run passes no --model flag at all.

func TestModelUnpinnedByDefault(t *testing.T) {
	t.Setenv(ModelEnv, "")

	if got := Model(); got != "" {
		t.Fatalf("Model() = %q, want empty so the CLI chooses", got)
	}
	if args := ModelArgs(); len(args) != 0 {
		t.Fatalf("ModelArgs() = %v, want nothing spliced into the command", args)
	}
}

func TestModelEnvPinsTheModel(t *testing.T) {
	t.Setenv(ModelEnv, "claude-opus-5")

	if got := Model(); got != "claude-opus-5" {
		t.Fatalf("Model() = %q, want the %s pin to win", got, ModelEnv)
	}
	args := ModelArgs()
	if len(args) != 2 || args[0] != "--model" || args[1] != "claude-opus-5" {
		t.Fatalf("ModelArgs() = %v, want [--model claude-opus-5]", args)
	}
}

// Whitespace-only is treated as unset: a CI expression that resolves to an
// empty string would otherwise pin the model to " " and fail the run.
func TestModelIgnoresABlankPin(t *testing.T) {
	t.Setenv(ModelEnv, "   ")

	if got := Model(); got != "" {
		t.Fatalf("Model() = %q, want a blank pin ignored", got)
	}
	if args := ModelArgs(); len(args) != 0 {
		t.Fatalf("ModelArgs() = %v, want a blank pin to splice nothing", args)
	}
}
