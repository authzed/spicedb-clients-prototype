// Package clauderun provides the Claude-CLI invocation shared by every
// client's Magefile.go. Before this package existed, three helpers --
// claudeAvailable, claudeArgs, and runClaude -- were duplicated byte-for-byte
// across all 14 Magefiles (7 proto-clients/*, 7 spicedb-*). That duplication
// was an accepted tradeoff in the original design, on the condition that a
// third cross-cutting concern would trigger extraction. The stream-json log
// renderer added here is that concern: unlike claudeAvailable/claudeArgs, it
// is real logic (JSON parsing, buffer sizing, error passthrough) that would
// otherwise drift silently across 14 copies. So the invocation is defined
// once, here, instead of 14 times.
package clauderun

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Available reports whether the claude CLI can be invoked and is expected to
// be authenticated.
//
// Returns false when CI is set, because the binary may be present but
// unauthenticated -- with one exception. CI_REGENERATION is set by exactly one
// workflow, .github/workflows/regen-from-api.yaml, which is also the only
// workflow holding Claude credentials. Every other CI job -- notably
// meta.yaml's gen-nodiff -- must keep taking the false branch, or `mage
// gen:all` starts making unreviewed changes inside a check whose whole purpose
// is asserting that generation produces no diff.
func Available() bool {
	if os.Getenv("CI") != "" && os.Getenv("CI_REGENERATION") == "" {
		return false
	}
	_, err := exec.LookPath("claude")
	return err == nil
}

// Run pipes prompt to claude and streams its output to stdout.
//
// Locally (CI_REGENERATION unset) this is byte-for-byte what every Magefile
// did before extraction: claude invoked with no flags, stdin set to prompt,
// stdout/stderr inherited from the parent process.
//
// Under CI_REGENERATION -- set only by .github/workflows/regen-from-api.yaml,
// the one workflow holding Claude credentials -- two flags were already
// required before this package existed:
//
//	--print                              a CI runner has no TTY; the bare
//	                                     interactive form has never run there
//	--permission-mode bypassPermissions  without it Claude replies "I don't
//	                                     have permission to write this file",
//	                                     edits nothing, and still exits 0 --
//	                                     so a regeneration reports success and
//	                                     produces an empty PR (observed on
//	                                     runs 33010304938 vs 33010790114)
//
// This package adds two more, to get structured, streamable progress output
// in CI logs instead of one big blob at the end:
//
//	--output-format stream-json  newline-delimited JSON, one event per line
//	--verbose                    required by the CLI when combining --print
//	                             with --output-format stream-json
//
// The NDJSON stream is rendered into readable lines by renderStream. The
// bypass and the streaming flags are both confined to that one workflow,
// which runs on an ephemeral single-purpose container. Local runs keep
// normal interactive prompting and are untouched by any of this.
func Run(prompt string) error {
	if os.Getenv("CI_REGENERATION") == "" {
		cmd := exec.Command("claude")
		cmd.Stdin = strings.NewReader(prompt)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	cmd := exec.Command("claude",
		"--print", "--permission-mode", "bypassPermissions",
		"--output-format", "stream-json", "--verbose",
	)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("clauderun: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("clauderun: start claude: %w", err)
	}

	// Render concurrently with the process running; renderStream returns once
	// stdout is closed (i.e. once claude exits), so this does not race Wait.
	renderStream(stdout, os.Stdout)

	// The exit code must come from claude, not the renderer: Wait is what
	// reports it. A renderer that swallowed the stream and returned nil here
	// would turn a failed generation into a silent success.
	return cmd.Wait()
}

// streamEvent is one line of claude's --output-format stream-json output.
// Fields not used by the renderer are left unmapped; json.Unmarshal ignores
// them.
type streamEvent struct {
	Type    string        `json:"type"`
	Message *assistantMsg `json:"message,omitempty"`

	// Model is carried by the "system"/"init" event. It is the single most
	// important determinant of what a regeneration produces, and until this
	// was recorded no run's output could be attributed to a model at all --
	// the pipeline pins the upstream API commit precisely and left this,
	// which matters more, unlogged.
	Model string `json:"model,omitempty"`

	// result fields
	Subtype      string  `json:"subtype,omitempty"`
	NumTurns     int     `json:"num_turns,omitempty"`
	DurationMS   int64   `json:"duration_ms,omitempty"`
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`
	IsError      bool    `json:"is_error,omitempty"`
}

type assistantMsg struct {
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`

	// text blocks
	Text string `json:"text,omitempty"`

	// tool_use blocks
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// maxLineSize bounds how large a single stream-json line is allowed to grow
// before renderStream gives up on it. bufio.Scanner's default (64KB) is
// exceeded by claude's own "system" init event, which enumerates every
// available tool and is routinely several hundred KB; a few MB comfortably
// covers that with headroom.
const maxLineSize = 4 * 1024 * 1024

// initialLineSize is the scanner's starting buffer, grown up to maxLineSize
// as needed.
const initialLineSize = 64 * 1024

// renderStream reads claude's --output-format stream-json NDJSON output from
// r, one event per line, and writes a human-readable rendering of it to w.
// Run is its only caller.
//
// Three correctness rules matter more than the formatting:
//
//  1. A line that fails to unmarshal is printed verbatim, never dropped. A
//     previous fix in this repo hid a diagnosable failure behind
//     2>/dev/null; this is the opposite of that.
//  2. renderStream never decides the exit code -- see Run, which calls
//     cmd.Wait() after this returns and returns that error.
//  3. The scanner buffer is sized up front (see maxLineSize) and
//     scanner.Err() is checked after the loop, so a line that is too large
//     produces a clear notice instead of a silent truncation.
func renderStream(r io.Reader, w io.Writer) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, initialLineSize), maxLineSize)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var evt streamEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			// Never swallow output we can't parse -- print it as-is so a
			// format change or CLI bug is visible instead of silently hidden.
			fmt.Fprintln(w, line)
			continue
		}

		switch evt.Type {
		case "system":
			renderSystem(w, evt)
		case "rate_limit_event":
			// Printed verbatim rather than parsed. This is the only advance
			// warning that a quota window is running out, and dropping it is
			// why regeneration run 33810252060 met the limit as seven
			// simultaneous failures with nothing in the log leading up to
			// them. The event's shape is not documented, so printing it whole
			// is both the honest and the robust choice.
			fmt.Fprintln(w, line)
		case "assistant":
			renderAssistant(w, evt)
		case "result":
			renderResult(w, evt)
		default:
			// An event type we don't specifically know about. Print it
			// verbatim rather than guessing it's safe to drop.
			fmt.Fprintln(w, line)
		}
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			fmt.Fprintf(w, "clauderun: a stream-json line exceeded the %d-byte buffer and was skipped\n", maxLineSize)
		} else {
			fmt.Fprintf(w, "clauderun: error reading claude output: %v\n", err)
		}
	}
}

// renderAssistant writes each content block of an "assistant" event: text
// blocks as indented prose, tool_use blocks as "-> Name hint".
func renderAssistant(w io.Writer, evt streamEvent) {
	if evt.Message == nil {
		return
	}
	for _, block := range evt.Message.Content {
		switch block.Type {
		case "text":
			text := strings.TrimRight(block.Text, "\n")
			if strings.TrimSpace(text) == "" {
				continue
			}
			for line := range strings.SplitSeq(text, "\n") {
				fmt.Fprintf(w, "  %s\n", line)
			}
		case "tool_use":
			name := block.Name
			if name == "" {
				name = "tool"
			}
			if hint := toolHint(block.Input); hint != "" {
				fmt.Fprintf(w, "  -> %s %s\n", name, hint)
			} else {
				fmt.Fprintf(w, "  -> %s\n", name)
			}
		}
	}
}

// maxHintLen bounds how much of a tool_use input value is echoed. The point
// is a short orientation, not a dump of the whole input object.
const maxHintLen = 100

// toolHint extracts a short, human-meaningful hint from a tool_use input
// object: whichever of file_path, command, or pattern is present first,
// truncated to maxHintLen.
func toolHint(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var fields map[string]any
	if err := json.Unmarshal(input, &fields); err != nil {
		return ""
	}
	for _, key := range []string{"file_path", "command", "pattern"} {
		v, ok := fields[key].(string)
		if ok && v != "" {
			return truncate(v, maxHintLen)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// renderResult writes the "result" event's summary line.
// renderSystem prints the one field of the init event worth keeping. The rest
// of a system event -- the tool list, the working directory, hook wiring -- is
// large and says nothing about what the run did, which is why the whole event
// used to be skipped along with it.
func renderSystem(w io.Writer, evt streamEvent) {
	if evt.Subtype == "init" && evt.Model != "" {
		fmt.Fprintf(w, "  == model: %s\n", evt.Model)
	}
}

func renderResult(w io.Writer, evt streamEvent) {
	seconds := float64(evt.DurationMS) / 1000.0
	fmt.Fprintf(w, "  == %s | %d turns | %.0fs | $%.4f\n", evt.Subtype, evt.NumTurns, seconds, evt.TotalCostUSD)
}
