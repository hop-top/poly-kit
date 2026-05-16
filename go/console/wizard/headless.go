package wizard

import (
	"context"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// RunHeadless drives a wizard non-interactively using pre-supplied answers.
// Missing keys fall back to DefaultValue, then zero values. Required steps
// with no answer and no default produce a ValidationError.
func RunHeadless(ctx context.Context, w *Wizard,
	answers map[string]any,
) (map[string]any, error) {
	for !w.Done() {
		if err := ctx.Err(); err != nil {
			return nil, &AbortError{}
		}

		step := w.Current()
		if step == nil {
			break
		}

		value, err := resolveValue(step, answers)
		if err != nil {
			return nil, err
		}

		result, advErr := w.Advance(value)
		if advErr != nil {
			return nil, advErr
		}

		if ar, ok := result.(*ActionRequest); ok && ar != nil {
			runErr := ar.Run(ctx, w.Results())
			if resolveErr := w.ResolveAction(runErr); resolveErr != nil {
				return nil, resolveErr
			}
		}
	}

	if !w.DryRun() {
		if err := w.Complete(); err != nil {
			return nil, err
		}
	}

	return w.Results(), nil
}

// resolveValue picks the value for a step from answers, defaults, or
// kind-appropriate zero values.
func resolveValue(step *Step, answers map[string]any) (any, error) {
	if v, ok := answers[step.Key]; ok {
		return v, nil
	}

	if step.DefaultValue != nil {
		return step.DefaultValue, nil
	}

	if step.Required {
		return nil, &ValidationError{
			StepKey: step.Key,
			Err:     errors.New("required"),
		}
	}

	return zeroForKind(step.Kind), nil
}

// zeroForKind returns the zero value appropriate for a step kind.
func zeroForKind(k StepKind) any {
	switch k {
	case KindConfirm:
		return false
	case KindMultiSelect:
		return []string{}
	default: // text_input, select, action, summary
		return ""
	}
}

// LoadAnswers reads a YAML file and returns its contents as a
// map suitable for RunHeadless.
func LoadAnswers(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load answers: %w", err)
	}

	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("load answers: %w", err)
	}
	return out, nil
}
