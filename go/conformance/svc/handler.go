package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/api"
)

// Service binds the runtime dependencies a request handler needs.
type Service struct {
	Store     ScenarioStore
	Claims    ClaimStore
	Grader    ScenarioGrader
	Limiter   RateLimiter
	Judges    ModelRegistry
	Receiver  *CassetteReceiver
	StartedAt time.Time
	// GraderVersion is echoed in the response envelope. When scen
	// merges, this becomes scenario.GraderVersion.
	GraderVersion string
}

// NewService constructs a Service with reasonable defaults.
func NewService(store ScenarioStore, claims ClaimStore, grader ScenarioGrader) *Service {
	return &Service{
		Store:         store,
		Claims:        claims,
		Grader:        grader,
		Limiter:       NewMemoryRateLimiter(func(id string) RateQuota { return rateFromClaim(claims, id) }),
		Judges:        NullRegistry{},
		Receiver:      &CassetteReceiver{},
		StartedAt:     time.Now().UTC(),
		GraderVersion: "0.0.0-stub",
	}
}

// rateFromClaim is a helper that resolves the RateQuota for a claim by
// ID. If the claim store lookup fails (revoked claim, etc.), fall back
// to DefaultQuota so the limiter still does something sensible.
func rateFromClaim(claims ClaimStore, id string) RateQuota {
	if claims == nil {
		return DefaultQuota
	}
	c, err := claims.LookupByID(context.Background(), id)
	if err != nil || c == nil {
		return DefaultQuota
	}
	return c.RateQuota
}

// Mount wires the v1 routes onto an api.Router. Auth and rate limit
// middleware are layered for the protected routes; /healthz, /readyz,
// /v1/capabilities are public.
func (s *Service) Mount(router *api.Router) {
	authed := router.Group("", Auth(s.Claims), RateLimit(s.Limiter))

	authed.Handle("POST", "/v1/grade", s.handleGrade)
	authed.Handle("POST", "/v1/run-and-grade", s.handleRunAndGrade)
	authed.Handle("GET", "/v1/scenarios", s.handleScenariosList)
	authed.Handle("GET", "/v1/scenarios/{ns}/{id}", s.handleScenarioGet)
	authed.Handle("GET", "/v1/scenarios/{ns}/{id}/meta", s.handleScenarioMeta)
	authed.Handle("GET", "/v1/scenarios/{ns}/{id}/versions", s.handleScenarioVersions)

	router.Handle("GET", "/v1/capabilities", s.handleCapabilities)
	router.Handle("GET", "/healthz", s.handleHealth)
	router.Handle("GET", "/readyz", s.handleReady)
}

// handleGrade implements POST /v1/grade per design §3.
func (s *Service) handleGrade(w http.ResponseWriter, r *http.Request) {
	rid := api.GetRequestID(r)
	claim := ClaimFromContext(r.Context())
	if claim == nil {
		WriteError(w, SvcError(CodeMissingBearer, "no claim on context", "", ""), rid)
		return
	}

	// Header parse.
	refRaw := r.Header.Get("X-Kit-Scenario-Ref")
	ref, err := ParseScenarioRef(refRaw)
	if err != nil {
		WriteError(w, SvcError(CodeScenarioRefMalformed, err.Error(), "",
			"send X-Kit-Scenario-Ref: <ns>/<id>[@<version>]"), rid)
		return
	}

	tier := 1
	if t := r.Header.Get("X-Kit-Tier"); t != "" {
		n, err := strconv.Atoi(t)
		if err != nil || n < 1 || n > 3 {
			WriteError(w, SvcError(CodeTierInvalid, fmt.Sprintf("invalid tier %q", t), "",
				"X-Kit-Tier must be 1, 2, or 3"), rid)
			return
		}
		tier = n
	}

	// Authz: scope check.
	required := "grade:" + ref.Namespace
	if !claim.HasScope(required) && !claim.HasScope("grade:*") {
		WriteError(w, SvcError(CodeScopeDenied,
			fmt.Sprintf("scope %q required", required),
			"", "request a token with the right grade:<ns> scope"), rid)
		return
	}
	if tier > claim.TierMax {
		WriteError(w, SvcError(CodeTierExceedsClaim,
			fmt.Sprintf("requested tier %d > claim cap %d", tier, claim.TierMax),
			"", "request a token with a higher tier_max"), rid)
		return
	}

	// Body decode.
	cas, err := s.Receiver.ReceiveHTTP(w, r)
	if err != nil {
		code := errCodeFromErr(err)
		WriteError(w, SvcError(code, err.Error(), "", ""), rid)
		return
	}
	defer func() { _ = cas.Close() }()

	if cas.UncompressedSz > effectiveSoftCap(s.Receiver) {
		w.Header().Set("X-Kit-Cassette-Size-Warning", strconv.FormatInt(cas.UncompressedSz, 10))
	}

	// Scenario load.
	sc, err := s.Store.Get(r.Context(), ref)
	if err != nil {
		WriteError(w, SvcError(CodeScenarioNotFound,
			fmt.Sprintf("scenario %s not found", ref), "",
			"verify ns/id/version and operator visibility"), rid)
		return
	}

	// Build the GradeInput and dispatch.
	captures := make(map[string]Capture, len(cas.Steps))
	for id, st := range cas.Steps {
		captures[id] = Capture{
			ExitCode:    st.ExitCode,
			DurationMS:  st.DurationMS,
			Stdout:      st.Stdout,
			Stderr:      st.Stderr,
			CassetteDir: st.CassetteDir,
		}
	}
	resolver := func(p string) (string, error) { return s.Store.Prompt(r.Context(), ref, p) }
	in := GradeInput{
		Scenario:       sc,
		StoryContent:   cas.StoryBytes,
		StepCaptures:   captures,
		Judge:          &registryJudge{reg: s.Judges, claim: claim},
		PromptResolver: resolver,
		Tier:           tier,
		RequestedAt:    time.Now().UTC(),
	}

	res, gerr := s.Grader.Grade(r.Context(), in)
	if gerr != nil {
		WriteError(w, SvcError(CodeGraderInternal, gerr.Error(), "", ""), rid)
		return
	}
	if res == nil {
		WriteError(w, SvcError(CodeGraderInternal, "grader returned nil result", "", ""), rid)
		return
	}
	// Tier truncate.
	res = res.ToTier(tier)
	if res.GraderVersion == "" {
		res.GraderVersion = s.GraderVersion
	}

	resp := GradeResponse{
		Result: res,
		Service: ServiceMeta{
			Version:   Version,
			RequestID: rid,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// GradeResponse wraps Result + service metadata per design §4.
type GradeResponse struct {
	Result  *Result     `json:"result"`
	Service ServiceMeta `json:"service"`
}

// ServiceMeta carries service-side audit fields surfaced to clients.
type ServiceMeta struct {
	Version   string `json:"version"`
	RequestID string `json:"request_id,omitempty"`
}

// handleRunAndGrade returns 501 per design §13.
func (s *Service) handleRunAndGrade(w http.ResponseWriter, r *http.Request) {
	rid := api.GetRequestID(r)
	WriteError(w, SvcError(CodeL4BNotImplemented,
		"server-side binary execution is not available in this kit version",
		"", "use POST /v1/grade with a pre-recorded cassette"), rid)
}

// handleScenariosList implements GET /v1/scenarios.
func (s *Service) handleScenariosList(w http.ResponseWriter, r *http.Request) {
	rid := api.GetRequestID(r)
	claim := ClaimFromContext(r.Context())
	if claim == nil {
		WriteError(w, SvcError(CodeMissingBearer, "no claim on context", "", ""), rid)
		return
	}
	all, err := s.Store.Namespaces(r.Context())
	if err != nil {
		WriteError(w, SvcError(CodeSvcInternal, err.Error(), "", ""), rid)
		return
	}
	out := struct {
		Scenarios []ScenarioMeta `json:"scenarios"`
	}{}
	for _, ns := range all {
		if !claim.HasScope("meta:"+ns) && !claim.HasScope("meta:*") &&
			!claim.HasScope("grade:"+ns) && !claim.HasScope("grade:*") &&
			!claim.HasScope("list:all") {
			continue
		}
		metas, _ := s.Store.List(r.Context(), ns)
		out.Scenarios = append(out.Scenarios, metas...)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleScenarioGet implements GET /v1/scenarios/{ns}/{id}.
func (s *Service) handleScenarioGet(w http.ResponseWriter, r *http.Request) {
	s.handleScenarioMeta(w, r) // same shape for v1
}

// handleScenarioMeta implements GET /v1/scenarios/{ns}/{id}/meta.
func (s *Service) handleScenarioMeta(w http.ResponseWriter, r *http.Request) {
	rid := api.GetRequestID(r)
	claim := ClaimFromContext(r.Context())
	if claim == nil {
		WriteError(w, SvcError(CodeMissingBearer, "no claim on context", "", ""), rid)
		return
	}
	ns := api.PathParam(r, "ns")
	id := api.PathParam(r, "id")
	ver := r.URL.Query().Get("version")
	ref := ScenarioRef{Namespace: ns, ID: id, Version: ver}
	if !ValidNamespace(ns) || !ValidID(id) {
		WriteError(w, SvcError(CodeScenarioRefMalformed,
			fmt.Sprintf("invalid ref %s/%s", ns, id), "", ""), rid)
		return
	}
	if !claim.HasScope("meta:"+ns) && !claim.HasScope("meta:*") &&
		!claim.HasScope("grade:"+ns) && !claim.HasScope("grade:*") {
		WriteError(w, SvcError(CodeScopeDenied,
			fmt.Sprintf("scope meta:%s or grade:%s required", ns, ns), "", ""), rid)
		return
	}
	meta, err := s.Store.Meta(r.Context(), ref)
	if err != nil {
		WriteError(w, SvcError(CodeScenarioNotFound,
			fmt.Sprintf("scenario %s not found", ref), "", ""), rid)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

// handleScenarioVersions implements GET /v1/scenarios/{ns}/{id}/versions.
func (s *Service) handleScenarioVersions(w http.ResponseWriter, r *http.Request) {
	rid := api.GetRequestID(r)
	claim := ClaimFromContext(r.Context())
	if claim == nil {
		WriteError(w, SvcError(CodeMissingBearer, "no claim on context", "", ""), rid)
		return
	}
	ns := api.PathParam(r, "ns")
	if !claim.HasScope("meta:"+ns) && !claim.HasScope("meta:*") &&
		!claim.HasScope("grade:"+ns) && !claim.HasScope("grade:*") {
		WriteError(w, SvcError(CodeScopeDenied, "missing meta scope", "", ""), rid)
		return
	}
	// In v1 we don't enumerate per-id; List returns latest only.
	writeJSON(w, http.StatusOK, struct {
		Versions []string `json:"versions"`
	}{Versions: []string{"latest"}})
}

// handleCapabilities returns the service capability descriptor.
func (s *Service) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	caps := struct {
		Service      string   `json:"service"`
		Version      string   `json:"version"`
		Endpoints    []string `json:"endpoints"`
		MaxCassette  int64    `json:"max_cassette_bytes"`
		Tiers        []int    `json:"tiers"`
		Capabilities []string `json:"capabilities"`
	}{
		Service:     "kit-conformance-svc",
		Version:     Version,
		Endpoints:   []string{"POST /v1/grade", "GET /v1/scenarios", "GET /v1/capabilities"},
		MaxCassette: effectiveHardCap(s.Receiver),
		Tiers:       []int{1, 2, 3},
		Capabilities: []string{
			"grade:cassette",
			"meta:scenarios",
		},
	}
	writeJSON(w, http.StatusOK, caps)
}

// handleHealth implements liveness probe.
func (s *Service) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"uptime_seconds": int(time.Since(s.StartedAt).Seconds()),
	})
}

// handleReady implements readiness probe. v1 just verifies the store
// is reachable.
func (s *Service) handleReady(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Store.Namespaces(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "unhealthy",
			"error":  err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

// writeJSON is a tiny helper for success responses.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// effectiveHardCap returns the hard cap honored by the receiver.
func effectiveHardCap(rc *CassetteReceiver) int64 {
	if rc == nil || rc.HardCap <= 0 {
		return DefaultHardCap
	}
	return rc.HardCap
}

func effectiveSoftCap(rc *CassetteReceiver) int64 {
	if rc == nil || rc.SoftCap <= 0 {
		return DefaultSoftCap
	}
	return rc.SoftCap
}

// errCodeFromErr extracts the code prefix from a receiver error string.
// Receiver errors are formatted as "<CODE>: <detail>" so handlers can
// shape the right HTTP response without re-classifying.
func errCodeFromErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, c := range []string{
		CodeCassetteMalformed, CodeCassetteManifestInvalid,
		CodeCassetteSizeExceeded, CodeCassetteGzipBomb,
		CodeStoryHashMismatch, CodeAcceptUnsupported,
	} {
		if len(msg) >= len(c) && msg[:len(c)] == c {
			return c
		}
	}
	return CodeCassetteMalformed
}

// registryJudge dispatches Score() through the ModelRegistry and applies
// per-claim caps. Closes over the claim so the dispatcher can charge
// the right tenant.
type registryJudge struct {
	reg   ModelRegistry
	claim *Claim
}

// Score satisfies AIJudge.
func (j *registryJudge) Score(ctx context.Context, req JudgeRequest) (JudgeResponse, error) {
	impl, err := j.reg.Resolve(req.Model)
	if err != nil {
		return JudgeResponse{}, fmt.Errorf("%s: %w", CodeJudgeUnavailable, err)
	}
	return impl.Score(ctx, req)
}

// ensure registryJudge implements AIJudge.
var _ AIJudge = (*registryJudge)(nil)

// Silence unused import lints.
var _ = output.CodeOK
