package buswf_test

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"hop.top/kit/cmd/kit/init/buswf"
)

// TestFilesParseAsYAML asserts every generated workflow body parses as
// valid YAML. We do not assert the full document tree shape here —
// that's covered by the per-file structural tests below.
func TestFilesParseAsYAML(t *testing.T) {
	t.Parallel()
	for _, f := range buswf.Files() {
		f := f
		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()
			var node yaml.Node
			if err := yaml.Unmarshal(f.Body, &node); err != nil {
				t.Fatalf("parse %s: %v\n--- body ---\n%s", f.Name, err, string(f.Body))
			}
		})
	}
}

// TestFilesHaveBusGating asserts every generated workflow gates the
// emit job on KIT_BUS_ENABLED + KIT_BUS_INGRESS_URL. This is the
// "disabled by default" behavior pinned in spec §3.
func TestFilesHaveBusGating(t *testing.T) {
	t.Parallel()
	for _, f := range buswf.Files() {
		body := string(f.Body)
		if !strings.Contains(body, "vars.KIT_BUS_ENABLED == 'true'") {
			t.Errorf("%s: missing KIT_BUS_ENABLED gating", f.Name)
		}
		if !strings.Contains(body, "vars.KIT_BUS_INGRESS_URL != ''") {
			t.Errorf("%s: missing KIT_BUS_INGRESS_URL gating", f.Name)
		}
	}
}

// TestFilesCarryAuthAndStrictEnv asserts every workflow forwards
// KIT_BUS_TOKEN, KIT_BUS_SIGNING_KEY, and KIT_BUS_STRICT to the helper
// binary (spec §3 auth + strict-mode env).
func TestFilesCarryAuthAndStrictEnv(t *testing.T) {
	t.Parallel()
	required := []string{
		"KIT_BUS_INGRESS_URL: ${{ vars.KIT_BUS_INGRESS_URL }}",
		"KIT_BUS_TOKEN: ${{ secrets.KIT_BUS_TOKEN }}",
		"KIT_BUS_SIGNING_KEY: ${{ secrets.KIT_BUS_SIGNING_KEY }}",
		"KIT_BUS_STRICT: ${{ vars.KIT_BUS_STRICT }}",
	}
	for _, f := range buswf.Files() {
		body := string(f.Body)
		for _, want := range required {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing env mapping %q", f.Name, want)
			}
		}
	}
}

// TestFilesAreDeterministic ensures repeated Files() calls produce
// byte-identical output. Determinism is load-bearing for the manifest
// hash policy (spec §6).
func TestFilesAreDeterministic(t *testing.T) {
	t.Parallel()
	a := buswf.Files()
	b := buswf.Files()
	if len(a) != len(b) {
		t.Fatalf("Files(): different lengths between calls: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if string(a[i].Body) != string(b[i].Body) {
			t.Errorf("Files()[%d] %s: body differs between invocations", i, a[i].Name)
		}
	}
}

// TestRunCompletedTrigger checks the run.completed workflow triggers on
// workflow_run + completed.
func TestRunCompletedTrigger(t *testing.T) {
	t.Parallel()
	f, ok := buswf.FileByTopic("github.pr.run.completed")
	if !ok {
		t.Fatal("FileByTopic(run.completed): not found")
	}
	body := string(f.Body)
	if !strings.Contains(body, "workflow_run:") {
		t.Error("missing workflow_run trigger")
	}
	if !strings.Contains(body, "types: [completed]") {
		t.Error("missing types: [completed]")
	}
}

// TestRunCompletedTriggerNoWildcardWorkflows asserts the workflow_run
// trigger does NOT use a literal "*" string as a workflow name. GitHub
// Actions has no wildcard syntax in workflow_run.workflows; the string
// "*" would be treated as a literal workflow name and the workflow
// would never fire. See Comment 3293191431.
func TestRunCompletedTriggerNoWildcardWorkflows(t *testing.T) {
	t.Parallel()
	f, ok := buswf.FileByTopic("github.pr.run.completed")
	if !ok {
		t.Fatal("FileByTopic(run.completed): not found")
	}
	body := string(f.Body)
	// Parse the YAML so we look at the structured value of
	// on.workflow_run.workflows, not just a substring match.
	var doc struct {
		On struct {
			WorkflowRun struct {
				Workflows []string `yaml:"workflows"`
				Types     []string `yaml:"types"`
			} `yaml:"workflow_run"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parse: %v\n--- body ---\n%s", err, body)
	}
	if len(doc.On.WorkflowRun.Workflows) == 0 {
		t.Fatal("workflow_run.workflows is empty; expected explicit caller workflow names")
	}
	for _, w := range doc.On.WorkflowRun.Workflows {
		if w == "*" {
			t.Errorf("workflow_run.workflows contains literal %q; GitHub Actions has no wildcard, this never fires", w)
		}
	}
}

// TestCommentCreatedTrigger checks the comment.created workflow uses
// pull_request_review_comment + created.
func TestCommentCreatedTrigger(t *testing.T) {
	t.Parallel()
	f, ok := buswf.FileByTopic("github.pr.comment.created")
	if !ok {
		t.Fatal("FileByTopic(comment.created): not found")
	}
	body := string(f.Body)
	if !strings.Contains(body, "pull_request_review_comment:") {
		t.Error("missing pull_request_review_comment trigger")
	}
	if !strings.Contains(body, "types: [created]") {
		t.Error("missing types: [created]")
	}
}

// TestPullMergedTrigger pins the merged-true guard.
func TestPullMergedTrigger(t *testing.T) {
	t.Parallel()
	f, ok := buswf.FileByTopic("github.pr.pull.merged")
	if !ok {
		t.Fatal("FileByTopic(pull.merged): not found")
	}
	body := string(f.Body)
	if !strings.Contains(body, "pull_request:") {
		t.Error("missing pull_request trigger")
	}
	if !strings.Contains(body, "github.event.pull_request.merged == true") {
		t.Error("missing merged==true guard")
	}
}

// TestPullClosedTrigger pins the merged-false guard. This is what
// distinguishes the close-without-merge workflow from the merge one,
// both of which fire on `pull_request closed`.
func TestPullClosedTrigger(t *testing.T) {
	t.Parallel()
	f, ok := buswf.FileByTopic("github.pr.pull.closed")
	if !ok {
		t.Fatal("FileByTopic(pull.closed): not found")
	}
	body := string(f.Body)
	if !strings.Contains(body, "pull_request:") {
		t.Error("missing pull_request trigger")
	}
	if !strings.Contains(body, "github.event.pull_request.merged == false") {
		t.Error("missing merged==false guard (distinguishes from pull.merged)")
	}
}

// TestFilesEmbedTopicInRunCmd asserts each workflow invokes
// kit-bus-emit with the correct --kind that maps back to its canonical
// topic.
func TestFilesEmbedTopicInRunCmd(t *testing.T) {
	t.Parallel()
	cases := []struct {
		topic string
		flag  string
	}{
		{"github.pr.run.completed", "--kind run.completed"},
		{"github.pr.comment.created", "--kind comment.created"},
		{"github.pr.pull.merged", "--kind pull.merged"},
		{"github.pr.pull.closed", "--kind pull.closed"},
	}
	for _, c := range cases {
		f, ok := buswf.FileByTopic(c.topic)
		if !ok {
			t.Errorf("FileByTopic(%s): not found", c.topic)
			continue
		}
		if !strings.Contains(string(f.Body), c.flag) {
			t.Errorf("%s: missing %q in run block", f.Name, c.flag)
		}
	}
}

// TestFilesDoNotEmbedFullLogsOrBodies guards spec §2's "no full logs,
// no full PR bodies, no full comment bodies" rule. The workflow embeds
// URLs, never the body text itself. The truncation lives in
// kit-bus-emit; the workflow forwards env vars but those are bounded
// at the emitter, not here.
//
// We assert the run.completed workflow doesn't ${{ ... }} a
// `.logs` or `.body` field directly; it only exposes URLs.
func TestFilesDoNotEmbedFullLogsOrBodies(t *testing.T) {
	t.Parallel()
	runWf, _ := buswf.FileByTopic("github.pr.run.completed")
	// run.completed must NOT carry github.event.workflow_run.logs
	// (only logs_url).
	if strings.Contains(string(runWf.Body), "github.event.workflow_run.logs }}") {
		t.Error("run.completed embeds raw logs (should only forward logs_url)")
	}

	commentWf, _ := buswf.FileByTopic("github.pr.comment.created")
	// The comment workflow forwards body via KIT_BUS_COMMENT_BODY
	// because the helper truncates to 256 bytes; we accept that
	// forwarding but the helper test guarantees truncation. Pin
	// the body env so a future refactor that moves truncation to
	// the workflow re-runs this test.
	if !strings.Contains(string(commentWf.Body), "KIT_BUS_COMMENT_BODY") {
		t.Error("comment.created: KIT_BUS_COMMENT_BODY missing (helper truncates)")
	}
}
