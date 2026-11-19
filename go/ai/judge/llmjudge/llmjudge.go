// Package llmjudge is a judge.Judge backed by a chat model. It asks for
// schema-constrained JSON holding verbalized probabilities per label,
// normalises them, and optionally takes N samples and aggregates their
// votes (self-consistency) into probabilities.
package llmjudge

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

const (
	// Backend is Verdict.Backend.
	Backend = "llm"
	// SourceVerbalized marks answers from one sample's stated probabilities.
	SourceVerbalized = "verbalized"
	// SourceVotes marks answers aggregated from several samples.
	SourceVotes = "votes"

	// defaultVoteTemperature spreads samples when votes > 1 and no
	// temperature was set; identical samples would add nothing.
	defaultVoteTemperature = 0.7
)

// Judge answers cases through an llm.Completer. Safe for concurrent use.
type Judge struct {
	c          llm.Completer
	model      string
	votes      int
	temp       *float64
	maxRepairs *int
}

// Option configures a Judge.
type Option func(*Judge)

// WithModel sets the model on every request; unset uses the client default.
func WithModel(m string) Option { return func(j *Judge) { j.model = m } }

// WithVotes samples each case n times and aggregates the samples' argmax
// labels into probabilities (share of votes). n < 1 means 1; 1 keeps the
// single sample's verbalized probabilities.
func WithVotes(n int) Option { return func(j *Judge) { j.votes = n } }

// WithTemperature sets the sampling temperature, 0 included. Unset: the
// provider default for one sample, 0.7 for votes > 1.
func WithTemperature(t float64) Option { return func(j *Judge) { j.temp = &t } }

// WithMaxRepairs bounds schema-repair retries per sample; unset keeps the
// llm package default.
func WithMaxRepairs(n int) Option { return func(j *Judge) { j.maxRepairs = &n } }

// New returns a Judge over c; *llm.Client satisfies llm.Completer.
func New(c llm.Completer, opts ...Option) *Judge {
	j := &Judge{c: c, votes: 1}
	for _, o := range opts {
		o(j)
	}
	if j.votes < 1 {
		j.votes = 1
	}
	return j
}

var _ judge.Judge = (*Judge)(nil)

// sample is one model reply: normalised probs and the stated reason.
type sample struct {
	probs map[string]float64
	why   string
}

// Judge answers every question of c, one model call per sample. Kinds
// other than choice, score and yes/no fail with an
// llmerrors.ErrCapabilityNotSupported; a malformed case fails with
// judge.ErrInvalid; output that never matches the schema fails with
// llm.ErrSchemaInvalid.
func (j *Judge) Judge(ctx context.Context, c judge.Case) (judge.Verdict, error) {
	for _, q := range c.Questions {
		switch q.Kind {
		case judge.KindChoice, judge.KindScore, judge.KindYesNo:
		default:
			return judge.Verdict{}, llmerrors.NewCapabilityNotSupported(
				fmt.Sprintf("question kind %q", q.Kind), "llmjudge")
		}
	}
	if err := judge.Validate(c); err != nil {
		return judge.Verdict{}, err
	}

	schema, err := buildSchema(c.Questions)
	if err != nil {
		return judge.Verdict{}, err
	}
	rf := llm.ResponseFormat{Name: "judgment", Schema: schema, Strict: true}
	req := llm.Request{Model: j.model, Messages: buildMessages(c), Temperature: j.temp}
	if j.temp == nil && j.votes > 1 {
		t := defaultVoteTemperature
		req.Temperature = &t
	}
	var sopts []llm.StructuredOption
	if j.maxRepairs != nil {
		sopts = append(sopts, llm.MaxRepairs(*j.maxRepairs))
	}

	samples := make([]map[string]sample, 0, j.votes)
	var usage llm.Usage
	for i := 0; i < j.votes; i++ {
		res, err := llm.CompleteStructured(ctx, j.c, req, rf, sopts...)
		usage = addUsage(usage, res.Usage)
		if err != nil {
			return judge.Verdict{}, err
		}
		s, err := parse(c.Questions, res.Data)
		if err != nil {
			return judge.Verdict{}, err
		}
		samples = append(samples, s)
	}

	v := judge.Verdict{
		Answers: make(map[string]judge.Answer, len(c.Questions)),
		Usage:   usage,
		Cost:    judge.Cost{Tokens: int64(usage.TotalTokens)},
		Backend: Backend,
		Model:   j.model,
	}
	for _, q := range c.Questions {
		var per []sample
		for _, s := range samples {
			per = append(per, s[q.Name])
		}
		a := aggregate(q, per, j.votes > 1)
		if err := judge.ValidateAnswer(q, a); err != nil {
			return judge.Verdict{}, err
		}
		v.Answers[q.Name] = a
	}
	return v, nil
}

// parse decodes one reply into normalised probs per question.
func parse(qs []judge.Question, data json.RawMessage) (map[string]sample, error) {
	var raw map[string]struct {
		Probs map[string]float64 `json:"probs"`
		Why   string             `json:"why"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("llmjudge: decode reply: %w", err)
	}
	out := make(map[string]sample, len(qs))
	for _, q := range qs {
		r := raw[q.Name]
		var sum float64
		for _, l := range judge.Labels(q) {
			sum += r.Probs[l]
		}
		if !(sum > 0) || math.IsInf(sum, 0) {
			return nil, fmt.Errorf("%w: question %q: stated probabilities sum to %v",
				judge.ErrInvalid, q.Name, sum)
		}
		probs := make(map[string]float64, len(r.Probs))
		for _, l := range judge.Labels(q) {
			probs[l] = r.Probs[l] / sum
		}
		out[q.Name] = sample{probs: probs, why: r.Why}
	}
	return out, nil
}

// aggregate builds q's Answer. One sample: its normalised probs. Votes:
// share of samples whose argmax is each label.
func aggregate(q judge.Question, ss []sample, votes bool) judge.Answer {
	labels := judge.Labels(q)
	probs := ss[0].probs
	source := SourceVerbalized
	if votes {
		source = SourceVotes
		probs = make(map[string]float64, len(labels))
		for _, s := range ss {
			probs[judge.Argmax(s.probs, labels)] += 1 / float64(len(ss))
		}
	}
	value := judge.Argmax(probs, labels)
	a := judge.Answer{
		Value:      value,
		Abstained:  value == judge.LabelAbstain,
		Probs:      probs,
		Confidence: probs[value],
		Source:     source,
	}
	for _, s := range ss {
		if judge.Argmax(s.probs, labels) == value {
			a.Why = s.why
			break
		}
	}
	if q.Kind == judge.KindScore && !a.Abstained {
		a.Score = judge.ScoreEV(probs, len(q.Anchors))
	}
	return a
}

func addUsage(a, b llm.Usage) llm.Usage {
	return llm.Usage{
		PromptTokens:        a.PromptTokens + b.PromptTokens,
		CompletionTokens:    a.CompletionTokens + b.CompletionTokens,
		CacheReadTokens:     a.CacheReadTokens + b.CacheReadTokens,
		CacheCreationTokens: a.CacheCreationTokens + b.CacheCreationTokens,
		TotalTokens:         a.TotalTokens + b.TotalTokens,
	}
}
