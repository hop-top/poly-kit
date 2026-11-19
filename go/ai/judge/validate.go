package judge

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// ErrInvalid wraps every Validate* failure; use errors.Is.
var ErrInvalid = errors.New("judge: invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Validate checks a Case is well-formed: at least one question; unique
// non-empty names; known kinds; kind-specific shape (choice: >= 2 unique
// non-reserved options, no anchors; score: >= 2 non-empty anchors, no
// options; yesno: neither). All problems are joined.
func Validate(c Case) error {
	if len(c.Questions) == 0 {
		return invalid("case has no questions")
	}
	var errs []error
	seen := map[string]bool{}
	for i, q := range c.Questions {
		if q.Name == "" {
			errs = append(errs, invalid("question %d: empty name", i))
		} else if seen[q.Name] {
			errs = append(errs, invalid("question %q: duplicate name", q.Name))
		}
		seen[q.Name] = true
		errs = append(errs, validateQuestion(q)...)
	}
	return errors.Join(errs...)
}

func validateQuestion(q Question) []error {
	var errs []error
	bad := func(format string, a ...any) {
		errs = append(errs, invalid("question %q: "+format, append([]any{q.Name}, a...)...))
	}
	switch q.Kind {
	case KindChoice:
		if len(q.Options) < 2 {
			bad("choice needs >= 2 options, got %d", len(q.Options))
		}
		if len(q.Anchors) > 0 {
			bad("choice takes no anchors")
		}
		seen := map[string]bool{}
		for _, o := range q.Options {
			switch {
			case strings.TrimSpace(o.Value) == "":
				bad("empty option value")
			case o.Value == LabelAbstain:
				bad("option value %q is reserved; set Abstain instead", o.Value)
			case seen[o.Value]:
				bad("duplicate option %q", o.Value)
			}
			seen[o.Value] = true
		}
	case KindScore:
		if len(q.Anchors) < 2 {
			bad("score needs >= 2 anchors, got %d", len(q.Anchors))
		}
		if len(q.Options) > 0 {
			bad("score takes no options")
		}
		for i, a := range q.Anchors {
			if strings.TrimSpace(a) == "" {
				bad("empty anchor %d", i)
			}
		}
	case KindYesNo:
		if len(q.Options) > 0 || len(q.Anchors) > 0 {
			bad("yesno takes no options or anchors")
		}
	default:
		bad("unknown kind %q", q.Kind)
	}
	return errs
}

// ValidateAnswer checks a against q: Value is a valid label; Abstained iff
// Value is "abstain", which needs q.Abstain; Probs keys are valid labels,
// each in [0,1], summing to 1 within ProbEpsilon; Confidence in [0,1] and
// equal to Probs[Value] when Probs is set; Score in [0,1], and for
// KindScore with Probs equal to ScoreEV (0 when abstained).
func ValidateAnswer(q Question, a Answer) error {
	if err := errors.Join(validateQuestion(q)...); err != nil {
		return err
	}
	labels := map[string]bool{}
	for _, l := range Labels(q) {
		labels[l] = true
	}
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, invalid("answer %q: "+format, append([]any{q.Name}, args...)...))
	}

	if a.Value == LabelAbstain && !q.Abstain {
		bad("abstain not allowed")
	} else if !labels[a.Value] {
		bad("value %q not a valid label", a.Value)
	}
	if a.Abstained != (a.Value == LabelAbstain) {
		bad("Abstained=%v but value %q", a.Abstained, a.Value)
	}
	if !unit(a.Confidence) {
		bad("confidence %v outside [0,1]", a.Confidence)
	}
	if !unit(a.Score) {
		bad("score %v outside [0,1]", a.Score)
	}
	if len(a.Probs) > 0 {
		var sum float64
		for k, p := range a.Probs {
			if !labels[k] {
				bad("probs key %q not a valid label", k)
			}
			if !unit(p) {
				bad("prob %q=%v outside [0,1]", k, p)
			}
			sum += p
		}
		if math.Abs(sum-1) > ProbEpsilon {
			bad("probs sum %v, want 1 +/- %g", sum, ProbEpsilon)
		}
		if math.Abs(a.Confidence-a.Probs[a.Value]) > ProbEpsilon {
			bad("confidence %v != P(%q)=%v", a.Confidence, a.Value, a.Probs[a.Value])
		}
		if q.Kind == KindScore {
			want := 0.0
			if !a.Abstained {
				want = ScoreEV(a.Probs, len(q.Anchors))
			}
			if math.Abs(a.Score-want) > ProbEpsilon {
				bad("score %v != expected value %v", a.Score, want)
			}
		}
	}
	return errors.Join(errs...)
}

// ValidateVerdict checks v answers exactly c's questions, each valid.
func ValidateVerdict(c Case, v Verdict) error {
	var errs []error
	names := map[string]bool{}
	for _, q := range c.Questions {
		names[q.Name] = true
		a, ok := v.Answers[q.Name]
		if !ok {
			errs = append(errs, invalid("answer %q: missing", q.Name))
			continue
		}
		if err := ValidateAnswer(q, a); err != nil {
			errs = append(errs, err)
		}
	}
	for n := range v.Answers {
		if !names[n] {
			errs = append(errs, invalid("answer %q: no such question", n))
		}
	}
	return errors.Join(errs...)
}

func unit(x float64) bool { return x >= 0 && x <= 1 && !math.IsNaN(x) }
