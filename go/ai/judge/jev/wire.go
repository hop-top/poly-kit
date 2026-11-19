package jev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/llm"
)

// request is the System One request body.
type request struct {
	Model     string              `json:"model"`
	State     map[string]any      `json:"state"`
	Questions map[string]question `json:"questions"`
}

// question is one System One question. Criteria is a choice's
// {value: description} object or a score's ordered array; a noul sends
// none.
type question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions,omitempty"`
	Criteria     any    `json:"criteria,omitempty"`
}

func buildRequest(model string, c judge.Case) request {
	state := c.State
	if state == nil {
		state = map[string]any{}
	}
	qs := make(map[string]question, len(c.Questions))
	for _, q := range c.Questions {
		wq := question{Instructions: q.Instructions}
		switch q.Kind {
		case judge.KindChoice:
			wq.Type = "choice"
			crit := make(map[string]string, len(q.Options))
			for _, o := range q.Options {
				d := o.Description
				if d == "" {
					d = o.Value // a bare name is interpreted by itself
				}
				crit[o.Value] = d
			}
			wq.Criteria = crit
		case judge.KindScore:
			wq.Type = "score"
			wq.Criteria = q.Anchors
		case judge.KindYesNo:
			wq.Type = "noul" // probability of yes; no abstain
		}
		qs[q.Name] = wq
	}
	return request{Model: model, State: state, Questions: qs}
}

// response is the System One response body (OpenRouter adds usage.cost).
type response struct {
	Model   string                `json:"model"`
	Answers map[string]wireAnswer `json:"answers"`
	Usage   struct {
		InputTokens  int      `json:"input_tokens"`
		OutputTokens int      `json:"output_tokens"`
		Cost         *float64 `json:"cost"`
	} `json:"usage"`
}

// wireAnswer is the union of the choice, score and noul answers.
type wireAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Score         *float64           `json:"score"`
	Noul          *float64           `json:"noul"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

func malformed(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformed, fmt.Sprintf(format, a...))
}

// parseResponse turns a 200 body into a validated Verdict for c.
func parseResponse(c judge.Case, model string, body []byte) (judge.Verdict, error) {
	var r response
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&r); err != nil {
		return judge.Verdict{}, malformed("decode: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return judge.Verdict{}, malformed("trailing data after the JSON document")
	}
	if r.Answers == nil {
		return judge.Verdict{}, malformed("no answers")
	}
	if r.Model != "" {
		model = r.Model
	}
	usage := llm.Usage{
		PromptTokens:     r.Usage.InputTokens,
		CompletionTokens: r.Usage.OutputTokens,
		TotalTokens:      r.Usage.InputTokens + r.Usage.OutputTokens,
	}
	v := judge.Verdict{
		Answers: make(map[string]judge.Answer, len(c.Questions)),
		Usage:   usage,
		Cost:    judge.Cost{Tokens: int64(usage.TotalTokens)},
		Backend: Backend,
		Model:   model,
	}
	if cost := r.Usage.Cost; cost != nil {
		if *cost < 0 || math.IsNaN(*cost) || math.IsInf(*cost, 0) {
			return judge.Verdict{}, malformed("usage.cost %v", *cost)
		}
		v.Cost.USD = *cost
	}

	names := make(map[string]bool, len(c.Questions))
	for _, q := range c.Questions {
		names[q.Name] = true
		wa, ok := r.Answers[q.Name]
		if !ok {
			return judge.Verdict{}, malformed("no answer for %q", q.Name)
		}
		a, err := buildAnswer(q, wa)
		if err != nil {
			return judge.Verdict{}, err
		}
		if err := judge.ValidateAnswer(q, a); err != nil {
			return judge.Verdict{}, fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		v.Answers[q.Name] = a
	}
	for n := range r.Answers {
		if !names[n] {
			return judge.Verdict{}, malformed("answer %q has no question", n)
		}
	}
	return v, nil
}

func buildAnswer(q judge.Question, w wireAnswer) (judge.Answer, error) {
	want := map[judge.Kind]string{
		judge.KindChoice: "choice", judge.KindScore: "score", judge.KindYesNo: "noul",
	}[q.Kind]
	if w.Type != want {
		return judge.Answer{}, malformed("answer %q: type %q, want %q", q.Name, w.Type, want)
	}
	switch q.Kind {
	case judge.KindYesNo:
		return yesNoAnswer(q, w)
	default:
		return distAnswer(q, w)
	}
}

// yesNoAnswer: noul is P(yes); P(no) is the rest. Value is the likelier
// side, yes on a tie.
func yesNoAnswer(q judge.Question, w wireAnswer) (judge.Answer, error) {
	if w.Noul == nil || !unit(*w.Noul) {
		return judge.Answer{}, malformed("answer %q: noul missing or outside [0,1]", q.Name)
	}
	probs := map[string]float64{judge.LabelYes: *w.Noul, judge.LabelNo: 1 - *w.Noul}
	val := judge.Argmax(probs, []string{judge.LabelYes, judge.LabelNo})
	return judge.Answer{Value: val, Probs: probs, Confidence: probs[val], Source: SourceNative}, nil
}

// distAnswer reads a choice or score answer. With probabilities they are
// kept (renormalised: the service rounds) and Value is their argmax,
// Confidence its probability. Without, the reported choice or score
// stands and so does the reported confidence.
func distAnswer(q judge.Question, w wireAnswer) (judge.Answer, error) {
	labels := judge.Labels(q)
	valid := make(map[string]bool, len(labels))
	for _, l := range labels {
		valid[l] = true
	}
	a := judge.Answer{Source: SourceNative}

	if len(w.Probabilities) > 0 {
		var sum float64
		for k, p := range w.Probabilities {
			if !valid[k] {
				return a, malformed("answer %q: probability for unknown label %q", q.Name, k)
			}
			if p < 0 || p > 1+judge.ProbEpsilon || math.IsNaN(p) {
				return a, malformed("answer %q: probability %q=%v", q.Name, k, p)
			}
			sum += p
		}
		if sum <= 0 {
			return a, malformed("answer %q: probabilities carry no mass", q.Name)
		}
		a.Probs = make(map[string]float64, len(w.Probabilities))
		for k, p := range w.Probabilities {
			a.Probs[k] = p / sum
		}
		a.Value = judge.Argmax(a.Probs, labels)
		a.Confidence = a.Probs[a.Value]
		if q.Kind == judge.KindScore {
			a.Score = judge.ScoreEV(a.Probs, len(q.Anchors))
		}
		return a, nil
	}

	conf := 0.0
	if w.Confidence != nil {
		if !unit(*w.Confidence) {
			return a, malformed("answer %q: confidence %v outside [0,1]", q.Name, *w.Confidence)
		}
		conf = *w.Confidence
	}
	a.Confidence = conf
	switch q.Kind {
	case judge.KindChoice:
		if !valid[w.Choice] {
			return a, malformed("answer %q: choice %q is not an option", q.Name, w.Choice)
		}
		a.Value = w.Choice
	case judge.KindScore:
		if w.Score == nil || math.IsNaN(*w.Score) || *w.Score < 0 || *w.Score > float64(len(q.Anchors)-1) {
			return a, malformed("answer %q: score missing or outside the anchors", q.Name)
		}
		a.Value = strconv.Itoa(int(math.Round(*w.Score)))
		a.Score = judge.ScoreFromIndex(*w.Score, len(q.Anchors))
	}
	return a, nil
}

func unit(x float64) bool { return x >= 0 && x <= 1 && !math.IsNaN(x) }
