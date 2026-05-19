// inspect.go implements `kit telemetry inspect`: a read-only audit of
// telemetry events that have been queued for shipment but not yet
// successfully transmitted. The command emits JSONL on stdout — one
// event payload per line — so operators can pipe the output to `jq`,
// `grep`, or anything else that speaks one-object-per-line.
//
// POST-REDACT CONTRACT
// --------------------
// inspect is the trust-building subcommand: an operator who is
// considering opting in must be able to see, byte for byte, what kit
// would ship on their behalf. The redact contract is unverifiable
// without inspect.
//
// inspect reads from the on-disk telemetry spool
// (<XDG_STATE_HOME>/kit/telemetry/spool/YYYY-MM-DD.jsonl). The spool
// is written by HTTPSSink.spool only AFTER the per-event redact pass
// inside the emitter has run — every byte on disk is post-redact by
// construction. inspect itself performs NO redaction: its contract is
// "show what was written to the spool, faithfully". If redact ever
// fails upstream, the failure is visible here — exactly the property
// an audit tool needs.
//
// V1 SCOPE: PATH A
// ----------------
// The plan distinguishes "next-N pending" (events still in the
// HTTPSSink in-memory ring) from "last-N shipped" (events that DID
// ship in the recent past). v1 reads ONLY the on-disk spool:
//
//   - Spool entries are exactly the events at risk of being shipped
//     next (i.e. the next-N batch that was held back by a flush
//     failure), so the "pending" semantic is preserved on the slice
//     of events the operator most needs to audit.
//
//   - An adopter with a healthy network sees an empty spool. inspect
//     prints an informational message rather than failing; the empty
//     state IS the audit result ("nothing is queued; all events have
//     flushed").
//
//   - "last-N shipped" would require an in-memory ring snapshot from
//     the running sink. That introspection API does not exist on
//     HTTPSSink today and belongs to the sink owner. v1 defers and
//     treats --last as a synonym for --next.
//
// When a future task adds an in-memory snapshot API on the sink,
// inspect can extend without changing its on-disk path.
//
// FORMAT FLAG
// -----------
// The kit-wide --format flag (table | json | yaml | text) is not a
// natural fit for streaming event payloads: each event is an arbitrary
// JSON object whose schema depends on its event Kind. v1 always emits
// JSONL and ignores --format. The flag is consulted only insofar as
// any downstream tooling expects JSON output — JSONL is a strict
// superset, so json consumers parse line-by-line.

package telemetry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"hop.top/kit/go/core/xdg"
)

// inspectSpoolSubPath mirrors the spool subdirectory used by
// HTTPSSink. Duplicated here (rather than re-exported from the
// runtime package) because the runtime constant is unexported on
// purpose — adding an export to satisfy the CLI would invert the
// dependency direction. The path is part of the public layout
// contract; a drift here is a test-detectable bug.
const (
	inspectSpoolSubPath = "telemetry/spool"
	inspectXDGTool      = "kit"
	inspectDefaultN     = 10
)

// inspectCmd builds the `kit telemetry inspect` leaf. RunE delegates
// to runInspect so the body stays exercisable from tests without a
// cobra invocation.
func inspectCmd() *cobra.Command {
	var (
		lastN int
		nextN int
	)
	c := &cobra.Command{
		Use:   "inspect",
		Short: "Audit pending telemetry events on the local spool (post-redact)",
		Long: `Inspect reads the local telemetry spool and emits the captured
events as JSONL on stdout — one event payload per line.

The displayed payload is the EXACT post-redact payload that would be
shipped on the next successful flush. The spool is written only after
the emitter's per-event redact pass, so every byte on disk is post-
redact by construction. inspect performs no further redaction; its
contract is "show what was written to the spool, faithfully".

Flags:
  --next N   Show next N pending events (default 10 when both flags
             unset). v1 reads from the on-disk spool — these are the
             events that failed to ship and are queued for retry.
  --last N   Synonym for --next in v1. A future enhancement may add
             an in-memory snapshot of recently-shipped events.

Output:
  When the spool is empty (the common case for a healthy adopter),
  inspect prints an informational message and exits 0 — the empty
  state IS the audit result.

Format:
  Always JSONL. The kit-wide --format flag is ignored: each event is
  an arbitrary JSON object whose schema depends on its Kind, which
  does not tabularize well. JSON consumers can parse line-by-line.`,
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			"kit/side-effect": "read",
			"kit/idempotent":  "yes",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			n := lastN
			if nextN > 0 {
				n = nextN
			}
			if n <= 0 {
				n = inspectDefaultN
			}
			return runInspect(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), n)
		},
	}
	c.Flags().IntVar(&lastN, "last", 0,
		"show last N events from the spool (currently a synonym for --next)")
	c.Flags().IntVar(&nextN, "next", 0,
		"show next N pending events from the spool (default 10)")
	return c
}

// runInspect is the testable core. It resolves the spool dir, lists
// the .jsonl files newest-first, walks each one collecting events
// newest-first within the file, and emits JSONL on stdout up to n
// events. Malformed lines are skipped with a warning on stderr so a
// single bad write does not block the audit of the rest.
//
// Ordering (DOCUMENTED CONTRACT):
//   - Across files: newest filename first (filenames are ISO-8601
//     YYYY-MM-DD, so a reverse-string-sort is equivalent to a date
//     sort).
//   - Within a file: newest line first (events are appended to spool
//     files, so the last line is the most recently written).
//   - Output: oldest-first within the emitted slice would be friendly
//     for human reading, but the slice is bounded by n; reversing
//     before emit would require holding the full slice in memory.
//     v1 emits in the order it walked: newest first. Tests assert
//     this contract.
func runInspect(ctx context.Context, stdout, stderr io.Writer, n int) error {
	spoolDir, err := inspectSpoolDirPath()
	if err != nil {
		return fmt.Errorf("inspect: resolve spool dir: %w", err)
	}

	files, err := inspectListSpoolFiles(spoolDir)
	if err != nil {
		return fmt.Errorf("inspect: list spool: %w", err)
	}
	if len(files) == 0 {
		_, _ = fmt.Fprintln(stdout,
			"No spooled telemetry events. The sink is flushing successfully "+
				"or telemetry is disabled.")
		return nil
	}

	events, err := inspectReadEventsNewestFirst(ctx, files, n, stderr)
	if err != nil {
		return fmt.Errorf("inspect: read events: %w", err)
	}
	if len(events) == 0 {
		_, _ = fmt.Fprintln(stdout,
			"Spool directory exists but contains no readable events.")
		return nil
	}

	for _, e := range events {
		if _, err := stdout.Write(e); err != nil {
			return err
		}
		if _, err := stdout.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return nil
}

// inspectSpoolDirPath resolves <XDG_STATE_HOME>/kit/telemetry/spool.
// Mirrors the runtime-side defaultSpoolDir in sink_https.go: we ask
// xdg for a sentinel file inside the spool dir and strip the
// filename. xdg.StateFile creates parent directories on demand, so a
// fresh adopter who has never spooled an event still ends up with a
// real (empty) directory we can ReadDir.
func inspectSpoolDirPath() (string, error) {
	p, err := xdg.StateFile(inspectXDGTool, inspectSpoolSubPath+"/.keep")
	if err != nil {
		return "", err
	}
	return filepath.Dir(p), nil
}

// inspectListSpoolFiles returns the spool's .jsonl files sorted
// newest-filename-first. Filenames are ISO-8601 YYYY-MM-DD, so a
// reverse lexicographic sort is equivalent to a reverse date sort.
// A missing spool dir returns (nil, nil): the caller treats this as
// "no events" rather than an error.
func inspectListSpoolFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

// inspectReadEventsNewestFirst walks files (already newest-first)
// and returns up to n event payloads, newest first within each file.
// Each returned []byte is a single JSON line that has been validated
// as well-formed JSON; malformed lines are skipped with a one-line
// warning on stderr so the audit of the remaining events proceeds.
//
// Reads the entire file into memory before reversing. Spool files
// are capped at MaxSpoolBytes (default 16 MiB) by the sink, so the
// memory footprint is bounded and the simpler implementation wins
// over a tail-read.
func inspectReadEventsNewestFirst(
	ctx context.Context,
	files []string,
	n int,
	stderr io.Writer,
) ([][]byte, error) {
	out := make([][]byte, 0, n)
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if len(out) >= n {
			break
		}

		lines, err := inspectReadFileLines(path)
		if err != nil {
			// One unreadable file should not abort the entire audit.
			// Warn on stderr; continue with the next file.
			_, _ = fmt.Fprintf(stderr,
				"inspect: skipping %s: %v\n", filepath.Base(path), err)
			continue
		}

		// Walk lines newest-first (last line is most recently appended).
		for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
			line := bytes.TrimSpace(lines[i])
			if len(line) == 0 {
				continue
			}
			// Validate it is well-formed JSON. A spool file with a
			// truncated tail (process crashed mid-write) would otherwise
			// emit garbage into stdout that breaks downstream parsers.
			var probe any
			if err := json.Unmarshal(line, &probe); err != nil {
				_, _ = fmt.Fprintf(stderr,
					"inspect: skipping malformed line in %s: %v\n",
					filepath.Base(path), err)
				continue
			}
			// Copy the line so a subsequent file read cannot clobber it.
			cp := make([]byte, len(line))
			copy(cp, line)
			out = append(out, cp)
		}
	}
	return out, nil
}

// inspectReadFileLines reads a spool file into a slice of lines. A
// bufio.Scanner with a generous buffer is used because individual
// events can be large (a single redacted batch line can carry a
// long argv slice). MaxScanTokenSize bumped to 1 MiB matches the
// per-event payload cap implied by MaxSpoolBytes / batchSize.
func inspectReadFileLines(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	const maxLine = 1 << 20 // 1 MiB
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)

	var lines [][]byte
	for sc.Scan() {
		// sc.Bytes() is reused on each Scan; we must copy.
		b := sc.Bytes()
		cp := make([]byte, len(b))
		copy(cp, b)
		lines = append(lines, cp)
	}
	if err := sc.Err(); err != nil {
		return lines, err
	}
	return lines, nil
}
