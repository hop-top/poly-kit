package llmjudge

import (
	"encoding/json"
	"fmt"
	"strings"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/prompt"
)

const systemPrompt = `You judge a case by answering typed questions about it.
For each question pick the single most likely label as "value", state your
confidence in it as a number between 0 and 1, and add a one-sentence reason in
"why". For a locate question give "box" instead of "value": the edges of the
tightest box around the target, as fractions between 0 and 1 of the image
height (top, bottom; 0 is the top edge) and width (left, right; 0 is the left
edge), top below bottom and left before right. Reply with JSON only.`

// boxSchema is a bounding box: four fractions of the image.
func boxSchema() map[string]any {
	edge := map[string]any{"type": "number", "minimum": 0, "maximum": 1}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"top": edge, "bottom": edge, "left": edge, "right": edge,
		},
		"required":             []string{"top", "bottom", "left", "right"},
		"additionalProperties": false,
	}
}

// groundingSchema is a locate question's reply object. With abstain the
// model also says whether the target is absent; its box is then ignored.
func groundingSchema(q judge.Question) map[string]any {
	props := map[string]any{
		"box":        boxSchema(),
		"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"why":        map[string]any{"type": "string"},
	}
	req := []string{"box", "confidence", "why"}
	if q.Abstain {
		props["abstain"] = map[string]any{"type": "boolean"}
		req = append(req, "abstain")
	}
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             req,
		"additionalProperties": false,
	}
}

// buildSchema returns the JSON Schema of a reply: per question name, an
// object {value: enum of that question's labels, confidence, why}.
func buildSchema(qs []judge.Question) (json.RawMessage, error) {
	props := make(map[string]any, len(qs))
	names := make([]string, 0, len(qs))
	for _, q := range qs {
		names = append(names, q.Name)
		if q.Kind == judge.KindGrounding {
			props[q.Name] = groundingSchema(q)
			continue
		}
		props[q.Name] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value":      map[string]any{"type": "string", "enum": judge.Labels(q)},
				"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				"why":        map[string]any{"type": "string"},
			},
			"required":             []string{"value", "confidence", "why"},
			"additionalProperties": false,
		}
	}
	return json.Marshal(map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             names,
		"additionalProperties": false,
	})
}

// buildMessages renders state and questions; Assets ride as extra parts.
func buildMessages(c judge.Case, p *prompt.Pattern) []llm.Message {
	var b strings.Builder
	if len(c.State) > 0 {
		st, err := json.MarshalIndent(c.State, "", "  ")
		if err != nil {
			st = []byte(fmt.Sprintf("%v", c.State))
		}
		b.WriteString("State:\n" + string(st) + "\n\n")
	}
	b.WriteString("Questions:\n")
	for i, q := range c.Questions {
		fmt.Fprintf(&b, "\n%d. %s (%s)\n", i+1, q.Name, kindText(q))
		if q.Instructions != "" {
			b.WriteString(q.Instructions + "\n")
		}
		if q.Kind == judge.KindGrounding {
			b.WriteString(groundingHelp(q))
			continue
		}
		b.WriteString("Labels:\n")
		for _, l := range labelLines(q) {
			b.WriteString("- " + l + "\n")
		}
	}
	user := llm.Message{Role: "user", Content: b.String()}
	if len(c.Assets) > 0 {
		user.Parts = append([]llm.ContentPart{{Type: llm.PartTypeText, Text: b.String()}}, assetParts(c)...)
	}
	sys := systemPrompt
	if p != nil {
		sys = p.System
	}
	return []llm.Message{{Role: "system", Content: sys}, user}
}

// assetParts is c.Assets; with a grounding question each image follows a
// text part naming it, so instructions can refer to it ("the mug in
// `photo`").
func assetParts(c judge.Case) []llm.ContentPart {
	grounding := false
	for _, q := range c.Questions {
		grounding = grounding || q.Kind == judge.KindGrounding
	}
	if !grounding {
		return c.Assets
	}
	names := judge.ImageNames(c.Assets)
	var out []llm.ContentPart
	i := 0
	for _, a := range c.Assets {
		if a.Type == llm.PartTypeImage {
			out = append(out, llm.ContentPart{Type: llm.PartTypeText, Text: fmt.Sprintf("Image `%s`:", names[i])})
			i++
		}
		out = append(out, a)
	}
	return out
}

func groundingHelp(q judge.Question) string {
	s := "Reply with \"box\": top, bottom, left, right as fractions of the image (0 to 1, origin top-left).\n"
	if q.Abstain {
		s += "If the target is not in the image set \"abstain\" to true (box is then ignored, use 0 for each edge); else false.\n"
	}
	return s
}

func kindText(q judge.Question) string {
	switch q.Kind {
	case judge.KindChoice:
		return "choose one"
	case judge.KindScore:
		return "score on ordered anchors, 0 lowest"
	case judge.KindGrounding:
		return "locate in an image"
	default:
		return "yes or no"
	}
}

func labelLines(q judge.Question) []string {
	var ls []string
	switch q.Kind {
	case judge.KindChoice:
		for _, o := range q.Options {
			ls = append(ls, describe(o.Value, o.Description))
		}
	case judge.KindScore:
		for i, a := range q.Anchors {
			ls = append(ls, describe(fmt.Sprint(i), a))
		}
	default:
		ls = []string{judge.LabelYes, judge.LabelNo}
	}
	if q.Abstain {
		ls = append(ls, describe(judge.LabelAbstain, "cannot tell from the given information"))
	}
	return ls
}

func describe(label, desc string) string {
	if desc == "" {
		return label
	}
	return label + ": " + desc
}
