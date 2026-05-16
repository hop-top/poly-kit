package wizard

import (
	"context"
	"errors"
	"fmt"
)

// ActionRequest is returned by Advance for Action steps so the frontend
// can execute the action and report back via ResolveAction.
type ActionRequest struct {
	StepKey string
	Run     func(ctx context.Context, results map[string]any) error
}

// Wizard is a headless, sequential wizard engine.
type Wizard struct {
	steps      []Step
	current    int
	results    map[string]any
	done       bool
	dryRun     bool
	onComplete func(map[string]any) error
}

// New validates steps and returns a ready-to-use Wizard.
func New(steps ...Step) (*Wizard, error) {
	seen := make(map[string]struct{}, len(steps))
	for i := range steps {
		s := &steps[i]

		// Auto-generate key for Summary steps.
		if s.Kind == KindSummary && s.Key == "" {
			s.Key = fmt.Sprintf("__summary_%d", i)
		}

		if s.Key == "" {
			return nil, fmt.Errorf(
				"step at index %d: key must not be empty", i,
			)
		}

		if _, dup := seen[s.Key]; dup {
			return nil, fmt.Errorf("duplicate step key %q", s.Key)
		}
		seen[s.Key] = struct{}{}

		if err := validateDefault(s); err != nil {
			return nil, err
		}

		if s.Kind == KindAction && s.ActionFn == nil {
			return nil, fmt.Errorf(
				"step %q: action kind must have ActionFn", s.Key,
			)
		}

		if (s.Kind == KindSelect || s.Kind == KindMultiSelect) &&
			len(s.Options) == 0 {
			return nil, fmt.Errorf(
				"step %q: select/multi_select must have options", s.Key,
			)
		}
	}

	return &Wizard{
		steps:   steps,
		results: make(map[string]any),
	}, nil
}

// validateDefault checks that a step's DefaultValue matches the expected
// type for its Kind.
func validateDefault(s *Step) error {
	if s.DefaultValue == nil {
		return nil
	}
	switch s.Kind {
	case KindTextInput, KindSelect:
		if _, ok := s.DefaultValue.(string); !ok {
			return fmt.Errorf(
				"step %q: default must be string for %s", s.Key, s.Kind,
			)
		}
	case KindConfirm:
		if _, ok := s.DefaultValue.(bool); !ok {
			return fmt.Errorf(
				"step %q: default must be bool for confirm", s.Key,
			)
		}
	case KindMultiSelect:
		if _, ok := s.DefaultValue.([]string); !ok {
			return fmt.Errorf(
				"step %q: default must be []string for multi_select",
				s.Key,
			)
		}
	}
	return nil
}

// Current returns the current visible step, skipping over steps whose
// When condition evaluates to false.
func (w *Wizard) Current() *Step {
	for w.current < len(w.steps) {
		s := &w.steps[w.current]
		if s.When != nil && !s.When.Pred(w.results[s.When.Key]) {
			delete(w.results, s.Key) // clear stale result
			w.current++
			continue
		}
		return s
	}
	w.done = true
	return nil
}

// Advance submits a value for the current step.
//
// Returns (*ActionRequest, nil) for Action steps.
// Returns (nil, *ValidationError) on validation failure.
// Returns (nil, nil) on normal advance.
func (w *Wizard) Advance(value any) (any, error) {
	s := w.Current()
	if s == nil {
		return nil, nil
	}

	switch s.Kind {
	case KindAction:
		return &ActionRequest{StepKey: s.Key, Run: s.ActionFn}, nil

	case KindSummary:
		w.advance()
		return nil, nil

	case KindTextInput, KindSelect:
		str, ok := value.(string)
		if !ok {
			return nil, &ValidationError{
				StepKey: s.Key,
				Err:     errors.New("expected string"),
			}
		}
		if s.Required && str == "" {
			return nil, &ValidationError{
				StepKey: s.Key,
				Err:     errors.New("required"),
			}
		}
		if s.Kind == KindTextInput && s.ValidateText != nil {
			if err := s.ValidateText(str); err != nil {
				return nil, &ValidationError{StepKey: s.Key, Err: err}
			}
		}
		if s.Kind == KindSelect {
			if !isValidOption(s.Options, str) {
				return nil, &ValidationError{
					StepKey: s.Key,
					Err:     fmt.Errorf("invalid option %q", str),
				}
			}
			if s.ValidateChoice != nil {
				if err := s.ValidateChoice(str); err != nil {
					return nil, &ValidationError{
						StepKey: s.Key, Err: err,
					}
				}
			}
		}
		w.results[s.Key] = str

	case KindConfirm:
		b, ok := value.(bool)
		if !ok {
			return nil, &ValidationError{
				StepKey: s.Key,
				Err:     errors.New("expected bool"),
			}
		}
		w.results[s.Key] = b

	case KindMultiSelect:
		ss, ok := value.([]string)
		if !ok {
			return nil, &ValidationError{
				StepKey: s.Key,
				Err:     errors.New("expected []string"),
			}
		}
		if s.Required && len(ss) == 0 {
			return nil, &ValidationError{
				StepKey: s.Key,
				Err:     errors.New("required"),
			}
		}
		for _, v := range ss {
			if !isValidOption(s.Options, v) {
				return nil, &ValidationError{
					StepKey: s.Key,
					Err:     fmt.Errorf("invalid option %q", v),
				}
			}
		}
		if s.ValidateChoices != nil {
			if err := s.ValidateChoices(ss); err != nil {
				return nil, &ValidationError{StepKey: s.Key, Err: err}
			}
		}
		w.results[s.Key] = ss
	}

	w.advance()
	return nil, nil
}

// advance moves to the next step index.
func (w *Wizard) advance() {
	w.current++
	// Current() will skip When=false on next call.
	if w.Current() == nil {
		w.done = true
	}
}

// ResolveAction is called after the frontend executes an action step.
// Pass nil on success; pass the error on failure.
func (w *Wizard) ResolveAction(err error) error {
	s := w.Current()
	if s == nil {
		return nil
	}
	if err == nil {
		w.advance()
		return nil
	}

	switch s.OnError {
	case ActionAbort:
		return &ActionError{StepKey: s.Key, Err: err, Action: ActionAbort}
	case ActionRetry:
		// Stay on current step.
		return nil
	case ActionSkip:
		w.advance()
		return nil
	}
	return &ActionError{StepKey: s.Key, Err: err, Action: s.OnError}
}

// Back moves to the previous visible step and clears its result.
func (w *Wizard) Back() {
	w.done = false
	for w.current > 0 {
		w.current--
		s := &w.steps[w.current]
		if s.When != nil && !s.When.Pred(w.results[s.When.Key]) {
			continue
		}
		delete(w.results, s.Key)
		return
	}
}

// Results returns a copy of the collected results.
func (w *Wizard) Results() map[string]any {
	out := make(map[string]any, len(w.results))
	for k, v := range w.results {
		out[k] = v
	}
	return out
}

// Done reports whether the wizard has advanced past the last step.
func (w *Wizard) Done() bool { return w.done }

// StepCount returns the number of currently visible steps.
func (w *Wizard) StepCount() int {
	n := 0
	for i := range w.steps {
		s := &w.steps[i]
		if s.When != nil && !s.When.Pred(w.results[s.When.Key]) {
			continue
		}
		n++
	}
	return n
}

// StepIndex returns the 0-based index of the current step among visible
// steps.
func (w *Wizard) StepIndex() int {
	idx := 0
	for i := 0; i < w.current && i < len(w.steps); i++ {
		s := &w.steps[i]
		if s.When != nil && !s.When.Pred(w.results[s.When.Key]) {
			continue
		}
		idx++
	}
	return idx
}

// SetDryRun enables or disables dry-run mode.
func (w *Wizard) SetDryRun(v bool) { w.dryRun = v }

// DryRun reports whether the wizard is in dry-run mode.
func (w *Wizard) DryRun() bool { return w.dryRun }

// SetOnComplete registers a callback invoked by Complete.
func (w *Wizard) SetOnComplete(fn func(map[string]any) error) {
	w.onComplete = fn
}

// Complete runs the OnComplete callback unless in dry-run mode.
func (w *Wizard) Complete() error {
	if w.dryRun || w.onComplete == nil {
		return nil
	}
	return w.onComplete(w.results)
}

// isValidOption returns true when val matches one of the option values.
func isValidOption(opts []Option, val string) bool {
	for _, o := range opts {
		if o.Value == val {
			return true
		}
	}
	return false
}
