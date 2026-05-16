package uri

import (
	"fmt"
	"io"
	"strings"

	"hop.top/kit/go/console/output"
)

func render(w io.Writer, format string, value any) error {
	if format == "" {
		format = formatTable
	}
	if err := validateFormat(format, formatText, formatJSON, formatYAML, formatTable); err != nil {
		return err
	}
	if format == formatText {
		return renderText(w, value)
	}
	return output.Render(w, format, value)
}

func renderCompletion(w io.Writer, format string, rows any) error {
	if format == "" {
		format = formatLines
	}
	if err := validateFormat(format, formatLines, formatJSON, formatYAML, formatTable); err != nil {
		return err
	}
	if format == formatLines {
		switch v := rows.(type) {
		case []completionRow:
			for _, row := range v {
				if _, err := fmt.Fprintln(w, row.Value); err != nil {
					return err
				}
			}
		case []vanityRow:
			for _, row := range v {
				if _, err := fmt.Fprintf(w, "%s\tcanonical: %s\n", row.From, row.To); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return output.Render(w, format, rows)
}

func renderText(w io.Writer, value any) error {
	switch v := value.(type) {
	case uriRow:
		_, err := fmt.Fprintf(w, "scheme=%s namespace=%s id=%s action=%s\n", v.Scheme, v.Namespace, v.ID, v.Action)
		return err
	case actionRow:
		_, err := fmt.Fprintf(w, "%s %s\n", v.Command, strings.Join(v.Args, " "))
		return err
	case handlerIDRow:
		_, err := fmt.Fprintln(w, v.HandlerID)
		return err
	case handlerGenerateRow:
		_, err := fmt.Fprint(w, v.Snippet)
		return err
	default:
		return output.Render(w, output.Table, value)
	}
}

func validateFormat(format string, allowed ...string) error {
	for _, candidate := range allowed {
		if format == candidate {
			return nil
		}
	}
	return fmt.Errorf("unknown --format %q (want %s)", format, strings.Join(allowed, "|"))
}
