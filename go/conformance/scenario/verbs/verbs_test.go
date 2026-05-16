package verbs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"hop.top/kit/go/conformance/scenario/judge"
	"hop.top/kit/go/conformance/scenario/verbs"
)

func mustEval(t *testing.T, kind string, args map[string]any, vctx verbs.VerbContext) verbs.EvalResult {
	t.Helper()
	e := verbs.Lookup(kind)
	if e == nil {
		t.Fatalf("verb %q not registered", kind)
	}
	if e.Evaluate == nil {
		t.Fatalf("verb %q has no Evaluate", kind)
	}
	return e.Evaluate(context.Background(), verbs.AssertionSpec{
		Kind: kind, Args: args,
	}, vctx)
}

func TestExitCodeEquals(t *testing.T) {
	r := mustEval(t, "exit_code_equals", map[string]any{"value": 0},
		verbs.VerbContext{Capture: verbs.Capture{ExitCode: 0}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s, want pass", r.Status)
	}
	r = mustEval(t, "exit_code_equals", map[string]any{"value": 0},
		verbs.VerbContext{Capture: verbs.Capture{ExitCode: 1}})
	if r.Status != verbs.StatusFail {
		t.Errorf("Status = %s, want fail", r.Status)
	}
}

func TestExitCodeIn(t *testing.T) {
	r := mustEval(t, "exit_code_in", map[string]any{"values": []any{0, 2}},
		verbs.VerbContext{Capture: verbs.Capture{ExitCode: 2}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s, want pass", r.Status)
	}
	r = mustEval(t, "exit_code_in", map[string]any{"values": []any{0, 2}},
		verbs.VerbContext{Capture: verbs.Capture{ExitCode: 1}})
	if r.Status != verbs.StatusFail {
		t.Errorf("Status = %s, want fail", r.Status)
	}
}

func TestExitCodeClass(t *testing.T) {
	r := mustEval(t, "exit_code_class", map[string]any{"classes": []any{"OK"}},
		verbs.VerbContext{Capture: verbs.Capture{ExitCode: 0}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s, want pass", r.Status)
	}
	r = mustEval(t, "exit_code_class", map[string]any{"classes": []any{"NOT_FOUND"}},
		verbs.VerbContext{Capture: verbs.Capture{ExitCode: 0}})
	if r.Status != verbs.StatusFail {
		t.Errorf("Status = %s, want fail", r.Status)
	}
}

func TestOutputFieldEquals(t *testing.T) {
	r := mustEval(t, "output_field_equals", map[string]any{
		"path": "$.summary", "value": "ok",
	}, verbs.VerbContext{Capture: verbs.Capture{Stdout: []byte(`{"summary":"ok"}`)}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s want pass", r.Status)
	}
	r = mustEval(t, "output_field_equals", map[string]any{
		"path": "$.summary", "value": "ok",
	}, verbs.VerbContext{Capture: verbs.Capture{Stdout: []byte(`{"summary":"no"}`)}})
	if r.Status != verbs.StatusFail {
		t.Errorf("Status = %s want fail", r.Status)
	}
}

func TestOutputFieldPresent(t *testing.T) {
	r := mustEval(t, "output_field_present", map[string]any{"path": "$.foo"},
		verbs.VerbContext{Capture: verbs.Capture{Stdout: []byte(`{"foo":1}`)}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s want pass", r.Status)
	}
	r = mustEval(t, "output_field_present", map[string]any{"path": "$.bar"},
		verbs.VerbContext{Capture: verbs.Capture{Stdout: []byte(`{"foo":1}`)}})
	if r.Status != verbs.StatusFail {
		t.Errorf("Status = %s want fail", r.Status)
	}
}

func TestOutputFieldCount(t *testing.T) {
	r := mustEval(t, "output_field_count", map[string]any{"path": "$.items", "equals": 3},
		verbs.VerbContext{Capture: verbs.Capture{Stdout: []byte(`{"items":[1,2,3]}`)}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s want pass", r.Status)
	}
}

func TestStderrContains(t *testing.T) {
	r := mustEval(t, "stderr_contains", map[string]any{"value": "warning"},
		verbs.VerbContext{Capture: verbs.Capture{Stderr: []byte("warning: low ink")}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s want pass", r.Status)
	}
	r = mustEval(t, "stderr_does_not_contain", map[string]any{"value": "PANIC"},
		verbs.VerbContext{Capture: verbs.Capture{Stderr: []byte("warning: low ink")}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s want pass", r.Status)
	}
}

func TestStreamDisciplinePass(t *testing.T) {
	// JSON stdout, free-form stderr ⇒ pass
	r := mustEval(t, "stream_discipline_pass", nil,
		verbs.VerbContext{Capture: verbs.Capture{
			Stdout: []byte(`{"ok":1}`),
			Stderr: []byte("informational"),
		}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s want pass", r.Status)
	}
	// Non-JSON stdout ⇒ fail
	r = mustEval(t, "stream_discipline_pass", nil,
		verbs.VerbContext{Capture: verbs.Capture{
			Stdout: []byte("not json"),
		}})
	if r.Status != verbs.StatusFail {
		t.Errorf("Status = %s want fail", r.Status)
	}
	// JSON stderr (when non-empty) ⇒ fail
	r = mustEval(t, "stream_discipline_pass", nil,
		verbs.VerbContext{Capture: verbs.Capture{
			Stdout: []byte(`{"ok":1}`),
			Stderr: []byte(`{"err":1}`),
		}})
	if r.Status != verbs.StatusFail {
		t.Errorf("Status = %s want fail", r.Status)
	}
}

func TestProvenancePresent(t *testing.T) {
	body := []byte(`{
		"data": {"x": 1},
		"provenance": {
			"/x": {"source": "cached", "url": "doc://x", "schema_version": "1"}
		}
	}`)
	r := mustEval(t, "provenance_present", nil,
		verbs.VerbContext{Capture: verbs.Capture{Stdout: body}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s want pass", r.Status)
	}

	// Missing envelope ⇒ fail
	r = mustEval(t, "provenance_present", nil,
		verbs.VerbContext{Capture: verbs.Capture{Stdout: []byte(`{"data":{"x":1}}`)}})
	if r.Status != verbs.StatusFail {
		t.Errorf("Status = %s want fail (no provenance key)", r.Status)
	}
}

func TestProvenanceMatchesCassette(t *testing.T) {
	dir := t.TempDir()
	// Write a tiny http cassette pair that matches doc://x via the
	// http url field. The diff loader is permissive about file names
	// (adapter-fp.{req,resp}.yaml); we use "http-abc".
	writeCassette(t, dir, "http-abc.req.yaml", `adapter: http
fingerprint: abc
payload:
  method: GET
  url: doc://x
`)
	writeCassette(t, dir, "http-abc.resp.yaml", `adapter: http
fingerprint: abc
payload:
  status: 200
`)
	body := []byte(`{
		"data": {"x": 1},
		"provenance": {
			"/x": {"source": "cached", "url": "doc://x", "schema_version": "1"}
		}
	}`)
	r := mustEval(t, "provenance_matches_cassette", nil,
		verbs.VerbContext{Capture: verbs.Capture{Stdout: body, CassetteDir: dir}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s want pass; msg=%s", r.Status, r.Message)
	}

	// Provenance URL not in cassette ⇒ fail
	body2 := []byte(`{
		"data": {"x": 1},
		"provenance": {
			"/x": {"source": "cached", "url": "doc://nowhere", "schema_version": "1"}
		}
	}`)
	r = mustEval(t, "provenance_matches_cassette", nil,
		verbs.VerbContext{Capture: verbs.Capture{Stdout: body2, CassetteDir: dir}})
	if r.Status != verbs.StatusFail {
		t.Errorf("Status = %s want fail; msg=%s", r.Status, r.Message)
	}
}

func writeCassette(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestCassetteMustContain(t *testing.T) {
	dir := t.TempDir()
	writeCassette(t, dir, "sql-abc.req.yaml", `adapter: sql
fingerprint: abc
payload:
  query: "INSERT INTO foo VALUES (1)"
`)
	writeCassette(t, dir, "sql-abc.resp.yaml", `adapter: sql
fingerprint: abc
payload:
  rows: 1
`)
	r := mustEval(t, "cassette_must_contain", map[string]any{
		"op_class": "mutating",
		"adapter":  "sql",
		"match":    map[string]any{"query_substring": "INSERT INTO foo"},
	}, verbs.VerbContext{Capture: verbs.Capture{CassetteDir: dir}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s want pass; msg=%s", r.Status, r.Message)
	}

	r = mustEval(t, "cassette_must_contain", map[string]any{
		"op_class": "mutating",
		"adapter":  "sql",
		"match":    map[string]any{"query_substring": "DROP TABLE"},
	}, verbs.VerbContext{Capture: verbs.Capture{CassetteDir: dir}})
	if r.Status != verbs.StatusFail {
		t.Errorf("Status = %s want fail; msg=%s", r.Status, r.Message)
	}

	r = mustEval(t, "cassette_must_not_contain", map[string]any{
		"op_class": "destructive",
	}, verbs.VerbContext{Capture: verbs.Capture{CassetteDir: dir}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s want pass (no destructive ops)", r.Status)
	}
}

func TestDryRunNoMutation(t *testing.T) {
	dir := t.TempDir()
	writeCassette(t, dir, "sql-r.req.yaml", `adapter: sql
fingerprint: r
payload:
  query: "SELECT * FROM foo"
`)
	writeCassette(t, dir, "sql-r.resp.yaml", `adapter: sql
fingerprint: r
payload:
  rows: 0
`)
	r := mustEval(t, "dry_run_no_mutation", nil,
		verbs.VerbContext{Capture: verbs.Capture{CassetteDir: dir}})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s want pass", r.Status)
	}

	// Add a mutating op ⇒ fail.
	writeCassette(t, dir, "sql-w.req.yaml", `adapter: sql
fingerprint: w
payload:
  query: "INSERT INTO foo VALUES (1)"
`)
	writeCassette(t, dir, "sql-w.resp.yaml", `adapter: sql
fingerprint: w
payload:
  rows: 1
`)
	r = mustEval(t, "dry_run_no_mutation", nil,
		verbs.VerbContext{Capture: verbs.Capture{CassetteDir: dir}})
	if r.Status != verbs.StatusFail {
		t.Errorf("Status = %s want fail", r.Status)
	}
}

func TestAuthLifecycleClean_NotImplemented(t *testing.T) {
	e := verbs.Lookup("auth_lifecycle_clean")
	if e == nil {
		t.Fatalf("auth_lifecycle_clean not registered")
	}
	if e.Evaluate != nil {
		t.Errorf("auth_lifecycle_clean.Evaluate must be nil in v1 (parsed-but-not-implemented)")
	}
}

func TestJudgeScoreAbove_Canned(t *testing.T) {
	jb := &verbs.JudgeBlockSpec{
		ID:             "j",
		Model:          "stub",
		ModelAllowlist: []string{"stub"},
		Prompt:         "score it",
	}
	stub := judge.NewCanned(map[string]float64{"j": 0.8})
	r := mustEval(t, "judge_score_above", map[string]any{
		"judge_id": "j", "value": 0.5,
	}, verbs.VerbContext{
		Capture:    verbs.Capture{Stdout: []byte("the body")},
		Judge:      stub,
		JudgeBlock: jb,
	})
	if r.Status != verbs.StatusPass {
		t.Errorf("Status = %s want pass; msg=%s", r.Status, r.Message)
	}
	if r.JudgeTrace == nil || r.JudgeTrace.Score != 0.8 {
		t.Errorf("expected judge trace with score 0.8; got %+v", r.JudgeTrace)
	}
}

func TestJudgeScoreAbove_ModelRejected(t *testing.T) {
	jb := &verbs.JudgeBlockSpec{
		ID:             "j",
		Model:          "gpt-9000", // not in allowlist
		ModelAllowlist: []string{"stub"},
		Prompt:         "x",
	}
	stub := judge.NewCanned(map[string]float64{"j": 0.8})
	r := mustEval(t, "judge_score_above", map[string]any{
		"judge_id": "j", "value": 0.5,
	}, verbs.VerbContext{
		Judge:      stub,
		JudgeBlock: jb,
	})
	if r.Status != verbs.StatusUngradable {
		t.Errorf("Status = %s want ungradable (model rejected)", r.Status)
	}
}
