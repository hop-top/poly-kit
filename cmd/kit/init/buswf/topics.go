// Canonical PR-lifecycle bus topics emitted by generated workflows.
// All four pass `bus.ValidateTopic` (4 segments, lowercase, past-tense).
//
// The object segment distinguishes which entity on the PR changed:
//   - run     a CI workflow run
//   - comment a review comment
//   - pull    the PR itself (merge / close)
//
// See docs/contracts/kit-init-pr-wiring.md §2 and
// go/runtime/bus/topics.go for the validator.
package buswf

import "hop.top/kit/go/runtime/bus"

// PR-lifecycle topic constants. Keep these exported so unit tests in
// other packages can pin the strings and confirm them against
// ValidateTopic without importing internal helpers.
const (
	TopicRunCompleted   bus.Topic = "github.pr.run.completed"
	TopicCommentCreated bus.Topic = "github.pr.comment.created"
	TopicPullMerged     bus.Topic = "github.pr.pull.merged"
	TopicPullClosed     bus.Topic = "github.pr.pull.closed"
)

// Topics returns the four canonical PR-lifecycle topics. Used by tests
// (and by the manifest writer) to iterate without re-listing the names.
func Topics() []bus.Topic {
	return []bus.Topic{
		TopicRunCompleted,
		TopicCommentCreated,
		TopicPullMerged,
		TopicPullClosed,
	}
}
