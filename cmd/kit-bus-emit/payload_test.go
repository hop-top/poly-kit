package main

import (
	"encoding/json"
	"strings"
	"testing"

	"hop.top/kit/go/runtime/bus"
)

// TestBuildPayloadAllKinds asserts BuildPayload succeeds for the four
// canonical kinds, that the returned topic passes ValidateTopic, and
// that the payload's "topic" field equals the canonical string.
func TestBuildPayloadAllKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind  string
		topic string
	}{
		{"run.completed", "github.pr.run.completed"},
		{"comment.created", "github.pr.comment.created"},
		{"pull.merged", "github.pr.pull.merged"},
		{"pull.closed", "github.pr.pull.closed"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.kind, func(t *testing.T) {
			t.Parallel()
			in := Inputs{
				Kind: c.kind, Repo: "hop-top/example", Actor: "octocat",
				PRNumber: "123", PRURL: "https://github.com/hop-top/example/pull/123",
				PRBranch: "feat/x", PRHeadSHA: "deadbeef", PRBaseSHA: "cafebabe",
				OccurredAt: "2026-05-23T14:02:11Z",
			}
			topic, body, err := BuildPayload(in)
			if err != nil {
				t.Fatalf("BuildPayload: %v", err)
			}
			if err := bus.ValidateTopic(topic); err != nil {
				t.Fatalf("ValidateTopic(%q): %v", topic, err)
			}
			if string(topic) != c.topic {
				t.Errorf("topic %q, want %q", topic, c.topic)
			}
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("unmarshal: %v\n%s", err, string(body))
			}
			if got["topic"] != c.topic {
				t.Errorf("payload.topic = %v, want %q", got["topic"], c.topic)
			}
			if got["repo"] != "hop-top/example" {
				t.Errorf("payload.repo = %v", got["repo"])
			}
			pr, ok := got["pr"].(map[string]any)
			if !ok {
				t.Fatalf("payload.pr missing or wrong type: %T", got["pr"])
			}
			if n, _ := pr["number"].(float64); int(n) != 123 {
				t.Errorf("pr.number = %v, want 123", pr["number"])
			}
		})
	}
}

// TestBuildPayloadUnknownKind: a kind outside the four canonical
// values is a hard error.
func TestBuildPayloadUnknownKind(t *testing.T) {
	t.Parallel()
	_, _, err := BuildPayload(Inputs{Kind: "label.applied"})
	if err == nil {
		t.Fatal("BuildPayload(label.applied): want error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error %q does not mention unknown", err.Error())
	}
}

// TestBuildPayloadRunCompletedFailureCarriesSummary asserts that a
// failure conclusion populates run.failure_summary; a success
// conclusion omits the field.
func TestBuildPayloadRunCompletedFailureCarriesSummary(t *testing.T) {
	t.Parallel()
	// failure → has failure_summary.
	_, body, err := BuildPayload(Inputs{
		Kind: "run.completed", Repo: "x/y", Actor: "u", PRNumber: "1",
		RunID: "42", RunName: "ci", RunConclusion: "failure",
		RunURL: "https://example.com/run/42", RunLogsURL: "https://example.com/logs/42",
		OccurredAt: "2026-05-23T14:00:00Z",
	})
	if err != nil {
		t.Fatalf("BuildPayload failure: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	run, _ := got["run"].(map[string]any)
	if _, ok := run["failure_summary"]; !ok {
		t.Error("failure: missing run.failure_summary")
	}

	// success → no failure_summary.
	_, body2, err := BuildPayload(Inputs{
		Kind: "run.completed", Repo: "x/y", Actor: "u", PRNumber: "1",
		RunID: "42", RunName: "ci", RunConclusion: "success",
		RunURL: "https://example.com/run/42", RunLogsURL: "https://example.com/logs/42",
		OccurredAt: "2026-05-23T14:00:00Z",
	})
	if err != nil {
		t.Fatalf("BuildPayload success: %v", err)
	}
	var got2 map[string]any
	_ = json.Unmarshal(body2, &got2)
	run2, _ := got2["run"].(map[string]any)
	if _, ok := run2["failure_summary"]; ok {
		t.Error("success: failure_summary should be absent")
	}
}

// TestFailureSummaryBound enforces spec §2 256-byte bound.
func TestFailureSummaryBound(t *testing.T) {
	t.Parallel()
	hugeName := strings.Repeat("workflow-with-very-long-name-", 50)
	_, body, err := BuildPayload(Inputs{
		Kind: "run.completed", Repo: "x/y", Actor: "u", PRNumber: "1",
		RunID: "42", RunName: hugeName, RunConclusion: "failure",
		RunURL: "https://example.com/run/42", RunLogsURL: "https://example.com/logs/42",
		OccurredAt: "2026-05-23T14:00:00Z",
	})
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	run, _ := got["run"].(map[string]any)
	summary, _ := run["failure_summary"].(string)
	if len(summary) > 256 {
		t.Errorf("failure_summary is %d bytes, want ≤ 256", len(summary))
	}
	if !strings.HasSuffix(summary, "…") {
		t.Errorf("oversized summary not truncated with ellipsis: %q", summary)
	}
}

// TestExcerptBound enforces spec §2 256-byte bound on comment excerpt.
func TestExcerptBound(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("Consider extracting this branch into a helper. ", 20)
	_, payload, err := BuildPayload(Inputs{
		Kind: "comment.created", Repo: "x/y", Actor: "u", PRNumber: "1",
		CommentID: "42", CommentAuthor: "octocat",
		CommentURL: "https://github.com/x/y/pull/1#x", CommentBody: body,
		OccurredAt: "2026-05-23T14:00:00Z",
	})
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(payload, &got)
	comment, _ := got["comment"].(map[string]any)
	excerpt, _ := comment["excerpt"].(string)
	if len(excerpt) > 256 {
		t.Errorf("excerpt is %d bytes, want ≤ 256", len(excerpt))
	}
	if !strings.HasSuffix(excerpt, "…") {
		t.Errorf("oversized excerpt not truncated with ellipsis: %q", excerpt)
	}
}

// TestExcerptShortInputUnchanged: when the input fits within 256 bytes,
// it is returned as-is (no ellipsis added).
func TestExcerptShortInputUnchanged(t *testing.T) {
	t.Parallel()
	short := "Consider returning early here."
	_, payload, err := BuildPayload(Inputs{
		Kind: "comment.created", Repo: "x/y", Actor: "u", PRNumber: "1",
		CommentID: "42", CommentAuthor: "octocat",
		CommentURL: "https://github.com/x/y/pull/1#x", CommentBody: short,
		OccurredAt: "2026-05-23T14:00:00Z",
	})
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(payload, &got)
	comment, _ := got["comment"].(map[string]any)
	if got, ok := comment["excerpt"].(string); !ok || got != short {
		t.Errorf("short excerpt mangled: %q != %q", got, short)
	}
}

// TestPullMergedShape pins the merge-specific keys.
func TestPullMergedShape(t *testing.T) {
	t.Parallel()
	_, payload, err := BuildPayload(Inputs{
		Kind: "pull.merged", Repo: "x/y", Actor: "u", PRNumber: "1",
		MergeCommitSHA: "abc1234", MergedAt: "2026-05-23T14:03:00Z",
		OccurredAt: "2026-05-23T14:03:00Z",
	})
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(payload, &got)
	merge, ok := got["merge"].(map[string]any)
	if !ok {
		t.Fatalf("merge object missing: %T", got["merge"])
	}
	if merge["merge_commit_sha"] != "abc1234" {
		t.Errorf("merge_commit_sha = %v", merge["merge_commit_sha"])
	}
	if merge["merged_at"] != "2026-05-23T14:03:00Z" {
		t.Errorf("merged_at = %v", merge["merged_at"])
	}
}

// TestPullClosedShape pins the close-specific keys.
func TestPullClosedShape(t *testing.T) {
	t.Parallel()
	_, payload, err := BuildPayload(Inputs{
		Kind: "pull.closed", Repo: "x/y", Actor: "u", PRNumber: "1",
		ClosedAt: "2026-05-23T14:03:00Z", Reason: "not_planned",
		OccurredAt: "2026-05-23T14:03:00Z",
	})
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(payload, &got)
	if got["closed_at"] != "2026-05-23T14:03:00Z" {
		t.Errorf("closed_at = %v", got["closed_at"])
	}
	if got["reason"] != "not_planned" {
		t.Errorf("reason = %v", got["reason"])
	}
}
