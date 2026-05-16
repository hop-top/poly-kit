package pkl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pklgo "github.com/apple/pkl-go/pkl"
)

// Resolve evaluates a PKL module with user-provided answers to
// resolve computed fields. Returns the complete config map including
// both user-provided and computed values.
//
// Requires the pkl binary (https://pkl-lang.org/main/current/pkl-cli/index.html):
//
//	brew install pkl    # macOS
//	# or download from https://github.com/apple/pkl/releases
//
// The PKL module must include an output block with JsonRenderer:
//
//	output { renderer = new JsonRenderer {} }
//
// All other config/pkl functions (LoadSchema, ValidateValue,
// CompletionKeys, WizardSteps) work without the pkl binary —
// they parse PKL source text directly.
func Resolve(
	ctx context.Context,
	pklPath string,
	answers map[string]any,
) (map[string]any, error) {
	absPath, err := filepath.Abs(pklPath)
	if err != nil {
		return nil, fmt.Errorf("pkl: resolve path: %w", err)
	}

	amendment, err := generateAmendment(absPath, answers)
	if err != nil {
		return nil, fmt.Errorf("pkl: generate amendment: %w", err)
	}

	tmpDir := filepath.Dir(absPath)
	tmp, err := os.CreateTemp(tmpDir, "pkl-amend-*.pkl")
	if err != nil {
		return nil, fmt.Errorf("pkl: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.WriteString(amendment); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("pkl: write amendment: %w", err)
	}
	tmp.Close()

	ev, err := pklgo.NewEvaluator(ctx, pklgo.PreconfiguredOptions)
	if err != nil {
		return nil, fmt.Errorf("pkl: create evaluator: %w", err)
	}
	defer ev.Close() //nolint:errcheck // best-effort cleanup

	src := pklgo.FileSource(tmpPath)
	text, err := ev.EvaluateOutputText(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("pkl: evaluate: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("pkl: unmarshal: %w", err)
	}
	return result, nil
}

// generateAmendment builds a PKL amendment source that overrides
// answered fields on top of the original module.
func generateAmendment(
	pklPath string,
	answers map[string]any,
) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "amends \"%s\"\n\n", pklPath)

	for key, val := range answers {
		lit := toPklLiteral(val)
		if lit == "" {
			return "", fmt.Errorf("unsupported value type for %q", key)
		}
		fmt.Fprintf(&b, "%s = %s\n", key, lit)
	}
	return b.String(), nil
}

// toPklLiteral converts a Go value to its PKL source representation.
func toPklLiteral(v any) string {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", val)
	case int64:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%g", val)
	case []string:
		if len(val) == 0 {
			return "new Listing {}"
		}
		var items []string
		for _, s := range val {
			items = append(items, fmt.Sprintf("  %q", s))
		}
		return "new Listing {\n" +
			strings.Join(items, "\n") + "\n}"
	case []any:
		if len(val) == 0 {
			return "new Listing {}"
		}
		var items []string
		for _, elem := range val {
			lit := toPklLiteral(elem)
			if lit == "" {
				return ""
			}
			items = append(items, "  "+lit)
		}
		return "new Listing {\n" +
			strings.Join(items, "\n") + "\n}"
	default:
		return ""
	}
}
