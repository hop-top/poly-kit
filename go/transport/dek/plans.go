package dek

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"hop.top/kit/go/transport/api"
)

// Whoami is the reply to GET /whoami.
type Whoami struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Tenant string   `json:"tenant"`
	Levels []string `json:"levels"`
	Roles  []string `json:"roles"`
}

// defaultPrincipal reads the identity the router's auth middleware
// verified. A listener with no authentication (loopback, unix socket)
// has one fixed local principal.
func defaultPrincipal(r *http.Request) (Whoami, bool) {
	m := api.RequestMetaFrom(r)
	w := Whoami{Kind: "user", Levels: []string{}, Roles: []string{}}
	if !m.Authenticated {
		w.ID, w.Name, w.Tenant = "local", "local", "local"
		return w, true
	}
	if m.Principal == "" {
		return w, false
	}
	w.ID, w.Name, w.Tenant = m.Principal, m.Principal, m.Tenant
	if w.Tenant == "" {
		w.Tenant = "default"
	}
	return w, true
}

func (c *Core) whoami(w http.ResponseWriter, r *http.Request) {
	who, ok := c.principal(r)
	if !ok {
		refuse(w, http.StatusUnauthorized, "no principal")
		return
	}
	if who.Levels == nil {
		who.Levels = []string{}
	}
	if who.Roles == nil {
		who.Roles = []string{}
	}
	api.JSON(w, http.StatusOK, who)
}

// ownerOf is the principal a plan or job belongs to.
func ownerOf(r *http.Request) string { return api.RequestMetaFrom(r).Principal }

type applyRequest struct {
	MinApprovals *uint8 `json:"min_approvals"`
}

type applyResponse struct {
	Kind string `json:"kind"`
	Body any    `json:"body"`
}

func blocked(reason string) applyResponse {
	return applyResponse{Kind: "blocked", Body: map[string]string{"reason": reason}}
}

// apply runs a stored plan: a fresh dry run first, compared with the
// plan the operator reviewed, and only when they agree the command
// itself.
func (c *Core) apply(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	owner := ownerOf(r)

	var ask applyRequest
	if raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1)); err != nil || len(raw) > maxBody {
		refuse(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	} else if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &ask); err != nil {
			refuse(w, http.StatusBadRequest, "malformed json")
			return
		}
	}
	if ask.MinApprovals != nil {
		// A core at this contract minor honors the request or
		// refuses it; this one has no approval levels to honor it.
		refuse(w, http.StatusUnprocessableEntity, "min_approvals is not supported: this core has no approval levels")
		return
	}

	unlock := c.locks.lock(id)
	defer unlock()

	sp, ok, err := c.plans.Get(ctx, id)
	if err != nil {
		refuse(w, http.StatusInternalServerError, "plan store failed")
		return
	}
	// Another principal's plan does not exist for the caller.
	if !ok || sp.Owner != owner {
		refuse(w, http.StatusNotFound, "no such plan")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = sp.Plan.IdempotencyKey
	}
	if key == "" {
		refuse(w, http.StatusUnprocessableEntity, "idempotency key required")
		return
	}
	if out, done := sp.Applied[key]; done {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out.Body)
		return
	}
	if other, found, err := c.plans.PlanForKey(ctx, owner, key); err != nil {
		refuse(w, http.StatusInternalServerError, "plan store failed")
		return
	} else if found && other != id {
		refuse(w, http.StatusConflict, "idempotency key used on another plan")
		return
	}
	if len(sp.Applied) > 0 {
		api.JSON(w, http.StatusOK, blocked("plan already applied"))
		return
	}

	cp, ok := c.byID[sp.Plan.Capability]
	if !ok {
		refuse(w, http.StatusNotFound, "no such capability")
		return
	}
	switch {
	case cp.Gate != GateFree && cp.Gate != GateApply:
		api.JSON(w, http.StatusOK, blocked(fmt.Sprintf("approval class %q is not served: this core has no review", cp.Gate)))
		return
	case cp.desc.RequiresConfirmToken:
		api.JSON(w, http.StatusOK, blocked("the command needs a typed confirmation token, which an apply cannot give"))
		return
	}

	rawArgs, _ := json.Marshal(sp.Plan.Args)
	creq, msg := c.decodeArgs(cp, rawArgs)
	if msg != "" {
		refuse(w, http.StatusUnprocessableEntity, msg)
		return
	}
	meta := api.RequestMetaFrom(r)

	// Fresh dry run, no key: see invoke.
	dry := creq
	dry.Flags = cloneFlags(creq.Flags)
	dry.Flags["dry-run"] = true
	dry.Meta = meta
	dry.Meta.IdempotencyKey = ""
	fresh, status, why := c.execute(ctx, dry)
	if status != 0 {
		refuse(w, status, why)
		return
	}
	if !reflect.DeepEqual(normalize(sp.Plan.Rendered), normalize(fresh)) {
		next := Plan{
			ID: newPlanID(), Capability: sp.Plan.Capability, Args: sp.Plan.Args,
			Rendered: fresh, IdempotencyKey: key,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if err := c.plans.Put(ctx, StoredPlan{Plan: next, Owner: owner}); err != nil {
			refuse(w, http.StatusInternalServerError, "plan not stored")
			return
		}
		api.JSON(w, http.StatusOK, applyResponse{Kind: "drifted", Body: map[string]any{
			"fresh": next,
			"diff":  map[string]any{"from": sp.Plan.Rendered, "to": fresh},
		}})
		return
	}

	// The plan the operator approved is the confirmation a gated
	// command asks for.
	if cp.desc.RequiresConfirmation {
		creq.Flags["confirm"] = "yes"
	}
	creq.Meta = meta
	creq.Meta.IdempotencyKey = key
	var result any
	if cp.desc.LongRunning {
		job, err := c.startJob(owner, cp, creq)
		if err != nil {
			refuse(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		result = map[string]string{"job_id": job.ID}
	} else {
		body, status, why := c.execute(ctx, creq)
		if status != 0 {
			refuse(w, status, why)
			return
		}
		result = body
	}

	resp := applyResponse{Kind: "applied", Body: result}
	raw, _ := json.Marshal(resp)
	raw = append(raw, '\n') // as api.JSON writes it, so a replay is byte-identical
	if sp.Applied == nil {
		sp.Applied = map[string]Outcome{}
	}
	sp.Applied[key] = Outcome{Body: raw}
	if err := c.plans.Put(ctx, sp); err != nil {
		// The command ran; say so rather than let a retry run it again.
		refuse(w, http.StatusInternalServerError, "applied, but the outcome was not stored")
		return
	}
	api.JSON(w, http.StatusOK, resp)
}

func cloneFlags(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// normalize makes two renderings of a plan comparable: JSON-shaped,
// with the timestamp kit's cli.Plan stamps at every run
// (generated_at) removed, and a captured stdout that is JSON read as
// JSON.
func normalize(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(raw, &out) != nil {
		return v
	}
	return strip(out)
}

func strip(v any) any {
	switch t := v.(type) {
	case map[string]any:
		delete(t, "generated_at")
		if s, ok := t["stdout"].(string); ok && len(t) == 1 {
			var inner any
			if json.Unmarshal([]byte(s), &inner) == nil {
				return strip(inner)
			}
		}
		for k, e := range t {
			t[k] = strip(e)
		}
	case []any:
		for i, e := range t {
			t[i] = strip(e)
		}
	}
	return v
}

// keyedMutex serializes work per key, forgetting a key once nobody
// holds or waits for it.
type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*keyedEntry
}

type keyedEntry struct {
	mu   sync.Mutex
	refs int
}

func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*keyedEntry{}
	}
	e := k.m[key]
	if e == nil {
		e = &keyedEntry{}
		k.m[key] = e
	}
	e.refs++
	k.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}
