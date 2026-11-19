package dek

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"hop.top/kit/go/transport/api"
)

// Prefix is the mount point of every dek route.
const Prefix = "/v1/dek"

// Contract version this surface is written against: the revision of
// the contract whose schemas the tests check against. A client refuses
// a different major and gates features on the minor, so it is the
// revision these types were taken from, not a higher one.
const (
	ContractMajor = 1
	ContractMinor = 33
)

const maxBody = 1 << 20

// Config configures [New].
type Config struct {
	// Projection is the reflected command set and its executor,
	// exactly what the REST projection serves (cmdsurface.Projection
	// builds one). The executor carries the permission gate, audit and
	// idempotency the other surfaces have; dek adds no policy of its
	// own.
	Projection api.ProjectionConfig
	// CoreID names the core in the hello. Empty uses the tool name.
	CoreID string
	// Gate picks an approval class per command; see [Options.Gate].
	Gate func(api.CommandDescriptor) Gate
	// Principal names the caller of a request for /whoami. It returns
	// false to refuse (401). Nil uses the router's verified identity,
	// or a fixed local principal on a listener with no authentication.
	Principal func(r *http.Request) (Whoami, bool)
	// Plans keeps plans between a dry run and its apply. Nil is in
	// memory.
	Plans PlanStore
	// Jobs keeps background jobs. Nil is in memory.
	Jobs JobStore
	// MaxJobs bounds jobs running at once; zero is 16.
	MaxJobs int
}

// Core is the dek surface of one tool: hello, capabilities, invoke.
type Core struct {
	caps   []Capability
	byID   map[string]*Capability
	etag   string
	coreID string
	exec   api.CommandExecutor

	principal func(r *http.Request) (Whoami, bool)
	plans     PlanStore
	jobs      JobStore
	locks     keyedMutex

	base   context.Context
	cancel context.CancelFunc
	slots  chan struct{}
}

// New builds the manifest and the handlers. It fails when the
// manifest cannot be built (see [Manifest]).
func New(cfg Config) (*Core, error) {
	caps, err := Manifest(cfg.Projection.Descriptors,
		Options{ToolName: cfg.Projection.ToolName, Gate: cfg.Gate})
	if err != nil {
		return nil, err
	}
	c := &Core{
		caps:   caps,
		byID:   make(map[string]*Capability, len(caps)),
		etag:   etagOf(caps),
		coreID: cfg.CoreID,
		exec:   cfg.Projection.Executor,
	}
	if c.coreID == "" {
		c.coreID = cfg.Projection.ToolName
	}
	for i := range c.caps {
		c.byID[c.caps[i].ID] = &c.caps[i]
	}
	c.principal = cfg.Principal
	if c.principal == nil {
		c.principal = defaultPrincipal
	}
	if c.plans = cfg.Plans; c.plans == nil {
		c.plans = NewMemoryPlanStore()
	}
	if c.jobs = cfg.Jobs; c.jobs == nil {
		c.jobs = NewMemoryJobStore()
	}
	n := cfg.MaxJobs
	if n <= 0 {
		n = 16
	}
	c.slots = make(chan struct{}, n)
	c.base, c.cancel = context.WithCancel(context.Background())
	if stop := cfg.Projection.Stopping; stop != nil {
		go func() {
			select {
			case <-stop:
				c.cancel()
			case <-c.base.Done():
			}
		}()
	}
	return c, nil
}

// Close cancels the jobs still running. A core built over a
// projection with a Stopping channel closes itself when it fires.
func (c *Core) Close() { c.cancel() }

// ETag is the manifest's ETag, as served on /capabilities and in the
// hello.
func (c *Core) ETag() string { return c.etag }

// Capabilities is the manifest.
func (c *Core) Capabilities() []Capability { return c.caps }

// Mount registers the dek routes on r. Authentication is the
// router's. The hello is the one route a client calls before it holds
// a credential: a router that authenticates must let GET
// /v1/dek/hello through (the api service does).
func (c *Core) Mount(r *api.Router) {
	r.Handle(http.MethodGet, Prefix+"/hello", c.hello)
	r.Handle(http.MethodGet, Prefix+"/capabilities", c.capabilities)
	r.Handle(http.MethodPost, Prefix+"/invoke", c.invoke)
	r.Handle(http.MethodGet, Prefix+"/whoami", c.whoami)
	r.Handle(http.MethodPost, Prefix+"/plans/{id}/apply", c.apply)
	r.Handle(http.MethodGet, Prefix+"/jobs", c.listJobs)
	r.Handle(http.MethodGet, Prefix+"/jobs/{id}", c.getJob)
}

type hello struct {
	Contract     contractVersion `json:"contract"`
	CoreID       string          `json:"core_id"`
	ManifestETag string          `json:"manifest_etag"`
}

type contractVersion struct {
	Major uint16 `json:"major"`
	Minor uint16 `json:"minor"`
}

func (c *Core) hello(w http.ResponseWriter, _ *http.Request) {
	api.JSON(w, http.StatusOK, hello{
		Contract:     contractVersion{ContractMajor, ContractMinor},
		CoreID:       c.coreID,
		ManifestETag: c.etag,
	})
}

func (c *Core) capabilities(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("ETag", c.etag)
	if r.Header.Get("If-None-Match") == c.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	api.JSON(w, http.StatusOK, c.caps)
}

// reasonBody is a dek error body.
type reasonBody struct {
	Reason string `json:"reason"`
}

func refuse(w http.ResponseWriter, status int, reason string) {
	api.JSON(w, status, reasonBody{Reason: reason})
}

type invokeRequest struct {
	Capability     string          `json:"capability"`
	Args           json.RawMessage `json:"args"`
	DryRun         bool            `json:"dry_run"`
	IdempotencyKey string          `json:"idempotency_key"`
}

// Plan is a dry run's result: what the command would do, as the
// command rendered it.
type Plan struct {
	ID             string `json:"id"`
	Capability     string `json:"capability"`
	Args           any    `json:"args"`
	Rendered       any    `json:"rendered"`
	IdempotencyKey string `json:"idempotency_key"`
	CreatedAt      string `json:"created_at"`
}

type invokeResponse struct {
	Kind string `json:"kind"`
	Body any    `json:"body"`
}

func (c *Core) invoke(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		refuse(w, http.StatusUnsupportedMediaType, "expected application/json")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		refuse(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	var req invokeRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		refuse(w, http.StatusBadRequest, "malformed json")
		return
	}
	cp, ok := c.byID[req.Capability]
	if !ok {
		refuse(w, http.StatusNotFound, "no such capability")
		return
	}

	// Plan rules first: nothing here reaches the command.
	canPlan := cp.desc.SideEffect == api.SideEffectWrite || cp.desc.SideEffect == api.SideEffectDestructive
	switch {
	case req.DryRun && !canPlan:
		refuse(w, http.StatusUnprocessableEntity, "capability has no dry run")
		return
	case !req.DryRun && cp.DryRun:
		refuse(w, http.StatusUnprocessableEntity, "capability needs a plan: invoke with dry_run, then apply")
		return
	case !req.DryRun && cp.Gate != GateFree:
		// Only the free class runs on a direct call here: any other
		// class is a promise this surface cannot keep yet.
		refuse(w, http.StatusForbidden, fmt.Sprintf("approval class %q is not served on a direct call", cp.Gate))
		return
	}

	creq, msg := c.decodeArgs(cp, req.Args)
	if msg != "" {
		refuse(w, http.StatusUnprocessableEntity, msg)
		return
	}
	if !cp.Read && req.IdempotencyKey == "" {
		refuse(w, http.StatusUnprocessableEntity, "idempotency_key required")
		return
	}
	creq.Meta = api.RequestMetaFrom(r)
	if req.DryRun {
		creq.Flags["dry-run"] = true
		// A dry run carries no idempotency key to the executor: one
		// that replays by key would answer the real run, later, with
		// this plan.
		creq.Meta.IdempotencyKey = ""
	} else if req.IdempotencyKey != "" {
		creq.Meta.IdempotencyKey = req.IdempotencyKey
	}

	if c.exec == nil {
		refuse(w, http.StatusServiceUnavailable, "no executor")
		return
	}
	if !req.DryRun && cp.desc.LongRunning {
		job, err := c.startJob(creq.Meta.Principal, cp, creq)
		if err != nil {
			refuse(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		api.JSON(w, http.StatusOK, invokeResponse{Kind: "accepted", Body: map[string]string{"job_id": job.ID}})
		return
	}
	body, status, why := c.execute(r.Context(), creq)
	if status != 0 {
		refuse(w, status, why)
		return
	}
	if !req.DryRun {
		api.JSON(w, http.StatusOK, invokeResponse{Kind: "result", Body: body})
		return
	}
	var args any = map[string]any{}
	_ = json.Unmarshal(req.Args, &args)
	if args == nil {
		args = map[string]any{}
	}
	plan := Plan{
		ID:             newPlanID(),
		Capability:     cp.ID,
		Args:           args,
		Rendered:       body,
		IdempotencyKey: req.IdempotencyKey,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	if err := c.plans.Put(r.Context(), StoredPlan{Plan: plan, Owner: creq.Meta.Principal}); err != nil {
		refuse(w, http.StatusInternalServerError, "plan not stored")
		return
	}
	api.JSON(w, http.StatusOK, invokeResponse{Kind: "plan", Body: plan})
}

// execute runs one command through the executor. A non-zero status
// is the HTTP status of a refusal and why its reason.
func (c *Core) execute(ctx context.Context, creq api.CommandRequest) (body any, status int, why string) {
	res, err := c.exec.Execute(ctx, creq)
	if err != nil {
		status, why = execErrorStatus(err)
		return nil, status, why
	}
	if res.ExitCode != 0 {
		why = strings.TrimSpace(res.Stderr)
		if why == "" {
			why = "command failed"
		}
		return nil, api.StatusForExitCode(res.ExitCode), why
	}
	return resultBody(res), 0, ""
}

// decodeArgs checks args against the capability's schema and maps
// them onto a command request: flags by name, positionals in
// declared order. A non-empty message is a 422 reason.
func (c *Core) decodeArgs(cp *Capability, raw json.RawMessage) (api.CommandRequest, string) {
	creq := api.CommandRequest{
		Path:  append([]string(nil), cp.desc.Path...),
		Flags: map[string]any{},
	}
	args := map[string]any{}
	if t := strings.TrimSpace(string(raw)); t != "" && t != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return creq, "args must be an object"
		}
	}
	for name, v := range args {
		typ, ok := cp.props[name]
		if !ok {
			return creq, "unknown argument: " + name
		}
		if !jsonTypeMatches(typ, v) {
			if isPositional(cp, name) {
				return creq, "argument " + name + " must be a string"
			}
			return creq, "argument " + name + " must be of type " + typ
		}
		if !isPositional(cp, name) {
			creq.Flags[name] = v
		}
	}
	for name := range cp.required {
		if _, ok := args[name]; !ok {
			return creq, "missing required argument: " + name
		}
	}
	for _, name := range cp.positional {
		v, ok := args[name].(string)
		if !ok {
			break // optional positionals are trailing
		}
		creq.Args = append(creq.Args, v)
	}
	return creq, ""
}

func isPositional(cp *Capability, name string) bool {
	for _, p := range cp.positional {
		if p == name {
			return true
		}
	}
	return false
}

func jsonTypeMatches(typ string, v any) bool {
	switch typ {
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == float64(int64(f))
	case "number":
		_, ok := v.(float64)
		return ok
	case "array":
		a, ok := v.([]any)
		if !ok {
			return false
		}
		for _, e := range a {
			if _, ok := e.(string); !ok {
				return false
			}
		}
		return true
	case "object":
		_, ok := v.(map[string]any)
		return ok
	default:
		_, ok := v.(string)
		return ok
	}
}

// resultBody is what a result carries: the command's structured data
// when it has some, else its captured output.
func resultBody(res api.CommandResult) any {
	if res.Data != nil {
		return res.Data
	}
	return map[string]any{"stdout": res.Stdout}
}

func execErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, api.ErrUnauthenticated):
		return http.StatusUnauthorized, "authentication required"
	case errors.Is(err, api.ErrPermissionDenied):
		return http.StatusForbidden, err.Error()
	case errors.Is(err, api.ErrDestructiveBlocked):
		return http.StatusForbidden, "destructive command blocked on this surface"
	case errors.Is(err, api.ErrCommandNotInvocable):
		return http.StatusNotFound, "no such capability"
	case errors.Is(err, context.Canceled):
		return http.StatusServiceUnavailable, "canceled"
	default:
		// Not the error text: it may carry what the command saw.
		return http.StatusInternalServerError, "command failed"
	}
}

func newPlanID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "plan_" + hex.EncodeToString(b[:])
}
