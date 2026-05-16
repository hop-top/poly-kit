package verbs

import (
	"time"

	"hop.top/kit/go/conformance/scenario/judge"
)

// judgeRequestFromBlock constructs the judge.Request payload from
// the resolved JudgeBlockSpec, prompt body, and captured input.
// Centralized here so the verb code stays focused on dispatch.
func judgeRequestFromBlock(jb *JudgeBlockSpec, prompt, input string) judge.Request {
	timeout := 60 * time.Second
	maxTokens := 2048
	return judge.Request{
		JudgeID:   jb.ID,
		Prompt:    prompt,
		Model:     jb.Model,
		Input:     input,
		Timeout:   timeout,
		MaxTokens: maxTokens,
	}
}
