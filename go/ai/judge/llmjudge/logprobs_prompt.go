package llmjudge

import (
	"encoding/json"
	"fmt"
	"strings"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/prompt"
)

const logprobsSystemPrompt = `You judge a case by answering one typed question about it.
Each label has a one-letter key. Reply with JSON only: {"answer": "<key>"}
holding the key of the single most likely label.`

// buildLogprobMessages renders state and one question, labels shown by key.
func buildLogprobMessages(c judge.Case, q judge.Question, ks []keyed, p *prompt.Pattern) []llm.Message {
	var b strings.Builder
	if len(c.State) > 0 {
		st, err := json.MarshalIndent(c.State, "", "  ")
		if err != nil {
			st = []byte(fmt.Sprintf("%v", c.State))
		}
		b.WriteString("State:\n" + string(st) + "\n\n")
	}
	fmt.Fprintf(&b, "Question: %s (%s)\n", q.Name, kindText(q))
	if q.Instructions != "" {
		b.WriteString(q.Instructions + "\n")
	}
	b.WriteString("Labels:\n")
	for i, l := range lpLines(q) {
		b.WriteString(ks[i].key + ": " + l + "\n")
	}
	user := llm.Message{Role: "user", Content: b.String()}
	if len(c.Assets) > 0 {
		user.Parts = append([]llm.ContentPart{{Type: llm.PartTypeText, Text: b.String()}}, c.Assets...)
	}
	sys := logprobsSystemPrompt
	if p != nil {
		sys = p.System
	}
	return []llm.Message{{Role: "system", Content: sys}, user}
}

// lpLines is labelLines without anchor indices: the key replaces them.
func lpLines(q judge.Question) []string {
	if q.Kind != judge.KindScore {
		return labelLines(q)
	}
	ls := append([]string(nil), q.Anchors...)
	if q.Abstain {
		ls = append(ls, describe(judge.LabelAbstain, "cannot tell from the given information"))
	}
	return ls
}
