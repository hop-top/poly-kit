package dek_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/dek"
)

// scriptExec answers each call through fn and records the requests.
type scriptExec struct {
	mu  sync.Mutex
	got []api.CommandRequest
	fn  func(n int, req api.CommandRequest) (api.CommandResult, error)
}

func (s *scriptExec) Execute(ctx context.Context, req api.CommandRequest) (api.CommandResult, error) {
	s.mu.Lock()
	n := len(s.got)
	s.got = append(s.got, req)
	s.mu.Unlock()
	return s.fn(n, req)
}

func (s *scriptExec) calls() []api.CommandRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]api.CommandRequest(nil), s.got...)
}

func isDry(r api.CommandRequest) bool { return r.Flags["dry-run"] == true }

// deleteExec plans "delete item 7" (with a timestamp that differs per
// call, as cli.Plan's generated_at does) and deletes for real.
func deleteExec(planEffect func() string) *scriptExec {
	n := 0
	return &scriptExec{fn: func(_ int, r api.CommandRequest) (api.CommandResult, error) {
		if isDry(r) {
			n++
			return api.CommandResult{Data: map[string]any{
				"effects": []any{planEffect()}, "generated_at": time.Now().String() + string(rune('a'+n)),
			}}, nil
		}
		return api.CommandResult{Data: map[string]any{"deleted": "7"}}, nil
	}}
}

// authCore is a core behind bearer auth: the token is the subject.
func authCore(t *testing.T, ex api.CommandExecutor, opts ...func(*dek.Config)) *httptest.Server {
	t.Helper()
	cfg := dek.Config{Projection: api.ProjectionConfig{
		ToolName: "acme", Descriptors: descriptors(), Executor: ex,
	}, CoreID: "c"}
	for _, o := range opts {
		o(&cfg)
	}
	core, err := dek.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	verify := func(r *http.Request) (any, error) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if tok == "" || tok == r.Header.Get("Authorization") {
			return nil, http.ErrNoCookie
		}
		return api.Claims{Subject: tok, Tenant: "acme"}, nil
	}
	exempt := func(next http.Handler) http.Handler {
		authed := api.Auth(verify)(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Path == dek.Prefix+"/hello" {
				next.ServeHTTP(w, r)
				return
			}
			authed.ServeHTTP(w, r)
		})
	}
	rt := api.NewRouter(api.WithOuterMiddleware(exempt))
	core.Mount(rt)
	srv := httptest.NewServer(rt)
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, method, url, token, body string, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return do(t, req)
}

func planOf(t *testing.T, base, token, key string) string {
	t.Helper()
	res, body := call(t, "POST", base+"/v1/dek/invoke", token,
		`{"capability":"item.delete","args":{"id":"7"},"dry_run":true,"idempotency_key":"`+key+`"}`,
		"Idempotency-Key", key)
	if res.StatusCode != 200 {
		t.Fatalf("plan: %d %s", res.StatusCode, body)
	}
	var out struct {
		Body struct {
			ID string `json:"id"`
		} `json:"body"`
	}
	_ = json.Unmarshal(body, &out)
	return out.Body.ID
}

func TestWhoami(t *testing.T) {
	srv := authCore(t, &scriptExec{})
	res, body := call(t, "GET", srv.URL+"/v1/dek/whoami", "alice", "")
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	validate(t, "Whoami", body)
	var w struct{ ID, Name, Tenant string }
	_ = json.Unmarshal(body, &w)
	if !strings.EqualFold(w.ID, "alice") || w.Tenant != "acme" {
		t.Fatalf("whoami %s", body)
	}
	res, _ = call(t, "GET", srv.URL+"/v1/dek/whoami", "", "")
	if res.StatusCode != 401 {
		t.Fatalf("whoami without a bearer: %d", res.StatusCode)
	}
}

func TestWhoami_UnauthenticatedListenerIsLocalPrincipal(t *testing.T) {
	srv, _ := newCore(t, &fakeExec{})
	res, body := get(t, srv.URL+"/v1/dek/whoami")
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	validate(t, "Whoami", body)
	if !strings.Contains(string(body), `"tenant":"local"`) {
		t.Fatalf("tenant must never be empty: %s", body)
	}
}

func TestHello_NeedsNoCredential(t *testing.T) {
	srv := authCore(t, &scriptExec{})
	res, body := call(t, "GET", srv.URL+"/v1/dek/hello", "", "")
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	res, _ = call(t, "GET", srv.URL+"/v1/dek/capabilities", "", "")
	if res.StatusCode != 401 {
		t.Fatalf("capabilities need the bearer: %d", res.StatusCode)
	}
}

func TestApply_RunsAfterFreshDryRunAndSkipsTimestamps(t *testing.T) {
	ex := deleteExec(func() string { return "delete item 7" })
	srv := authCore(t, ex)
	id := planOf(t, srv.URL, "alice", "k1")

	res, body := call(t, "POST", srv.URL+"/v1/dek/plans/"+id+"/apply", "alice", "", "Idempotency-Key", "k1")
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	validate(t, "ApplyResponse", body)
	if !strings.Contains(string(body), `"kind":"applied"`) || !strings.Contains(string(body), `"deleted":"7"`) {
		t.Fatalf("%s", body)
	}
	calls := ex.calls()
	if len(calls) != 3 || !isDry(calls[0]) || !isDry(calls[1]) || isDry(calls[2]) {
		t.Fatalf("want plan, fresh plan, real run: %+v", calls)
	}
	real := calls[2]
	if real.Flags["confirm"] != "yes" {
		t.Fatalf("an applied plan is the confirmation: %+v", real.Flags)
	}
	if real.Meta.IdempotencyKey != "k1" || real.Args[0] != "7" {
		t.Fatalf("real run: %+v", real)
	}
	if calls[0].Meta.IdempotencyKey != "" || calls[1].Meta.IdempotencyKey != "" {
		t.Fatal("a dry run must not carry the idempotency key: a replaying executor would answer the real run with the plan")
	}
}

func TestApply_Drift(t *testing.T) {
	n := 0
	ex := deleteExec(func() string { n++; return map[int]string{1: "delete item 7", 2: "delete items 7 and 8"}[n] })
	srv := authCore(t, ex)
	id := planOf(t, srv.URL, "alice", "k1")

	res, body := call(t, "POST", srv.URL+"/v1/dek/plans/"+id+"/apply", "alice", "")
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	validate(t, "ApplyResponse", body)
	var out struct {
		Kind string `json:"kind"`
		Body struct {
			Fresh struct {
				ID string `json:"id"`
			} `json:"fresh"`
			Diff any `json:"diff"`
		} `json:"body"`
	}
	_ = json.Unmarshal(body, &out)
	if out.Kind != "drifted" || out.Body.Fresh.ID == "" || out.Body.Fresh.ID == id || out.Body.Diff == nil {
		t.Fatalf("%s", body)
	}
	for _, c := range ex.calls() {
		if !isDry(c) {
			t.Fatal("a drifted apply must execute nothing")
		}
	}
}

func TestApply_IdempotencyAndSingleUse(t *testing.T) {
	ex := deleteExec(func() string { return "delete item 7" })
	srv := authCore(t, ex)
	a := planOf(t, srv.URL, "alice", "ka")
	b := planOf(t, srv.URL, "alice", "kb")
	url := func(id string) string { return srv.URL + "/v1/dek/plans/" + id + "/apply" }

	_, first := call(t, "POST", url(a), "alice", "")
	before := len(ex.calls())
	res, replay := call(t, "POST", url(a), "alice", "")
	if res.StatusCode != 200 || string(replay) != string(first) || len(ex.calls()) != before {
		t.Fatalf("same key must replay without running: %d %s vs %s", res.StatusCode, replay, first)
	}

	res, body := call(t, "POST", url(a), "alice", "", "Idempotency-Key", "other")
	if res.StatusCode != 200 || !strings.Contains(string(body), `"kind":"blocked"`) {
		t.Fatalf("an applied plan applies once: %d %s", res.StatusCode, body)
	}
	validate(t, "ApplyResponse", body)

	res, body = call(t, "POST", url(b), "alice", "", "Idempotency-Key", "ka")
	if res.StatusCode != 409 || errReason(t, body) != "idempotency key used on another plan" {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
}

func TestApply_Refusals(t *testing.T) {
	ex := deleteExec(func() string { return "delete item 7" })
	srv := authCore(t, ex)
	id := planOf(t, srv.URL, "alice", "k1")
	url := srv.URL + "/v1/dek/plans/" + id + "/apply"

	if res, _ := call(t, "POST", srv.URL+"/v1/dek/plans/plan_nope/apply", "alice", ""); res.StatusCode != 404 {
		t.Fatalf("unknown plan: %d", res.StatusCode)
	}
	if res, _ := call(t, "POST", url, "mallory", ""); res.StatusCode != 404 {
		t.Fatalf("another principal's plan must not exist for me: %d", res.StatusCode)
	}
	res, body := call(t, "POST", url, "alice", `{"min_approvals":2}`)
	if res.StatusCode != 422 || !strings.Contains(errReason(t, body), "min_approvals") {
		t.Fatalf("a minimum this core cannot honour must be refused: %d %s", res.StatusCode, body)
	}
	if res, _ := call(t, "POST", url, "", ""); res.StatusCode != 401 {
		t.Fatalf("apply needs the bearer: %d", res.StatusCode)
	}
	for _, c := range ex.calls() {
		if !isDry(c) {
			t.Fatal("a refused apply must not run the command")
		}
	}
}

func TestApply_ClassesThisCoreCannotEnforceAreBlocked(t *testing.T) {
	ex := deleteExec(func() string { return "delete item 7" })
	srv := authCore(t, ex, func(c *dek.Config) {
		c.Gate = func(d api.CommandDescriptor) string {
			if d.SideEffect == api.SideEffectDestructive {
				return "review"
			}
			return ""
		}
	})
	id := planOf(t, srv.URL, "alice", "k1")
	res, body := call(t, "POST", srv.URL+"/v1/dek/plans/"+id+"/apply", "alice", "")
	if res.StatusCode != 200 || !strings.Contains(string(body), `"kind":"blocked"`) {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	validate(t, "ApplyResponse", body)
	for _, c := range ex.calls() {
		if !isDry(c) {
			t.Fatal("blocked must not run")
		}
	}
}

func TestApply_ConfirmTokenIsBlocked(t *testing.T) {
	ex := deleteExec(func() string { return "delete item 7" })
	descs := descriptors()
	for i := range descs {
		if descs[i].PathKey() == "item delete" {
			descs[i].RequiresConfirmToken = true
		}
	}
	srv := authCore(t, ex, func(c *dek.Config) { c.Projection.Descriptors = descs })
	id := planOf(t, srv.URL, "alice", "k1")
	_, body := call(t, "POST", srv.URL+"/v1/dek/plans/"+id+"/apply", "alice", "")
	if !strings.Contains(string(body), `"kind":"blocked"`) {
		t.Fatalf("a typed confirm token cannot be given by an apply: %s", body)
	}
}

// countingStore wraps the default store to prove the interface is the seam.
type countingStore struct {
	dek.PlanStore
	puts int
}

func (c *countingStore) Put(ctx context.Context, p dek.StoredPlan) error {
	c.puts++
	return c.PlanStore.Put(ctx, p)
}

func TestApply_PlanStoreIsReplaceable(t *testing.T) {
	cs := &countingStore{PlanStore: dek.NewMemoryPlanStore()}
	srv := authCore(t, deleteExec(func() string { return "x" }), func(c *dek.Config) { c.Plans = cs })
	id := planOf(t, srv.URL, "alice", "k1")
	call(t, "POST", srv.URL+"/v1/dek/plans/"+id+"/apply", "alice", "")
	if cs.puts < 2 {
		t.Fatalf("plan and outcome must both go through the store, puts=%d", cs.puts)
	}
}

func TestJobs_LongRunningAnswersAccepted(t *testing.T) {
	release := make(chan struct{})
	ex := &scriptExec{fn: func(_ int, r api.CommandRequest) (api.CommandResult, error) {
		<-release
		return api.CommandResult{Data: map[string]any{"imported": 3}}, nil
	}}
	srv := authCore(t, ex)
	res, body := call(t, "POST", srv.URL+"/v1/dek/invoke", "alice",
		`{"capability":"item.import","args":{"src":"a.csv"},"dry_run":false,"idempotency_key":"j1"}`)
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	validate(t, "InvokeResponse", body)
	var acc struct {
		Kind string `json:"kind"`
		Body struct {
			JobID string `json:"job_id"`
		} `json:"body"`
	}
	_ = json.Unmarshal(body, &acc)
	if acc.Kind != "accepted" || acc.Body.JobID == "" {
		t.Fatalf("%s", body)
	}

	// Running: listed as active, readable by id, invisible to others.
	res, list := call(t, "GET", srv.URL+"/v1/dek/jobs", "alice", "")
	var jobs []map[string]any
	_ = json.Unmarshal(list, &jobs)
	if res.StatusCode != 200 || len(jobs) != 1 {
		t.Fatalf("active jobs: %d %s", res.StatusCode, list)
	}
	validate(t, "Job", mustJSON(jobs[0]))
	if jobs[0]["kind"] != "item.import" || (jobs[0]["status"] != "running" && jobs[0]["status"] != "queued") {
		t.Fatalf("%v", jobs[0])
	}
	if res, _ := call(t, "GET", srv.URL+"/v1/dek/jobs/"+acc.Body.JobID, "mallory", ""); res.StatusCode != 404 {
		t.Fatalf("another principal's job: %d", res.StatusCode)
	}
	if _, l := call(t, "GET", srv.URL+"/v1/dek/jobs", "mallory", ""); strings.TrimSpace(string(l)) != "[]" {
		t.Fatalf("mallory sees %s", l)
	}

	close(release)
	var final map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, b := call(t, "GET", srv.URL+"/v1/dek/jobs/"+acc.Body.JobID, "alice", "")
		_ = json.Unmarshal(b, &final)
		if final["status"] == "succeeded" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final["status"] != "succeeded" {
		t.Fatalf("job never finished: %v", final)
	}
	validate(t, "Job", mustJSON(final))
	if !strings.Contains(string(mustJSON(final["outcome"])), "imported") {
		t.Fatalf("outcome: %v", final)
	}
	_, l := call(t, "GET", srv.URL+"/v1/dek/jobs", "alice", "")
	if strings.TrimSpace(string(l)) != "[]" {
		t.Fatalf("a finished job leaves the active list: %s", l)
	}
}

func TestJobs_FailureIsRecorded(t *testing.T) {
	ex := &scriptExec{fn: func(int, api.CommandRequest) (api.CommandResult, error) {
		return api.CommandResult{ExitCode: 1, Stderr: "disk full"}, nil
	}}
	srv := authCore(t, ex)
	_, body := call(t, "POST", srv.URL+"/v1/dek/invoke", "alice",
		`{"capability":"item.import","args":{},"dry_run":false,"idempotency_key":"j2"}`)
	var acc struct {
		Body struct {
			JobID string `json:"job_id"`
		} `json:"body"`
	}
	_ = json.Unmarshal(body, &acc)
	var j map[string]any
	for i := 0; i < 300 && j["status"] != "failed"; i++ {
		_, b := call(t, "GET", srv.URL+"/v1/dek/jobs/"+acc.Body.JobID, "alice", "")
		_ = json.Unmarshal(b, &j)
		time.Sleep(10 * time.Millisecond)
	}
	if j["status"] != "failed" || !strings.Contains(string(mustJSON(j["outcome"])), "disk full") {
		t.Fatalf("%v", j)
	}
	validate(t, "Job", mustJSON(j))
}

func TestJobs_DryRunOfLongRunningIsStillAPlan(t *testing.T) {
	ex := &scriptExec{fn: func(int, api.CommandRequest) (api.CommandResult, error) {
		return api.CommandResult{Data: "would import"}, nil
	}}
	srv := authCore(t, ex)
	_, body := call(t, "POST", srv.URL+"/v1/dek/invoke", "alice",
		`{"capability":"item.import","args":{},"dry_run":true,"idempotency_key":"j3"}`)
	if !strings.Contains(string(body), `"kind":"plan"`) {
		t.Fatalf("%s", body)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
