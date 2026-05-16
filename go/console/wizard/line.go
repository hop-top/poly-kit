package wizard

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
)

const pageSize = 20

// RunLine drives a wizard through a line-oriented stdio interface.
func RunLine(ctx context.Context, w *Wizard, in io.Reader, out io.Writer) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	sc := bufio.NewScanner(in)
	var lastGroup string

	for !w.Done() {
		if err := ctx.Err(); err != nil {
			return &AbortError{}
		}
		step := w.Current()
		if step == nil {
			break
		}

		if step.Group != "" && step.Group != lastGroup {
			fmt.Fprintf(out, "\n── %s ──\n", step.Group)
		}
		lastGroup = step.Group

		switch step.Kind {
		case KindSummary:
			renderSummary(out, step, w.Results())
			if _, err := w.Advance(nil); err != nil {
				return err
			}
			continue
		case KindAction:
			if err := doAction(ctx, out, step, w); err != nil {
				return err
			}
			continue
		}

		val, back, err := promptStep(ctx, sc, out, step)
		if err != nil {
			return err
		}
		if back {
			w.Back()
			continue
		}
		if _, err := w.Advance(val); err != nil {
			if ve, ok := err.(*ValidationError); ok {
				fmt.Fprintf(out, "Error: %v\n", ve.Err)
				continue
			}
			return err
		}
	}
	return nil
}

func promptStep(ctx context.Context, sc *bufio.Scanner, out io.Writer, s *Step) (any, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, &AbortError{}
	}
	switch s.Kind {
	case KindTextInput:
		return promptText(sc, out, s)
	case KindSelect:
		return promptSelect(sc, out, s)
	case KindConfirm:
		return promptConfirm(sc, out, s)
	case KindMultiSelect:
		return promptMultiSelect(sc, out, s)
	default:
		return nil, false, fmt.Errorf("unsupported step kind: %s", s.Kind)
	}
}
func promptText(sc *bufio.Scanner, out io.Writer, s *Step) (any, bool, error) {
	for {
		p := s.Label
		if s.DefaultValue != nil {
			p += fmt.Sprintf(" [%v]", s.DefaultValue)
		}
		fmt.Fprintf(out, "%s: ", p)
		if !sc.Scan() {
			return nil, false, &AbortError{}
		}
		line := strings.TrimSpace(sc.Text())
		if isBack(line) {
			return nil, true, nil
		}
		if line == "" {
			if s.DefaultValue != nil {
				return s.DefaultValue, false, nil
			}
			if s.Required {
				fmt.Fprintln(out, "Error: value is required")
				continue
			}
			return "", false, nil
		}
		return line, false, nil
	}
}
func promptSelect(sc *bufio.Scanner, out io.Writer, s *Step) (any, bool, error) {
	page, pages := 0, optPages(len(s.Options))
	for {
		fmt.Fprintf(out, "%s:\n", s.Label)
		renderOptions(out, s.Options, page, pages)
		def := fmtDefault(s.DefaultValue)
		fmt.Fprintf(out, "Choice%s: ", def)
		if !sc.Scan() {
			return nil, false, &AbortError{}
		}
		line := strings.TrimSpace(sc.Text())
		if isBack(line) {
			return nil, true, nil
		}
		if paginate(line, &page, pages) {
			continue
		}
		if line == "" {
			if s.DefaultValue != nil {
				return s.DefaultValue, false, nil
			}
			if s.Required {
				fmt.Fprintln(out, "Error: selection is required")
				continue
			}
			return "", false, nil
		}
		n, err := strconv.Atoi(line)
		if err != nil || n < 1 || n > len(s.Options) {
			fmt.Fprintf(out, "Error: choose 1-%d\n", len(s.Options))
			continue
		}
		return s.Options[n-1].Value, false, nil
	}
}
func promptConfirm(sc *bufio.Scanner, out io.Writer, s *Step) (any, bool, error) {
	for {
		hint := "[y/n]"
		if b, ok := s.DefaultValue.(bool); ok {
			if b {
				hint = "[Y/n]"
			} else {
				hint = "[y/N]"
			}
		}
		fmt.Fprintf(out, "%s %s: ", s.Label, hint)
		if !sc.Scan() {
			return nil, false, &AbortError{}
		}
		line := strings.TrimSpace(sc.Text())
		if isBack(line) {
			return nil, true, nil
		}
		switch strings.ToLower(line) {
		case "y", "yes":
			return true, false, nil
		case "n", "no":
			return false, false, nil
		case "":
			if s.DefaultValue != nil {
				return s.DefaultValue, false, nil
			}
			fmt.Fprintln(out, "Error: please enter y or n")
		default:
			fmt.Fprintln(out, "Error: please enter y or n")
		}
	}
}
func promptMultiSelect(sc *bufio.Scanner, out io.Writer, s *Step) (any, bool, error) {
	page, pages := 0, optPages(len(s.Options))
	for {
		fmt.Fprintf(out, "%s (comma-separated numbers):\n", s.Label)
		renderOptions(out, s.Options, page, pages)
		fmt.Fprintf(out, "Choices%s: ", fmtDefault(s.DefaultValue))
		if !sc.Scan() {
			return nil, false, &AbortError{}
		}
		line := strings.TrimSpace(sc.Text())
		if isBack(line) {
			return nil, true, nil
		}
		if paginate(line, &page, pages) {
			continue
		}
		if line == "" {
			if s.DefaultValue != nil {
				return s.DefaultValue, false, nil
			}
			if s.Required {
				fmt.Fprintln(out, "Error: selection is required")
				continue
			}
			return []string{}, false, nil
		}
		vals, err := parseChoices(line, s.Options)
		if err != nil {
			fmt.Fprintf(out, "Error: choose 1-%d\n", len(s.Options))
			continue
		}
		return vals, false, nil
	}
}

func parseChoices(line string, opts []Option) ([]string, error) {
	parts := strings.Split(line, ",")
	vals := make([]string, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 1 || n > len(opts) {
			return nil, fmt.Errorf("out of range")
		}
		vals = append(vals, opts[n-1].Value)
	}
	return vals, nil
}
func doAction(ctx context.Context, out io.Writer, s *Step, w *Wizard) error {
	result, err := w.Advance(nil)
	if err != nil {
		return err
	}
	ar, ok := result.(*ActionRequest)
	if !ok || ar == nil {
		return nil
	}
	fmt.Fprintf(out, "Running %s...\n", s.Label)
	var runErr error
	if ar.Run != nil {
		runErr = ar.Run(ctx, w.Results())
	}
	printResult(out, s.Label, runErr)
	return w.ResolveAction(runErr)
}

func printResult(out io.Writer, label string, err error) {
	if err == nil {
		fmt.Fprintf(out, "✓ %s\n", label)
	} else {
		fmt.Fprintf(out, "✗ %s: %v\n", label, err)
	}
}
func renderSummary(out io.Writer, s *Step, results map[string]any) {
	fmt.Fprintf(out, "\n── %s ──\n", s.Label)
	if s.FormatFn != nil {
		fmt.Fprintln(out, s.FormatFn(results))
		return
	}
	keys := sortedVisibleKeys(results)
	maxLen := 0
	for _, k := range keys {
		if len(k) > maxLen {
			maxLen = len(k)
		}
	}
	for _, k := range keys {
		fmt.Fprintf(out, "  %-*s  %v\n", maxLen+1, k+":", results[k])
	}
}

func sortedVisibleKeys(results map[string]any) []string {
	keys := make([]string, 0, len(results))
	for k := range results {
		if !strings.HasPrefix(k, "__") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}
func isBack(s string) bool {
	l := strings.ToLower(s)
	return l == "b" || l == "back"
}

func optPages(n int) int { return (n + pageSize - 1) / pageSize }

func renderOptions(out io.Writer, opts []Option, page, pages int) {
	start := page * pageSize
	end := start + pageSize
	if end > len(opts) {
		end = len(opts)
	}
	for i := start; i < end; i++ {
		fmt.Fprintf(out, "  %d) %s\n", i+1, opts[i].Label)
	}
	if pages > 1 {
		fmt.Fprintln(out, "  [n]ext page, [p]rev page")
	}
}

func fmtDefault(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf(" [%v]", v)
}

func paginate(line string, page *int, pages int) bool {
	switch strings.ToLower(line) {
	case "n", "next":
		if *page < pages-1 {
			*page++
		}
		return true
	case "p", "prev":
		if *page > 0 {
			*page--
		}
		return true
	}
	return false
}
