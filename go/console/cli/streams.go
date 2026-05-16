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
)

const streamsAnnotationKey = "streams"

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
// If --stream includes this name, writes to stderr with [name] prefix.
// Otherwise returns io.Discard (zero-cost no-op).
// Thread-safe.
func Channel(cmd *cobra.Command, name string) io.Writer {
	if !streamEnabled(cmd, name) {
		return io.Discard
	}
	dim := lipgloss.NewStyle().Faint(true)
	prefix := dim.Render(fmt.Sprintf("[%s] ", name))
	return &streamChannel{
		prefix: prefix,
		w:      os.Stderr,
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

// ensureStreamFlag registers --stream on cmd once.
func ensureStreamFlag(cmd *cobra.Command) {
	if cmd.Flags().Lookup("stream") != nil {
		return
	}
	cmd.Flags().StringSlice("stream", nil,
		"Enable named output streams (comma-separated)")
}

// streamEnabled checks if name is in the --stream flag value.
func streamEnabled(cmd *cobra.Command, name string) bool {
	f := cmd.Flags().Lookup("stream")
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
