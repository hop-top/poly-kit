package jev

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// request is the System One request body.
type request struct {
	Model     string              `json:"model"`
	State     map[string]any      `json:"state"`
	Questions map[string]question `json:"questions"`
	Assets    map[string]asset    `json:"assets,omitempty"`
}

// asset is one image, referenced from the state by its ref.
type asset struct {
	Type       string `json:"type"`
	Format     string `json:"format"`
	DataBase64 string `json:"data_base64"`
}

func mimeOf(p llm.ContentPart) string {
	m := p.MimeType
	if m == "" && p.Source != nil {
		m = p.Source.MimeType()
	}
	return strings.ToLower(strings.TrimSpace(m))
}

// imageFormat is the service's format name for p's MIME type.
func imageFormat(p llm.ContentPart) (string, bool) {
	switch mimeOf(p) {
	case "image/png":
		return "png", true
	case "image/jpeg", "image/jpg":
		return "jpg", true
	}
	return "", false
}

// addAssets reads the case's images into req: each under ref
// "asset-<name>", the state pointing its name at the ref.
func addAssets(ctx context.Context, req *request, c judge.Case) error {
	names := judge.ImageNames(c.Assets)
	if len(names) == 0 {
		return nil
	}
	req.Assets = make(map[string]asset, len(names))
	i := 0
	for _, p := range c.Assets {
		if p.Type != llm.PartTypeImage {
			continue
		}
		name := names[i]
		i++
		if p.Source == nil {
			return fmt.Errorf("%w: image %q has no source", judge.ErrInvalid, name)
		}
		rc, err := p.Source.Reader(ctx)
		if err != nil {
			return fmt.Errorf("jev: read image %q: %w", name, err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxImage+1))
		rc.Close()
		if err != nil {
			return fmt.Errorf("jev: read image %q: %w", name, err)
		}
		if len(data) > maxImage {
			return llmerrors.NewMediaTooLarge(int64(len(data)), maxImage, Backend, nil)
		}
		format, _ := imageFormat(p)
		ref := "asset-" + name
		req.State[name] = ref
		req.Assets[ref] = asset{Type: "image", Format: format, DataBase64: base64.StdEncoding.EncodeToString(data)}
	}
	return nil
}

// question is one System One question. Criteria is a choice's
// {value: description} object or a score's ordered array; a noul sends
// none.
type question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions,omitempty"`
	Criteria     any    `json:"criteria,omitempty"`
}

func buildRequest(ctx context.Context, model string, c judge.Case) (request, error) {
	state := make(map[string]any, len(c.State)+len(c.Assets))
	for k, v := range c.State {
		state[k] = v
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
		case judge.KindGrounding:
			wq.Type = "box" // no criteria; the target is named in instructions
		}
		qs[q.Name] = wq
	}
	req := request{Model: model, State: state, Questions: qs}
	if err := addAssets(ctx, &req, c); err != nil {
		return request{}, err
	}
	return req, nil
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
	Box           *wireBox           `json:"box"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// wireBox is a box answer's edges, fractions of the image.
type wireBox struct {
	Top    *float64 `json:"top"`
	Bottom *float64 `json:"bottom"`
	Left   *float64 `json:"left"`
	Right  *float64 `json:"right"`
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
		judge.KindGrounding: "box",
	}[q.Kind]
	if w.Type != want {
		return judge.Answer{}, malformed("answer %q: type %q, want %q", q.Name, w.Type, want)
	}
	switch q.Kind {
	case judge.KindYesNo:
		return yesNoAnswer(q, w)
	case judge.KindGrounding:
		return boxAnswer(q, w)
	default:
		return distAnswer(q, w)
	}
}

// boxAnswer reads a box answer. A null confidence is 0 (unknown). With
// no box the target is absent: an abstention when q allows one, else
// malformed. A box with any edge missing is malformed.
func boxAnswer(q judge.Question, w wireAnswer) (judge.Answer, error) {
	a := judge.Answer{Source: SourceNative}
	if w.Confidence != nil {
		if !unit(*w.Confidence) {
			return a, malformed("answer %q: confidence %v outside [0,1]", q.Name, *w.Confidence)
		}
		a.Confidence = *w.Confidence
	}
	if w.Box == nil {
		if !q.Abstain {
			return a, malformed("answer %q: no box", q.Name)
		}
		a.Value, a.Abstained = judge.LabelAbstain, true
		return a, nil
	}
	b := w.Box
	if b.Top == nil || b.Bottom == nil || b.Left == nil || b.Right == nil {
		return a, malformed("answer %q: box needs top, bottom, left and right", q.Name)
	}
	a.Box = &judge.Box{Top: *b.Top, Bottom: *b.Bottom, Left: *b.Left, Right: *b.Right}
	return a, nil
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
