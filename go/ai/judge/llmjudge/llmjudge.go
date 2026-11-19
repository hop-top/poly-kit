// Package llmjudge is a judge.Judge backed by a chat model. Verbalized
// mode asks for schema-constrained JSON holding a value, a confidence and
// a reason per question; Votes(n) takes n samples and turns the share of
// samples per label into probabilities (self-consistency).
package llmjudge

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
	"hop.top/kit/go/ai/prompt"
)

const (
	// Backend is Verdict.Backend.
	Backend = "llm"
	// SourceVerbalized marks answers from one sample's stated confidence.
	SourceVerbalized = "verbalized"
	// SourceVotes marks answers aggregated from several samples.
	SourceVotes = "votes"

	// defaultVoteTemperature spreads samples when votes > 1 and no
	// temperature was set; identical samples would add nothing.
	defaultVoteTemperature = 0.7
)

// ModeKind is how a judge turns model replies into answers.
type ModeKind struct {
	votes    int
	logprobs bool
}

// Verbalized takes one sample; Answer.Confidence is the model's stated
// confidence and Answer.Probs is empty.
var Verbalized = ModeKind{votes: 1}

// Logprobs takes one call per question and reads Answer.Probs off the
// answer token's top_logprobs: options are relabelled as single-token keys
// (A, B, ...; Y/N for yes/no; X for abstain) under an enum schema, the
// keys' log-probabilities are softmaxed and renormalised, and keys missing
// from the top-k get [LogprobFloor]. Answer.Source is "logprobs", Why is
// empty. Needs a completer that returns logprobs (see llm.LogprobCompleter)
// and at most llm.MaxTopLogprobs labels per question; no repair, so
// MaxRepairs is ignored. Calibrate afterwards with calibrate.Calibrated.
var Logprobs = ModeKind{votes: 1, logprobs: true}

// Votes takes n samples per case, sequentially; Answer.Probs is each
// label's share of the samples' values. n < 1 means 1. Any failed sample
// fails the whole case.
func Votes(n int) ModeKind {
	if n < 1 {
		n = 1
	}
	return ModeKind{votes: n}
}

type judgeImpl struct {
	c          llm.Completer
	model      string
	mode       ModeKind
	temp       *float64
	maxRepairs *int
	price      *llm.Price
	pattern    *prompt.Pattern
}

// Option configures a judge.
type Option func(*judgeImpl)

// Model sets the model on every request and Verdict.Model; unset uses the
// client default. Responses carry no model name, so none is read back.
func Model(m string) Option { return func(j *judgeImpl) { j.model = m } }

// Price fills Verdict.Cost.USD from the verdict's Usage.
func Price(p llm.Price) Option { return func(j *judgeImpl) { j.price = &p } }

// Pattern renders the system message from p instead of the built-in
// wording. The reply schema is enforced either way.
func Pattern(p *prompt.Pattern) Option { return func(j *judgeImpl) { j.pattern = p } }

// Mode picks Verbalized (default), Votes(n) or Logprobs.
func Mode(m ModeKind) Option { return func(j *judgeImpl) { j.mode = m } }

// Temperature sets the sampling temperature, 0 included. Unset: the
// provider default for Verbalized, 0.7 for Votes(n > 1).
func Temperature(t float64) Option { return func(j *judgeImpl) { j.temp = &t } }

// MaxRepairs bounds schema-repair retries per sample; unset keeps the llm
// package default.
func MaxRepairs(n int) Option { return func(j *judgeImpl) { j.maxRepairs = &n } }

// New returns a judge over c; *llm.Client satisfies llm.Completer.
func New(c llm.Completer, opts ...Option) judge.Judge {
	j := &judgeImpl{c: c, mode: Verbalized}
	for _, o := range opts {
		o(j)
	}
	return j
}

// sample is one model reply for one question.
type sample struct {
	value      string
	confidence float64
	why        string
}

// Judge answers every question of c, one model call per sample. Kinds
// other than choice, score and yes/no fail with an
// llmerrors.ErrCapabilityNotSupported; a malformed case fails with
// judge.ErrInvalid; output that never matches the schema fails with
// llm.ErrSchemaInvalid.
func (j *judgeImpl) Judge(ctx context.Context, c judge.Case) (judge.Verdict, error) {
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
	if j.mode.logprobs {
		return j.judgeLogprobs(ctx, c)
	}

	schema, err := buildSchema(c.Questions)
	if err != nil {
		return judge.Verdict{}, err
	}
	rf := llm.ResponseFormat{Name: "judgment", Schema: schema, Strict: true}
	req := llm.Request{Model: j.model, Messages: buildMessages(c, j.pattern), Temperature: j.temp}
	if j.temp == nil && j.mode.votes > 1 {
		t := defaultVoteTemperature
		req.Temperature = &t
	}
	var sopts []llm.StructuredOption
	if j.maxRepairs != nil {
		sopts = append(sopts, llm.MaxRepairs(*j.maxRepairs))
	}

	samples := make([]map[string]sample, 0, j.mode.votes)
	var usage llm.Usage
	for i := 0; i < j.mode.votes; i++ {
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
	if j.price != nil {
		v.Cost.USD = usage.Cost(*j.price)
	}
	votes := j.mode.votes > 1
	for _, q := range c.Questions {
		var per []sample
		for _, s := range samples {
			per = append(per, s[q.Name])
		}
		a := aggregate(q, per, votes)
		if err := judge.ValidateAnswer(q, a); err != nil {
			return judge.Verdict{}, err
		}
		v.Answers[q.Name] = a
	}
	return v, nil
}

// parse decodes one reply into a sample per question.
func parse(qs []judge.Question, data json.RawMessage) (map[string]sample, error) {
	var raw map[string]struct {
		Value      string  `json:"value"`
		Confidence float64 `json:"confidence"`
		Why        string  `json:"why"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("llmjudge: decode reply: %w", err)
	}
	out := make(map[string]sample, len(qs))
	for _, q := range qs {
		r := raw[q.Name]
		out[q.Name] = sample{value: r.Value, confidence: r.Confidence, why: r.Why}
	}
	return out, nil
}

// aggregate builds q's Answer. One sample: its value and stated
// confidence, no Probs. Votes: Probs are each label's share of samples.
func aggregate(q judge.Question, ss []sample, votes bool) judge.Answer {
	if !votes {
		s := ss[0]
		a := judge.Answer{
			Value:      s.value,
			Abstained:  s.value == judge.LabelAbstain,
			Confidence: s.confidence,
			Why:        s.why,
			Source:     SourceVerbalized,
		}
		if q.Kind == judge.KindScore && !a.Abstained {
			if i, err := strconv.Atoi(s.value); err == nil {
				a.Score = judge.ScoreFromIndex(float64(i), len(q.Anchors))
			}
		}
		return a
	}

	labels := judge.Labels(q)
	probs := make(map[string]float64, len(labels))
	for _, s := range ss {
		probs[s.value] += 1 / float64(len(ss))
	}
	value := judge.Argmax(probs, labels)
	a := judge.Answer{
		Value:      value,
		Abstained:  value == judge.LabelAbstain,
		Probs:      probs,
		Confidence: probs[value],
		Source:     SourceVotes,
	}
	for _, s := range ss {
		if s.value == value {
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
