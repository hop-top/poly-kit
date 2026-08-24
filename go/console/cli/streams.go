package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"hop.top/kit/contracts/parity"
)

const streamsAnnotationKey = "streams"

// streamLabel renders a stream's prefix from the parity contract's
// streams.label_format template. The template's "{name}" placeholder is
// substituted with the stream name; a trailing space separates the label
// from the payload.
//
// Taking the contract as a parameter (rather than reading parity.Values)
// keeps the rendering testable against a constructed Data without
// mutating the shared parity.json.
func streamLabel(d *parity.Data, name string) string {
	format := d.Streams.LabelFormat
	if format == "" {
		format = "[{name}]"
	}
	return strings.ReplaceAll(format, "{name}", name) + " "
}

// streamOutput resolves the parity contract's streams.output destination
// to a writer. Anything other than "stdout" resolves to stderr, which is
// the contract's declared destination.
func streamOutput(d *parity.Data) io.Writer {
	if d.Streams.Output == "stdout" {
		return os.Stdout
	}
	return os.Stderr
}

// StreamDef describes a named stream registered on a command.
type StreamDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// RegisterStream registers a named stream on a command.
// Streams appear in that command's STREAMS help section.
func RegisterStream(cmd *cobra.Command, name, description string) {
	defs := loadStreamDefs(cmd)
	defs = append(defs, StreamDef{Name: name, Description: description})
	saveStreamDefs(cmd, defs)
	ensureStreamFlag(cmd)
	ensureStreamsUsage(cmd)
}

// Channel returns an io.Writer for the named stream.
// If --stream includes this name, writes to the parity contract's
// streams.output destination, prefixed with the contract's
// streams.label_format label.
// Otherwise returns io.Discard (zero-cost no-op).
// Thread-safe.
func Channel(cmd *cobra.Command, name string) io.Writer {
	if !streamEnabled(cmd, name) {
		return io.Discard
	}
	dim := lipgloss.NewStyle().Faint(true)
	return &streamChannel{
		prefix: dim.Render(streamLabel(&parity.Values, name)),
		w:      streamOutput(&parity.Values),
	}
}

// streamChannel is a thread-safe writer that prepends a prefix to each Write.
type streamChannel struct {
	prefix string
	mu     sync.Mutex
	w      io.Writer
}

func (c *streamChannel) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	lines := strings.SplitAfter(string(p), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if _, err := fmt.Fprint(c.w, c.prefix+line); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// ── flag + annotation helpers ───────────────────────────────────────────────

func loadStreamDefs(cmd *cobra.Command) []StreamDef {
	if cmd.Annotations == nil {
		return nil
	}
	raw, ok := cmd.Annotations[streamsAnnotationKey]
	if !ok {
		return nil
	}
	var defs []StreamDef
	_ = json.Unmarshal([]byte(raw), &defs)
	return defs
}

func saveStreamDefs(cmd *cobra.Command, defs []StreamDef) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	b, _ := json.Marshal(defs)
	cmd.Annotations[streamsAnnotationKey] = string(b)
}

// streamFlagName returns the long flag name declared by the parity
// contract's streams.flag, with the leading dashes stripped for cobra
// (which registers names undashed). Falls back to "stream".
func streamFlagName(d *parity.Data) string {
	name := strings.TrimLeft(d.Streams.Flag, "-")
	if name == "" {
		return "stream"
	}
	return name
}

// ensureStreamFlag registers the contract's stream flag on cmd once.
func ensureStreamFlag(cmd *cobra.Command) {
	flag := streamFlagName(&parity.Values)
	if cmd.Flags().Lookup(flag) != nil {
		return
	}
	cmd.Flags().StringSlice(flag, nil,
		"Enable named output streams (comma-separated)")
}

// streamEnabled checks if name is in the stream flag's value.
func streamEnabled(cmd *cobra.Command, name string) bool {
	f := cmd.Flags().Lookup(streamFlagName(&parity.Values))
	if f == nil || !f.Changed {
		return false
	}
	raw := f.Value.String()
	// StringSlice stores as "[a,b,c]"; strip brackets.
	raw = strings.Trim(raw, "[]")
	for _, s := range strings.Split(raw, ",") {
		if strings.TrimSpace(s) == name {
			return true
		}
	}
	return false
}

// ensureStreamsUsage appends a STREAMS section to the command's usage template.
func ensureStreamsUsage(cmd *cobra.Command) {
	cmd.SetUsageFunc(func(c *cobra.Command) error {
		// Render default usage via a temporary clone without our func.
		clone := *c
		clone.SetUsageFunc(nil)
		_ = clone.Usage()

		defs := loadStreamDefs(c)
		if len(defs) == 0 {
			return nil
		}

		// Compute max name width for alignment.
		maxLen := 0
		for _, d := range defs {
			if len(d.Name) > maxLen {
				maxLen = len(d.Name)
			}
		}

		fmt.Fprintln(c.ErrOrStderr())
		fmt.Fprintln(c.ErrOrStderr(), "STREAMS")
		for _, d := range defs {
			pad := strings.Repeat(" ", maxLen-len(d.Name))
			fmt.Fprintf(c.ErrOrStderr(), "  %s%s   %s\n",
				d.Name, pad, d.Description)
		}
		return nil
	})
}
