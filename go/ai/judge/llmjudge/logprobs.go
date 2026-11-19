package llmjudge

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

const (
	// SourceLogprobs marks answers read from token log-probabilities.
	SourceLogprobs = "logprobs"

	// LogprobFloor is the probability a label gets when its key is not in
	// the answer token's top-k (before renormalising): about e^-13.8, below
	// anything a top-20 list shows in practice.
	LogprobFloor = 1e-6

	abstainKey = "X"
	yesKey     = "Y"
	noKey      = "N"
	answerProp = "answer"
)

// keyPunct is trimmed from a token before it is compared to a key: JSON
// punctuation, quotes and whitespace a tokenizer glues to the value.
const keyPunct = "{}[]\":, \t\r\n"

// keyed is one label and the single-token key the model sees for it.
type keyed struct{ key, label string }

// keysFor relabels q's labels as single-token keys: A, B, ... in order for
// choice options and score anchors (labels "0", "1", ... for anchors), Y
// and N for yes/no, X for abstain.
func keysFor(q judge.Question) []keyed {
	labels := judge.Labels(q)
	var out []keyed
	for i, l := range labels {
		switch {
		case l == judge.LabelAbstain:
			out = append(out, keyed{abstainKey, l})
		case q.Kind == judge.KindYesNo && l == judge.LabelYes:
			out = append(out, keyed{yesKey, l})
		case q.Kind == judge.KindYesNo && l == judge.LabelNo:
			out = append(out, keyed{noKey, l})
		default:
			out = append(out, keyed{string(rune('A' + i)), l})
		}
	}
	return out
}

// logprobsSchema is the reply schema: one enum property holding a key.
func logprobsSchema(ks []keyed) (json.RawMessage, error) {
	enum := make([]string, len(ks))
	for i, k := range ks {
		enum[i] = k.key
	}
	return json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			answerProp: map[string]any{"type": "string", "enum": enum},
		},
		"required":             []string{answerProp},
		"additionalProperties": false,
	})
}

// judgeLogprobs answers each question with its own call: with several
// questions in one reply each would have its own value position, and a
// wrong guess at one silently skews another's distribution.
func (j *judgeImpl) judgeLogprobs(ctx context.Context, c judge.Case) (judge.Verdict, error) {
	if lc, ok := j.c.(llm.LogprobCompleter); ok && !lc.SupportsLogprobs() {
		return judge.Verdict{}, llm.NoLogprobs("llmjudge")
	}
	for _, q := range c.Questions {
		if n := len(judge.Labels(q)); n > llm.MaxTopLogprobs {
			return judge.Verdict{}, llmerrors.NewCapabilityNotSupported(
				fmt.Sprintf("logprobs mode with %d labels for question %q (top_logprobs allows %d)",
					n, q.Name, llm.MaxTopLogprobs), "llmjudge")
		}
	}

	v := judge.Verdict{
		Answers: make(map[string]judge.Answer, len(c.Questions)),
		Backend: Backend,
		Model:   j.model,
	}
	var usage llm.Usage
	for _, q := range c.Questions {
		ks := keysFor(q)
		schema, err := logprobsSchema(ks)
		if err != nil {
			return judge.Verdict{}, err
		}
		rf := llm.ResponseFormat{Name: "judgment", Schema: schema, Strict: true}
		req := llm.Request{
			Model:       j.model,
			Messages:    buildLogprobMessages(c, q, ks, j.pattern),
			Temperature: j.temp,
			Logprobs:    &llm.LogprobsConfig{TopK: llm.MaxTopLogprobs},
		}
		// No repair: logprobs describe one reply, and a re-ask is a new one.
		res, err := llm.CompleteStructured(ctx, j.c, req, rf, llm.MaxRepairs(0))
		usage = addUsage(usage, res.Usage)
		if err != nil {
			return judge.Verdict{}, err
		}
		a, err := answerFromLogprobs(q, ks, res.Response)
		if err != nil {
			return judge.Verdict{}, err
		}
		if err := judge.ValidateAnswer(q, a); err != nil {
			return judge.Verdict{}, err
		}
		v.Answers[q.Name] = a
	}
	v.Usage = usage
	v.Cost = judge.Cost{Tokens: int64(usage.TotalTokens)}
	if j.price != nil {
		v.Cost.USD = usage.Cost(*j.price)
	}
	return v, nil
}

// answerFromLogprobs reads q's distribution off the answer token.
func answerFromLogprobs(q judge.Question, ks []keyed, r llm.Response) (judge.Answer, error) {
	if len(r.Logprobs) == 0 {
		return judge.Answer{}, llm.NoLogprobs("llmjudge")
	}
	tok, ok := locateAnswerToken(r.Logprobs, ks)
	if !ok {
		return judge.Answer{}, fmt.Errorf(
			"llmjudge: answer token not found in logprobs for question %q (reply %q); "+
				"repair is not applicable in logprobs mode", q.Name, snippet(r.Content))
	}

	// Probability mass per key over every spelling of it in the top-k.
	mass := map[string]float64{}
	seen := map[string]bool{}
	add := func(tok string, lp float64) {
		if k, ok := keyOf(tok, ks); ok {
			mass[k] += math.Exp(lp)
		}
	}
	for _, t := range tok.Top {
		seen[t.Token] = true
		add(t.Token, t.Logprob)
	}
	if !seen[tok.Token] { // the sampled token is not always in Top
		add(tok.Token, tok.Logprob)
	}

	probs := make(map[string]float64, len(ks))
	var sum float64
	for _, k := range ks {
		m, ok := mass[k.key]
		if !ok || m < LogprobFloor {
			m = LogprobFloor
		}
		probs[k.label] = m
		sum += m
	}
	labels := make([]string, len(ks))
	for i, k := range ks {
		probs[k.label] /= sum
		labels[i] = k.label
	}

	value := judge.Argmax(probs, labels)
	a := judge.Answer{
		Value:      value,
		Abstained:  value == judge.LabelAbstain,
		Probs:      probs,
		Confidence: probs[value],
		Source:     SourceLogprobs,
	}
	if q.Kind == judge.KindScore && !a.Abstained {
		a.Score = judge.ScoreEV(probs, len(q.Anchors))
	}
	return a, nil
}

var answerField = regexp.MustCompile(`"` + answerProp + `"\s*:\s*"`)

// locateAnswerToken returns the token that holds the first character of
// the last "answer" string value, provided it is one of the keys once JSON
// punctuation and whitespace are trimmed. A tokenizer may merge the
// opening quote into it (`"B`) or the closing one (`B"}`); an offset walk
// over the concatenated tokens finds it either way.
func locateAnswerToken(ts []llm.TokenLogprob, ks []keyed) (llm.TokenLogprob, bool) {
	var b strings.Builder
	starts := make([]int, len(ts))
	for i, t := range ts {
		starts[i] = b.Len()
		b.WriteString(t.Token)
	}
	text := b.String()
	// Last match: a reasoning model's thinking tokens come first and may
	// quote the JSON; the reply is the tail.
	locs := answerField.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return llm.TokenLogprob{}, false
	}
	loc := locs[len(locs)-1]
	pos := loc[1]
	for i, t := range ts {
		end := starts[i] + len(t.Token)
		if pos < end {
			if starts[i] > pos {
				return llm.TokenLogprob{}, false
			}
			if _, ok := keyOf(t.Token, ks); ok {
				return t, true
			}
			return llm.TokenLogprob{}, false
		}
	}
	return llm.TokenLogprob{}, false
}

// keyOf returns the key a token spells once trimmed of JSON punctuation.
func keyOf(tok string, ks []keyed) (string, bool) {
	t := strings.Trim(tok, keyPunct)
	for _, k := range ks {
		if k.key == t {
			return t, true
		}
	}
	return "", false
}

func snippet(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}
