package consent

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Grants answers "may this subject use this capability now", records
// decisions, and audits every grant, deny, revoke and use.
//
// One Grants value is one session: once and session grants live in it
// and die with it (or with EndSession). Until and always grants go to
// the GrantStore. Every path fails closed: no grant, no prompter, a
// failing prompter, an unreadable store or a failing auditor on use all
// end in "no".
//
// Lookup for (capability, subject): the subject's own grant first, then
// the any-subject ("") grant; within a key the in-memory grant beats the
// persisted one. An expired until grant counts as absent.
type Grants struct {
	store         GrantStore
	audit         Auditor
	now           func() time.Time
	env           EnvProvider
	appPrefix     string
	promptVersion int

	mu  sync.Mutex
	mem map[grantKey]Grant
}

// Option configures Grants.
type Option func(*Grants)

// WithAuditor sets the auditor. Without one, NewGrants audits nothing
// and OpenGrants writes the default audit file.
func WithAuditor(a Auditor) Option { return func(g *Grants) { g.audit = a } }

// WithClock injects the clock (tests, deterministic replay).
func WithClock(now func() time.Time) Option { return func(g *Grants) { g.now = now } }

// WithEnv sets the env lookup for the telemetry precedence chain.
func WithEnv(env EnvProvider) Option { return func(g *Grants) { g.env = env } }

// WithAppPrefix sets the <APP>_TELEMETRY_MODE prefix for telemetry checks.
func WithAppPrefix(p string) Option { return func(g *Grants) { g.appPrefix = p } }

// WithPromptVersion stamps grants decided through Request. Default 1.
func WithPromptVersion(v int) Option { return func(g *Grants) { g.promptVersion = v } }

// NewGrants builds Grants over store. A nil store panics: it is a
// wiring bug, like NewHook(nil).
func NewGrants(store GrantStore, opts ...Option) *Grants {
	if store == nil {
		panic("consent: NewGrants called with nil GrantStore")
	}
	g := &Grants{
		store:         store,
		audit:         AuditorFunc(func(context.Context, AuditEvent) error { return nil }),
		now:           time.Now,
		env:           OSEnv(),
		promptVersion: 1,
	}
	for _, o := range opts {
		o(g)
	}
	return g
}

// OpenGrants builds Grants over the default FileStore and FileAuditor;
// opts apply after the defaults.
func OpenGrants(opts ...Option) (*Grants, error) {
	s, err := NewFileStore()
	if err != nil {
		return nil, err
	}
	a, err := NewFileAuditor()
	if err != nil {
		return nil, err
	}
	return NewGrants(s, append([]Option{WithAuditor(a)}, opts...)...), nil
}

// Verdict is the outcome of a check. Grant is the governing grant, zero
// when Reason is unset or expired.
type Verdict struct {
	Allowed bool
	Reason  Reason
	Grant   Grant
}

func (g *Grants) clock() time.Time { return g.now().UTC().Truncate(time.Second) }

// Check reports the current verdict without prompting, auditing or
// consuming anything.
func (g *Grants) Check(ctx context.Context, c Capability, subject string) (Verdict, error) {
	if err := validateKey(c, subject); err != nil {
		return Verdict{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.checkLocked(ctx, c, subject)
}

func (g *Grants) checkLocked(ctx context.Context, c Capability, subject string) (Verdict, error) {
	persisted, err := g.store.Grants(ctx)
	if err != nil {
		return Verdict{}, err
	}
	if c == CapTelemetry {
		return g.telemetryVerdict(ctx, persisted), nil
	}
	now := g.clock()
	expired := false
	keys := []grantKey{{c, subject}}
	if subject != "" {
		keys = append(keys, grantKey{c, ""})
	}
	for _, k := range keys {
		if m, ok := g.mem[k]; ok {
			return verdictOf(m), nil
		}
		for _, p := range persisted {
			if p.key() != k {
				continue
			}
			if p.Expired(now) {
				expired = true
				break
			}
			return verdictOf(p), nil
		}
	}
	if expired {
		return Verdict{Reason: ReasonExpired}, nil
	}
	return Verdict{Reason: ReasonUnset}, nil
}

func verdictOf(gr Grant) Verdict {
	if gr.Decision == Allow {
		return Verdict{Allowed: true, Reason: ReasonAllowed, Grant: gr}
	}
	return Verdict{Reason: ReasonDenied, Grant: gr}
}

// telemetryVerdict runs the telemetry resolver over the persisted
// record, so DO_NOT_TRACK and KIT_TELEMETRY_MODE=off stay a standing no.
func (g *Grants) telemetryVerdict(ctx context.Context, persisted []Grant) Verdict {
	var rec Decision
	found := false
	for _, p := range persisted {
		if p.Capability == CapTelemetry {
			rec, found = telemetryDecision(p), true
		}
	}
	d := Resolve(ctx, Inputs{Env: g.env, AppPrefix: g.appPrefix, Persisted: rec})
	if !found && d.State == StateDenied && d.DecisionSource == SourceConfig {
		return Verdict{Reason: ReasonUnset}
	}
	gr, _ := telemetryGrant(d)
	return verdictOf(gr)
}

// Decide records a decision and audits it as granted or denied. A zero
// DecidedAt is stamped with the clock. Until and always grants go to
// the store and replace any in-memory grant for the key; session and
// once allow grants stay in memory; a once deny is audited only.
func (g *Grants) Decide(ctx context.Context, gr Grant) error {
	if gr.DecidedAt.IsZero() {
		gr.DecidedAt = g.clock()
	}
	if err := gr.Validate(); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.decideLocked(ctx, gr)
}

func (g *Grants) decideLocked(ctx context.Context, gr Grant) error {
	switch {
	case gr.Duration.Persisted():
		if err := g.store.PutGrant(ctx, gr); err != nil {
			return err
		}
		delete(g.mem, gr.key())
	case gr.Duration == Once && gr.Decision == Deny:
	default:
		if g.mem == nil {
			g.mem = map[grantKey]Grant{}
		}
		g.mem[gr.key()] = gr
	}
	kind := AuditGranted
	if gr.Decision == Deny {
		kind = AuditDenied
	}
	return g.audit.Record(ctx, auditFromGrant(kind, gr, g.clock()))
}

// Request returns an allow verdict, asking p when there is no standing
// decision. A standing deny is refused without asking; no prompter, a
// prompter error or an invalid answer is refused too. Every refusal is
// a *RefusedError; store and audit failures come back as they are.
func (g *Grants) Request(ctx context.Context, c Capability, subject string, p Prompter) (Verdict, error) {
	v, err := g.Check(ctx, c, subject)
	if err != nil || v.Allowed {
		return v, err
	}
	refuse := func(r Reason, cause error) (Verdict, error) {
		return v, &RefusedError{Capability: c, Subject: subject, Reason: r, Err: cause}
	}
	if v.Reason == ReasonDenied || p == nil {
		return refuse(v.Reason, nil)
	}
	ans, err := p.Ask(ctx, Prompt{Capability: c, Subject: subject, PromptVersion: g.promptVersion, Reason: v.Reason})
	if err != nil {
		return refuse(v.Reason, err)
	}
	gr := Grant{
		Capability: c, Subject: subject, Decision: ans.Decision, Duration: ans.Duration,
		DecidedAt: g.clock(), ExpiresAt: ans.ExpiresAt,
		DecisionSource: SourcePrompt, PromptVersion: g.promptVersion,
	}
	if err := gr.Validate(); err != nil {
		return refuse(v.Reason, err)
	}
	if err := g.Decide(ctx, gr); err != nil {
		return v, err
	}
	if gr.Decision == Deny {
		return refuse(ReasonDeclined, nil)
	}
	return verdictOf(gr), nil
}

// Use is the gate before a tap: it re-checks, audits the use and
// consumes a once grant. Anything but an allow is a *RefusedError; an
// audit failure refuses the use with that error. Use never prompts.
func (g *Grants) Use(ctx context.Context, c Capability, subject string) (Grant, error) {
	if err := validateKey(c, subject); err != nil {
		return Grant{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	v, err := g.checkLocked(ctx, c, subject)
	if err != nil {
		return Grant{}, err
	}
	if !v.Allowed {
		return Grant{}, &RefusedError{Capability: c, Subject: subject, Reason: v.Reason}
	}
	if err := g.audit.Record(ctx, auditFromGrant(AuditUsed, v.Grant, g.clock())); err != nil {
		return Grant{}, fmt.Errorf("consent: audit use of %s: %w", c, err)
	}
	if v.Grant.Duration == Once {
		delete(g.mem, v.Grant.key())
	}
	return v.Grant, nil
}

// Revoke removes the grant for the exact key, in memory and in the
// store, and audits it when something was removed. source says who
// revoked (prompt for a user action, flag for a CLI verb, ...).
func (g *Grants) Revoke(ctx context.Context, c Capability, subject string, source DecisionSource) (bool, error) {
	if err := validateKey(c, subject); err != nil {
		return false, err
	}
	if !validSource(source) {
		return false, grantErr(KindInvalidSource, "%q (want prompt, flag, env or config)", source)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	k := grantKey{c, subject}
	_, inMem := g.mem[k]
	delete(g.mem, k)
	removed, err := g.store.DeleteGrant(ctx, c, subject)
	if err != nil {
		return inMem, err
	}
	if !removed && !inMem {
		return false, nil
	}
	e := AuditEvent{Event: AuditRevoked, At: g.clock(), Capability: c, Subject: subject, DecisionSource: source}
	return true, g.audit.Record(ctx, e)
}

// List returns every grant this session sees: persisted ones with the
// in-memory ones laid over them, sorted. Expired grants are included;
// use Grant.Expired to filter.
func (g *Grants) List(ctx context.Context) ([]Grant, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	persisted, err := g.store.Grants(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Grant, 0, len(persisted)+len(g.mem))
	for _, p := range persisted {
		if _, ok := g.mem[p.key()]; !ok {
			out = append(out, p)
		}
	}
	for _, m := range g.mem {
		out = append(out, m)
	}
	sortGrants(out)
	return out, nil
}

// EndSession drops every once and session grant.
func (g *Grants) EndSession() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mem = nil
}
