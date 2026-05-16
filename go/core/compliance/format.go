package compliance

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FormatReport renders the report in the given format.
// Supported: "text" (default), "json".
func FormatReport(r *Report, format string) string {
	switch strings.ToLower(format) {
	case "json":
		return formatJSON(r)
	default:
		return formatText(r)
	}
}

func formatText(r *Report) string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString("  12-Factor AI CLI Compliance Report\n")
	b.WriteString("  ══════════════════════════════════\n")
	if r.Binary != "" {
		fmt.Fprintf(&b, "  Binary   : %s\n", r.Binary)
	}
	if r.Toolspec != "" {
		fmt.Fprintf(&b, "  Toolspec : %s\n", r.Toolspec)
	}
	b.WriteString("\n")

	for _, cr := range r.Results {
		icon := statusIcon(cr.Status)
		fmt.Fprintf(&b, "  %s  F%-2d %-20s %s\n",
			icon, int(cr.Factor), cr.Name, cr.Details)
		if cr.Suggestion != "" {
			fmt.Fprintf(&b, "       └─ %s\n", cr.Suggestion)
		}
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "  Score: %d/%d factors passing\n", r.Score, r.Total)
	b.WriteString("\n")

	return b.String()
}

func statusIcon(s string) string {
	switch s {
	case "pass":
		return "PASS"
	case "fail":
		return "FAIL"
	case "warn":
		return "WARN"
	case "skip":
		return "SKIP"
	default:
		return "????"
	}
}

func formatJSON(r *Report) string {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	return string(data) + "\n"
}
