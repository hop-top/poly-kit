package wizard

import "context"

// StepKind identifies the type of wizard step.
type StepKind string

const (
	KindTextInput   StepKind = "text_input"
	KindSelect      StepKind = "select"
	KindConfirm     StepKind = "confirm"
	KindMultiSelect StepKind = "multi_select"
	KindAction      StepKind = "action"
	KindSummary     StepKind = "summary"
)

// ErrorAction controls behavior when an action step fails.
type ErrorAction int

const (
	ActionAbort ErrorAction = iota
	ActionRetry
	ActionSkip
)

// Option represents a selectable choice for Select/MultiSelect steps.
type Option struct {
	Value       string
	Label       string
	Description string
}

// Condition gates step visibility based on prior results.
type Condition struct {
	Key  string
	Pred func(any) bool
}

// Step is the core data type for a single wizard step.
type Step struct {
	Key             string
	Kind            StepKind
	Label           string
	Description     string
	Group           string
	Required        bool
	DefaultValue    any
	Options         []Option
	ValidateText    func(string) error
	ValidateChoice  func(string) error
	ValidateChoices func([]string) error
	When            *Condition
	ActionFn        func(ctx context.Context, results map[string]any) error
	OnError         ErrorAction
	FormatFn        func(results map[string]any) string
}

// --- Builders ---

// TextInput creates a text input step.
func TextInput(key, label string) Step {
	return Step{Key: key, Kind: KindTextInput, Label: label}
}

// Select creates a single-choice selection step.
func Select(key, label string, options []Option) Step {
	return Step{Key: key, Kind: KindSelect, Label: label, Options: options}
}

// Confirm creates a boolean confirmation step.
func Confirm(key, label string) Step {
	return Step{Key: key, Kind: KindConfirm, Label: label}
}

// MultiSelect creates a multi-choice selection step.
func MultiSelect(key, label string, options []Option) Step {
	return Step{
		Key: key, Kind: KindMultiSelect, Label: label, Options: options,
	}
}

// Action creates a step that runs an arbitrary function.
func Action(
	key, label string,
	fn func(context.Context, map[string]any) error,
) Step {
	return Step{Key: key, Kind: KindAction, Label: label, ActionFn: fn}
}

// Summary creates a read-only summary step. Key is auto-generated if empty.
func Summary(label string) Step {
	return Step{Kind: KindSummary, Label: label}
}

// --- Chainable modifiers ---

func (s Step) WithRequired() Step            { s.Required = true; return s }
func (s Step) WithDefault(v any) Step        { s.DefaultValue = v; return s }
func (s Step) WithGroup(g string) Step       { s.Group = g; return s }
func (s Step) WithDescription(d string) Step { s.Description = d; return s }

func (s Step) WithValidateText(fn func(string) error) Step {
	s.ValidateText = fn
	return s
}

func (s Step) WithValidateChoice(fn func(string) error) Step {
	s.ValidateChoice = fn
	return s
}

func (s Step) WithValidateChoices(fn func([]string) error) Step {
	s.ValidateChoices = fn
	return s
}

func (s Step) WithWhen(key string, pred func(any) bool) Step {
	s.When = &Condition{Key: key, Pred: pred}
	return s
}

func (s Step) WithOnError(a ErrorAction) Step {
	s.OnError = a
	return s
}

func (s Step) WithFormat(fn func(map[string]any) string) Step {
	s.FormatFn = fn
	return s
}
