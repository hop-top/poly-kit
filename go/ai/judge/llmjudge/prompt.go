package llmjudge

import (
	"encoding/json"
	"fmt"
	"strings"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/llm"
)

const systemPrompt = `You judge a case by answering typed questions about it.
For each question give a probability for every listed label, between 0 and 1,
reflecting how likely that label is the right answer; they should sum to 1.
Add a one-sentence reason in "why". Reply with JSON only.`

// buildSchema returns the JSON Schema of a reply: per question name, an
// object with probs (exactly that question's labels) and why.
func buildSchema(qs []judge.Question) (json.RawMessage, error) {
	props := make(map[string]any, len(qs))
	names := make([]string, 0, len(qs))
	for _, q := range qs {
		labels := judge.Labels(q)
		lp := make(map[string]any, len(labels))
		for _, l := range labels {
			lp[l] = map[string]any{"type": "number", "minimum": 0, "maximum": 1}
		}
		props[q.Name] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"probs": map[string]any{
					"type":                 "object",
					"properties":           lp,
					"required":             labels,
					"additionalProperties": false,
				},
				"why": map[string]any{"type": "string"},
			},
			"required":             []string{"probs", "why"},
			"additionalProperties": false,
		}
		names = append(names, q.Name)
	}
	return json.Marshal(map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             names,
		"additionalProperties": false,
	})
}

// buildMessages renders state and questions; Assets ride as extra parts.
func buildMessages(c judge.Case) []llm.Message {
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
		b.WriteString("Labels:\n")
		for _, l := range labelLines(q) {
			b.WriteString("- " + l + "\n")
		}
	}
	user := llm.Message{Role: "user", Content: b.String()}
	if len(c.Assets) > 0 {
		user.Parts = append([]llm.ContentPart{{Type: llm.PartTypeText, Text: b.String()}}, c.Assets...)
	}
	return []llm.Message{{Role: "system", Content: systemPrompt}, user}
}

func kindText(q judge.Question) string {
	switch q.Kind {
	case judge.KindChoice:
		return "choose one"
	case judge.KindScore:
		return "score on ordered anchors, 0 lowest"
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
