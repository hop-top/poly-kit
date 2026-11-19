package llmjudge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // register decoders for image.DecodeConfig
	_ "image/png"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/prompt"
)

// BoxConvention is how a model writes a bounding box. Local vision models
// ground in their own coordinates and fail a fractions JSON schema (some
// answer 0.5 for every edge), so a convention other than [Fractions] asks
// in free text, in the model's native coordinates, and converts.
type BoxConvention int

const (
	// Fractions asks for {top, bottom, left, right} in [0,1] under a JSON
	// schema. The default; fits models that follow schemas and ground in
	// fractions.
	Fractions BoxConvention = iota
	// Grid1000 asks for {"bbox_2d": [x1, y1, x2, y2]} on a 0-1000 grid,
	// origin top-left, in free text: Qwen3-VL and Qwen2-VL.
	Grid1000
	// Pixels asks for the same array in pixels of the image and divides by
	// its decoded width and height: Qwen2.5-VL. It needs exactly one image
	// asset (PNG or JPEG). Qwen2.5-VL counts pixels of its resized input,
	// so boxes drift on large images; prefer Grid1000 models.
	Pixels
)

// BoxFormat sets the box convention for grounding questions; default
// [Fractions]. Grid1000 and Pixels need every question of the case to be
// grounding (others fail with llmerrors.ErrCapabilityNotSupported), send
// no response schema, make one call per grounding question, and
// parse leniently: a Markdown fence or prose around the JSON, an array of
// objects (the first box wins), numbers as strings, a string-encoded
// array, or any key holding four numbers. A reply with no box is sent back
// up to [MaxRepairs] times (default 2) with a corrective message, then
// fails with llm.ErrSchemaInvalid. The box is then clamped to [0,1] and
// reordered (min before max per axis); a box with no area, or covering the
// whole image, is a model fallback and not repaired: it fails with
// judge.ErrInvalid.
func BoxFormat(c BoxConvention) Option { return func(j *judgeImpl) { j.boxFormat = c } }

// BoxFormatFor picks a convention from a model name by prefix, ignoring
// case, registry and namespace: qwen3-vl and qwen2-vl give [Grid1000],
// qwen2.5vl and qwen2.5-vl give [Pixels], any other [Fractions].
func BoxFormatFor(model string) BoxConvention {
	m := strings.ToLower(model)
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	switch {
	case strings.HasPrefix(m, "qwen3-vl"), strings.HasPrefix(m, "qwen2-vl"):
		return Grid1000
	case strings.HasPrefix(m, "qwen2.5vl"), strings.HasPrefix(m, "qwen2.5-vl"):
		return Pixels
	}
	return Fractions
}

const freeSystemPrompt = `You locate things in images. Reply with JSON only, no code fence and no explanation outside it.`

// judgeFreeBoxes answers every grounding question of c with one free-text
// call each, in the judge's box convention.
func (j *judgeImpl) judgeFreeBoxes(ctx context.Context, c judge.Case) (judge.Verdict, error) {
	var w, h int
	if j.boxFormat == Pixels {
		var err error
		if w, h, err = pixelSize(ctx, c.Assets); err != nil {
			return judge.Verdict{}, err
		}
	}
	maxRepairs := 2
	if j.maxRepairs != nil {
		maxRepairs = max(*j.maxRepairs, 0)
	}
	v := judge.Verdict{
		Answers: make(map[string]judge.Answer, len(c.Questions)),
		Backend: Backend,
		Model:   j.model,
	}
	var usage llm.Usage
	for _, q := range c.Questions {
		msgs := buildFreeMessages(c, q, j.boxFormat, j.pattern)
		req := llm.Request{Model: j.model, Messages: msgs, Temperature: j.temp}
		a, u, err := j.askBox(ctx, req, q, w, h, maxRepairs)
		usage = addUsage(usage, u)
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

// askBox runs one grounding question: ask, parse, and on an unparsable
// reply send it back with a corrective message.
func (j *judgeImpl) askBox(ctx context.Context, req llm.Request, q judge.Question, w, h, maxRepairs int) (judge.Answer, llm.Usage, error) {
	var usage llm.Usage
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return judge.Answer{}, usage, err
		}
		resp, err := j.c.Complete(ctx, req)
		if err != nil {
			return judge.Answer{}, usage, err
		}
		usage = addUsage(usage, resp.Usage)
		r, perr := parseBoxReply(resp.Content, q.Abstain)
		if perr == nil {
			a := judge.Answer{Confidence: r.confidence, Why: r.why, Source: SourceVerbalized}
			if r.abstain {
				a.Value, a.Abstained = judge.LabelAbstain, true
				return a, usage, nil
			}
			b, err := j.toBox(r.coords, w, h)
			if err != nil {
				return judge.Answer{}, usage, fmt.Errorf("question %q: %w", q.Name, err)
			}
			a.Box = &b
			return a, usage, nil
		}
		if attempt > maxRepairs {
			return judge.Answer{}, usage, &llm.SchemaError{
				Attempts: attempt, Violations: []string{perr.Error()}, Last: resp.Content,
			}
		}
		req.Messages = append(append([]llm.Message(nil), req.Messages...),
			llm.Message{Role: "assistant", Content: resp.Content},
			llm.Message{Role: "user", Content: repairBoxPrompt(perr, q)})
	}
}

// toBox converts model coordinates [x1, y1, x2, y2] to a Box: scale to
// fractions, reorder, clamp, then reject degenerate boxes.
func (j *judgeImpl) toBox(c [4]float64, w, h int) (judge.Box, error) {
	sx, sy := 1000.0, 1000.0
	if j.boxFormat == Pixels {
		sx, sy = float64(w), float64(h)
	}
	x1, y1, x2, y2 := c[0]/sx, c[1]/sy, c[2]/sx, c[3]/sy
	b := judge.Box{
		Left: clamp01(math.Min(x1, x2)), Right: clamp01(math.Max(x1, x2)),
		Top: clamp01(math.Min(y1, y2)), Bottom: clamp01(math.Max(y1, y2)),
	}
	switch {
	case b.Left >= b.Right || b.Top >= b.Bottom:
		return judge.Box{}, fmt.Errorf("%w: box %v has no area inside the image", judge.ErrInvalid, c)
	case b.Left <= 0 && b.Top <= 0 && b.Right >= 1 && b.Bottom >= 1:
		return judge.Box{}, fmt.Errorf("%w: box %v covers the whole image (model fallback)", judge.ErrInvalid, c)
	}
	return b, nil
}

func clamp01(f float64) float64 { return math.Min(1, math.Max(0, f)) }

// pixelSize is the pixel width and height of the case's only image.
func pixelSize(ctx context.Context, assets []llm.ContentPart) (int, int, error) {
	var img *llm.ContentPart
	for i := range assets {
		if assets[i].Type != llm.PartTypeImage {
			continue
		}
		if img != nil {
			return 0, 0, fmt.Errorf("%w: Pixels box format needs exactly one image asset", judge.ErrInvalid)
		}
		img = &assets[i]
	}
	if img == nil || img.Source == nil {
		return 0, 0, fmt.Errorf("%w: Pixels box format needs an image asset with a source", judge.ErrInvalid)
	}
	rc, err := img.Source.Reader(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: read image for its size: %v", judge.ErrInvalid, err)
	}
	defer rc.Close()
	cfg, _, err := image.DecodeConfig(io.LimitReader(rc, 64<<20))
	if err != nil {
		return 0, 0, fmt.Errorf("%w: image size (PNG or JPEG only): %v", judge.ErrInvalid, err)
	}
	if cfg.Width < 1 || cfg.Height < 1 {
		return 0, 0, fmt.Errorf("%w: image is %dx%d", judge.ErrInvalid, cfg.Width, cfg.Height)
	}
	return cfg.Width, cfg.Height, nil
}

// buildFreeMessages asks one grounding question in free text, in bf's
// coordinates. The system message is the pattern's, when one is set.
func buildFreeMessages(c judge.Case, q judge.Question, bf BoxConvention, p *prompt.Pattern) []llm.Message {
	var b strings.Builder
	if len(c.State) > 0 {
		st, err := json.MarshalIndent(c.State, "", "  ")
		if err != nil {
			st = []byte(fmt.Sprintf("%v", c.State))
		}
		b.WriteString("State:\n" + string(st) + "\n\n")
	}
	b.WriteString("Locate in the image: " + q.Instructions + "\n")
	b.WriteString("Output its bounding box as JSON: {\"bbox_2d\": [x1, y1, x2, y2]}, ")
	switch bf {
	case Pixels:
		b.WriteString("with (x1, y1) the top-left and (x2, y2) the bottom-right corner, in pixels of the image as given.\n")
	default:
		b.WriteString("with (x1, y1) the top-left and (x2, y2) the bottom-right corner, " +
			"on a grid of 0 to 1000 over the image width (x) and height (y); origin top-left.\n")
	}
	b.WriteString("Also add \"confidence\" (0 to 1) and \"why\" (one short sentence).\n")
	if q.Abstain {
		b.WriteString("If it is not in the image reply {\"abstain\": true} instead.\n")
	}
	user := llm.Message{Role: "user", Content: b.String()}
	user.Parts = append([]llm.ContentPart{{Type: llm.PartTypeText, Text: b.String()}}, assetParts(c)...)
	sys := freeSystemPrompt
	if p != nil {
		sys = p.System
	}
	return []llm.Message{{Role: "system", Content: sys}, user}
}

// repairBoxPrompt tells the model its reply held no usable box.
func repairBoxPrompt(err error, q judge.Question) string {
	s := "Your previous reply is unusable: " + err.Error() + ".\n" +
		"Reply again with JSON only: {\"bbox_2d\": [x1, y1, x2, y2]}"
	if q.Abstain {
		s += " (or {\"abstain\": true} if the target is not in the image)"
	}
	return s + ", no code fence, no explanation."
}

// boxReply is what a free-text reply held.
type boxReply struct {
	coords     [4]float64
	confidence float64
	why        string
	abstain    bool
}

// parseBoxReply finds the first box in a model reply; see [BoxFormat].
// With allowAbstain a reply saying "abstain": true needs no box.
func parseBoxReply(text string, allowAbstain bool) (boxReply, error) {
	root, err := decodeLoose(text)
	if err != nil {
		return boxReply{}, err
	}
	if m, ok := root.(map[string]any); ok && allowAbstain && truthy(m["abstain"]) {
		return boxReply{abstain: true, confidence: unitNumber(m["confidence"]), why: str(m["why"])}, nil
	}
	coords, holder, ok := findBox(root, nil)
	if !ok {
		return boxReply{}, fmt.Errorf("no array of four numbers [x1, y1, x2, y2] in the reply")
	}
	r := boxReply{coords: coords}
	if holder != nil {
		r.confidence = unitNumber(holder["confidence"])
		r.why = str(holder["why"])
	}
	return r, nil
}

// decodeLoose decodes the first JSON value in text, skipping a Markdown
// fence and any prose before it.
func decodeLoose(text string) (any, error) {
	s := text
	for i := 0; i < len(s); i++ {
		if s[i] != '{' && s[i] != '[' {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader([]byte(s[i:])))
		var v any
		if err := dec.Decode(&v); err == nil {
			return v, nil
		}
	}
	return nil, fmt.Errorf("no JSON value in the reply")
}

// preferredKeys are tried first when an object holds several candidates.
var preferredKeys = []string{"bbox_2d", "bbox", "box_2d", "box", "bounding_box"}

// findBox returns the first 4-number array in v, depth first, with the
// object that held it. holder is the nearest enclosing object.
func findBox(v any, holder map[string]any) ([4]float64, map[string]any, bool) {
	switch t := v.(type) {
	case []any:
		if c, ok := fourNumbers(t); ok {
			return c, holder, true
		}
		for _, e := range t {
			if c, h, ok := findBox(e, holder); ok {
				return c, h, true
			}
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for _, k := range preferredKeys {
			if _, ok := t[k]; ok {
				keys = append(keys, k)
			}
		}
		var rest []string
		for k := range t {
			if !contains(preferredKeys, k) {
				rest = append(rest, k)
			}
		}
		sort.Strings(rest)
		for _, k := range append(keys, rest...) {
			if c, h, ok := findBox(t[k], t); ok {
				return c, h, true
			}
		}
	case string:
		s := strings.TrimSpace(t)
		if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{") {
			var inner any
			if json.Unmarshal([]byte(s), &inner) == nil {
				return findBox(inner, holder)
			}
		}
	}
	return [4]float64{}, nil, false
}

// fourNumbers reads a 4-element array of numbers or numeric strings.
func fourNumbers(a []any) ([4]float64, bool) {
	var out [4]float64
	if len(a) != 4 {
		return out, false
	}
	for i, e := range a {
		f, ok := number(e)
		if !ok {
			return out, false
		}
		out[i] = f
	}
	return out, true
}

func number(v any) (float64, bool) {
	var f float64
	switch t := v.(type) {
	case float64:
		f = t
	case string:
		var err error
		if f, err = strconv.ParseFloat(strings.TrimSpace(t), 64); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0)
}

// unitNumber is v as a number in [0,1]; anything else is 0 (unknown).
func unitNumber(v any) float64 {
	if f, ok := number(v); ok && f >= 0 && f <= 1 {
		return f
	}
	return 0
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	}
	return false
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
