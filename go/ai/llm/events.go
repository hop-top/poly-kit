package llm

import "time"

// Bus topic constants for llm lifecycle events.
//
// All topics use the kit 4-segment convention
// [Source].[Category].[Object].[Action]. These vars read from
// [DefaultTopics] so there is a single source of truth; adopters that
// need to override should use [WithTopics] or [WithTopicPrefix].
var (
	TopicRequestStart = DefaultTopics.RequestStart
	TopicRequestEnd   = DefaultTopics.RequestEnd
	TopicRequestError = DefaultTopics.RequestError
	TopicFallback     = DefaultTopics.Fallback
	TopicRoute        = DefaultTopics.Route
	TopicEvaResult    = DefaultTopics.EvaResult
)

// RequestStartPayload is published before each LLM call.
type RequestStartPayload struct {
	Request Request
}

// RequestEndPayload is published after a successful LLM call.
type RequestEndPayload struct {
	Response Response
	Duration time.Duration
}

// RequestErrorPayload is published when an LLM call fails terminally.
type RequestErrorPayload struct {
	Err        error  `json:"-"`
	ErrMessage string `json:"error"`
}

// FallbackPayload is published when falling back to the next provider.
type FallbackPayload struct {
	From       int
	To         int
	Err        error  `json:"-"`
	ErrMessage string `json:"error"`
}

// RoutePayload is published after a routing decision.
type RoutePayload struct {
	Router string
	Score  float64
	Model  string
}

// EvaResultPayload is published after an eva contract evaluation.
type EvaResultPayload struct {
	Contract   string
	Passed     bool
	Violations []string
}
