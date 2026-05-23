// Payload construction for the four PR-lifecycle topics. Split out
// from main.go so it is directly unit-testable.
package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"hop.top/kit/cmd/kit/init/buswf"
	"hop.top/kit/go/runtime/bus"
)

// maxExcerptBytes bounds failure_summary and excerpt fields (spec §2).
const maxExcerptBytes = 256

// Inputs gathers everything the helper needs to build a payload. All
// fields come from environment variables set by the workflow.
type Inputs struct {
	Kind  string // run.completed | comment.created | pull.merged | pull.closed
	Repo  string
	Actor string

	PRNumber  string
	PRURL     string
	PRBranch  string
	PRHeadSHA string
	PRBaseSHA string

	// run.completed-specific
	RunID         string
	RunName       string
	RunConclusion string
	RunURL        string
	RunLogsURL    string

	// comment.created-specific
	CommentID     string
	CommentAuthor string
	CommentURL    string
	CommentBody   string

	// pull.merged-specific
	MergeCommitSHA string
	MergedAt       string

	// pull.closed-specific
	ClosedAt string
	Reason   string

	// OccurredAt is the timestamp embedded in the envelope; the helper
	// fills it in if blank.
	OccurredAt string
}

// kindToTopic maps the --kind selector to the canonical bus topic.
// Returns ("", false) for unknown kinds.
func kindToTopic(kind string) (bus.Topic, bool) {
	switch kind {
	case "run.completed":
		return buswf.TopicRunCompleted, true
	case "comment.created":
		return buswf.TopicCommentCreated, true
	case "pull.merged":
		return buswf.TopicPullMerged, true
	case "pull.closed":
		return buswf.TopicPullClosed, true
	default:
		return "", false
	}
}

// BuildPayload assembles the JSON-serialisable map for the given
// inputs. Returns the topic that was used (validated against
// bus.ValidateTopic) and the marshalled body.
func BuildPayload(in Inputs) (bus.Topic, []byte, error) {
	topic, ok := kindToTopic(in.Kind)
	if !ok {
		return "", nil, fmt.Errorf("kit-bus-emit: unknown --kind %q", in.Kind)
	}
	if err := bus.ValidateTopic(topic); err != nil {
		// Defence in depth — if someone changes the topic constants
		// without checking, this fires loudly at runtime.
		return "", nil, fmt.Errorf("kit-bus-emit: topic %q invalid: %w", topic, err)
	}

	occurred := in.OccurredAt
	if occurred == "" {
		occurred = time.Now().UTC().Format(time.RFC3339)
	}

	prNumber, _ := strconv.Atoi(strings.TrimSpace(in.PRNumber))
	envelope := map[string]any{
		"topic": string(topic),
		"repo":  in.Repo,
		"pr": map[string]any{
			"number":   prNumber,
			"url":      in.PRURL,
			"branch":   in.PRBranch,
			"head_sha": in.PRHeadSHA,
			"base_sha": in.PRBaseSHA,
		},
		"actor":       in.Actor,
		"occurred_at": occurred,
	}

	switch in.Kind {
	case "run.completed":
		runID, _ := strconv.ParseInt(strings.TrimSpace(in.RunID), 10, 64)
		runObj := map[string]any{
			"id":         runID,
			"name":       in.RunName,
			"conclusion": in.RunConclusion,
			"url":        in.RunURL,
			"logs_url":   in.RunLogsURL,
		}
		// failure_summary only present for failure conclusions.
		// We never embed full logs — the listener fetches logs_url.
		if strings.EqualFold(in.RunConclusion, "failure") {
			summary := fmt.Sprintf("workflow %q failed; see logs at %s",
				in.RunName, in.RunLogsURL)
			runObj["failure_summary"] = truncate(summary, maxExcerptBytes)
		}
		envelope["run"] = runObj

	case "comment.created":
		commentID, _ := strconv.ParseInt(strings.TrimSpace(in.CommentID), 10, 64)
		envelope["comment"] = map[string]any{
			"id":      commentID,
			"kind":    "review",
			"author":  in.CommentAuthor,
			"url":     in.CommentURL,
			"excerpt": truncate(in.CommentBody, maxExcerptBytes),
		}

	case "pull.merged":
		envelope["merge"] = map[string]any{
			"merge_commit_sha": in.MergeCommitSHA,
			"merged_at":        in.MergedAt,
		}

	case "pull.closed":
		envelope["closed_at"] = in.ClosedAt
		envelope["reason"] = in.Reason
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		return "", nil, fmt.Errorf("kit-bus-emit: marshal payload: %w", err)
	}
	return topic, body, nil
}

// truncate enforces the §2 256-byte bound by trimming on rune
// boundaries and appending an ellipsis ("…", 3 bytes UTF-8). When the
// input is already within the limit it is returned unchanged.
//
// Operates on byte length because the spec specifies bytes. The "…"
// suffix is included in the 256-byte budget.
func truncate(s string, max int) string {
	const ellipsis = "…"
	if max <= 0 || len(s) <= max {
		return s
	}
	budget := max - len(ellipsis)
	if budget <= 0 {
		// Pathological max; just return the ellipsis.
		return ellipsis
	}
	// Walk runes so we don't cut in the middle of a multibyte
	// sequence.
	out := []byte{}
	for _, r := range s {
		rb := []byte(string(r))
		if len(out)+len(rb) > budget {
			break
		}
		out = append(out, rb...)
	}
	return string(out) + ellipsis
}
