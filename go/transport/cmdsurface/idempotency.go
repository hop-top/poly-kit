package cmdsurface

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/transport/api"
)

// Idempotency replay is the invocation plane's slot 8 (see
// docs/contracts/serve-lifecycle.md, Middleware). A remote call
// carrying Meta.IdempotencyKey is answered from the ledger when the
// same principal already ran the same invocation under that key: the
// recorded Result comes back marked Replayed, and nothing runs. A call
// the ledger has not seen runs, and a successful run is recorded at
// slot 13. Inside slot 8, idempotency replay answers first; the
// read-tier result cache is consulted only for a call it let through.

// Refusal codes of the idempotency block, the same string on every
// surface.
const (
	// CodeIdempotencyConflict refuses a call whose key names a call
	// that is still running.
	CodeIdempotencyConflict = api.CodeIdempotencyConflict
	// CodeIdempotencyKeyReused refuses a key reused for a different
	// invocation.
	CodeIdempotencyKeyReused = api.CodeIdempotencyKeyReused
)

// Replay markers and key carriers a transport renders.
const (
	// HeaderIdempotentReplayed is the response header, set to "true",
	// that marks a replayed answer over HTTP and Connect.
	HeaderIdempotentReplayed = api.HeaderIdempotentReplayed
	// MCPMetaIdempotencyKey is the params._meta key an MCP client puts
	// its idempotency key under. It wins over an Idempotency-Key header
	// on the same call.
	MCPMetaIdempotencyKey = "hop.top/idempotency-key"
	// MCPMetaIdempotentReplayed is the result _meta key, set to true,
	// that marks a replayed tools/call result.
	MCPMetaIdempotentReplayed = "hop.top/idempotent-replayed"
	// idempotentReplayedExtra marks a replay in the audit record.
	idempotentReplayedExtra = "idempotent_replayed"
)

// ErrIdempotencyConflict refuses a call whose idempotency key belongs
// to a call from the same principal that is still running. It wraps
// domain.ErrConflict, so a transport that predates the class answers
// 409.
var ErrIdempotencyConflict error = &wrappingError{
	msg:  "cmdsurface: " + CodeIdempotencyConflict + ": key in use by a call still running",
	wrap: domain.ErrConflict,
}

// ErrIdempotencyKeyReused refuses a call whose idempotency key the same
// principal already used for a different invocation. It wraps
// domain.ErrValidation, so a transport that predates the class
// answers 422.
var ErrIdempotencyKeyReused error = &wrappingError{
	msg:  "cmdsurface: " + CodeIdempotencyKeyReused + ": key already used for a different invocation",
	wrap: domain.ErrValidation,
}

// wrappingError is a sentinel that wraps its nearest broader class
// without repeating that class's text.
type wrappingError struct {
	msg  string
	wrap error
}

func (e *wrappingError) Error() string { return e.msg }
func (e *wrappingError) Unwrap() error { return e.wrap }

// IdempotencyRefusalCode returns the refusal code of an idempotency
// refusal — [CodeIdempotencyConflict] or [CodeIdempotencyKeyReused] —
// or "" for any other error.
func IdempotencyRefusalCode(err error) string {
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		return CodeIdempotencyConflict
	case errors.Is(err, ErrIdempotencyKeyReused):
		return CodeIdempotencyKeyReused
	}
	return ""
}

// IdempotencyLedger is the state idempotency replay keeps: the store
// of recorded results and the set of keyed calls still running. One
// ledger is shared by every bridge that should answer for the same
// keys — the services of one process — so a key in flight on one
// transport is in flight on all of them. It is safe for concurrent
// use.
//
// Calls in flight are tracked in this process only. Recorded results
// live in the store, so a sqlite store shared by processes replays
// across them; a conflict between two processes running the same key
// at once is not detected.
type IdempotencyLedger struct {
	open func() (idemstore.Store, error)
	now  func() time.Time

	mu       sync.Mutex
	store    idemstore.Store
	openErr  error
	opened   bool
	inflight map[string]string // store key -> fingerprint
}

// NewIdempotencyLedger returns a ledger recording into store.
func NewIdempotencyLedger(store idemstore.Store) *IdempotencyLedger {
	return OpenIdempotencyLedger(func() (idemstore.Store, error) {
		if store == nil {
			return nil, errors.New("cmdsurface: nil idempotency store")
		}
		return store, nil
	})
}

// OpenIdempotencyLedger returns a ledger whose store open returns on
// the first keyed call, so a process that never sees a key never
// creates one. An open error fails every keyed call with that error.
func OpenIdempotencyLedger(open func() (idemstore.Store, error)) *IdempotencyLedger {
	return &IdempotencyLedger{open: open, now: time.Now, inflight: map[string]string{}}
}

// Close closes the ledger's store when it was opened.
func (l *IdempotencyLedger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.store == nil {
		return nil
	}
	err := l.store.Close()
	l.store = nil
	l.openErr = errors.New("cmdsurface: idempotency ledger closed")
	return err
}

func (l *IdempotencyLedger) storeLocked() (idemstore.Store, error) {
	if !l.opened {
		l.opened = true
		l.store, l.openErr = l.open()
		if l.openErr != nil {
			l.openErr = fmt.Errorf("cmdsurface: idempotency store: %w", l.openErr)
		}
	}
	return l.store, l.openErr
}

// WithIdempotency turns idempotency replay on for the bridge's remote
// surfaces, recording into ledger and replaying a recorded result for
// ttl after it was recorded; zero or less is idemstore.DefaultTTL. A
// nil ledger leaves replay off, and a call without a key is never
// affected.
func WithIdempotency(ledger *IdempotencyLedger, ttl time.Duration) Option {
	return func(c *bridgeConfig) {
		if ledger == nil {
			c.idempotency = nil
			return
		}
		if ttl <= 0 {
			ttl = idemstore.DefaultTTL
		}
		c.idempotency = &idempotencyConfig{ledger: ledger, ttl: ttl}
	}
}

type idempotencyConfig struct {
	ledger *IdempotencyLedger
	ttl    time.Duration
}

// idemClaim is what slot 8 decided for one admitted call: a replay, or
// a reservation of the key the run must settle. The nil claim is a
// call idempotency does not touch.
type idemClaim struct {
	ledger *IdempotencyLedger
	key    string
	fp     string
	replay *Result
	stop   func() bool
	once   sync.Once
}

// idemRecord is what the store keeps for a key: the invocation's
// fingerprint and the Result to replay.
type idemRecord struct {
	Fingerprint string          `json:"fingerprint"`
	ExitCode    int             `json:"exit_code"`
	Stdout      string          `json:"stdout,omitempty"`
	Stderr      string          `json:"stderr,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
}

// admitIdempotency is slot 8's idempotency half. It returns nil for a
// call it does not touch, a claim carrying the replay for a key the
// principal already used for this invocation, or a claim reserving the
// key for the run. A key in flight refuses with ErrIdempotencyConflict;
// a key used for another invocation, running or recorded, with
// ErrIdempotencyKeyReused.
//
// The reservation is released when the run settles it, or when ctx
// ends first, so an admission a transport abandons (a person declined
// the confirmation) does not hold its key.
func (b *Bridge) admitIdempotency(ctx context.Context, inv Invocation) (*idemClaim, error) {
	cfg := b.cfg.idempotency
	if cfg == nil || inv.Meta.IdempotencyKey == "" || !inv.Meta.Surface.remote() {
		return nil, nil
	}
	l := cfg.ledger
	key := idempotencyStoreKey(inv.Meta)
	fp := idempotencyFingerprint(inv)

	l.mu.Lock()
	if running, ok := l.inflight[key]; ok {
		l.mu.Unlock()
		if running != fp {
			return nil, ErrIdempotencyKeyReused
		}
		return nil, ErrIdempotencyConflict
	}
	store, err := l.storeLocked()
	if err != nil {
		l.mu.Unlock()
		return nil, err
	}
	l.inflight[key] = fp
	l.mu.Unlock()

	claim := &idemClaim{ledger: l, key: key, fp: fp}
	rec, hit, err := store.Lookup(ctx, key)
	if err != nil {
		claim.release()
		return nil, fmt.Errorf("cmdsurface: idempotency lookup: %w", err)
	}
	if !hit || l.now().Sub(rec.Recorded) > cfg.ttl {
		return claim.bind(ctx), nil
	}
	res, recFP, err := decodeIdemRecord(rec.Output)
	if err != nil {
		// A record this version cannot read is no record: run, and
		// the run's record replaces it.
		return claim.bind(ctx), nil
	}
	// A replay runs nothing, so it holds no reservation.
	claim.release()
	if recFP != fp {
		return nil, ErrIdempotencyKeyReused
	}
	res.Replayed = true
	claim.replay = &res
	return claim, nil
}

// bind releases the reservation when ctx ends before the run settles.
func (c *idemClaim) bind(ctx context.Context) *idemClaim {
	c.stop = context.AfterFunc(ctx, c.release)
	return c
}

// replayed reports the recorded Result a replay answers with.
func (c *idemClaim) replayed() (Result, bool) {
	if c == nil || c.replay == nil {
		return Result{}, false
	}
	return *c.replay, true
}

// settle is slot 13's idempotency half: a run that ended without an
// error and with exit code zero is recorded under the key, and the
// reservation is released either way. The record is written before
// the release, so a retry arriving after the release replays.
//
// A record that cannot be written is dropped: the work happened, and
// its answer still reaches the caller; a later call with the key runs
// again.
func (c *idemClaim) settle(ctx context.Context, res Result, err error) {
	if c == nil || c.replay != nil {
		return
	}
	if c.stop != nil {
		c.stop()
	}
	defer c.release()
	if err != nil || res.ExitCode != 0 {
		return
	}
	payload, merr := encodeIdemRecord(c.fp, res)
	if merr != nil {
		return
	}
	c.ledger.mu.Lock()
	store := c.ledger.store
	c.ledger.mu.Unlock()
	if store == nil {
		return
	}
	_ = store.Record(context.WithoutCancel(ctx), c.key, idemstore.Result{
		ExitCode: res.ExitCode,
		Output:   payload,
		Recorded: c.ledger.now().UTC(),
	})
}

// release frees the key's reservation, once.
func (c *idemClaim) release() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		c.ledger.mu.Lock()
		if c.ledger.inflight[c.key] == c.fp {
			delete(c.ledger.inflight, c.key)
		}
		c.ledger.mu.Unlock()
	})
}

// abandon releases a reservation whose run will never happen.
func (c *idemClaim) abandon() {
	if c == nil {
		return
	}
	if c.stop != nil {
		c.stop()
	}
	c.release()
}

// idempotencyStoreKey scopes the caller's key to its principal and
// hashes the result, so a key is never answered for another caller
// and the store holds no principal or key in the clear.
//
// The scope is the tenant, the caller and the surface. A call without
// a caller is scoped to its client host, and with neither to the
// surface alone — the socket's owner-only file and the stdio spawn
// admit one local user, so their callers share one scope. The
// surface stays in the scope because a caller on some surfaces is a
// claim, not a verified identity, and a claim must not reach another
// surface's records.
func idempotencyStoreKey(m Meta) string {
	parts := []string{"served", string(m.Surface), m.Tenant, m.Caller}
	if m.Caller == "" {
		parts = append(parts, addrHost(m.Extra["remote_addr"]))
	}
	parts = append(parts, m.IdempotencyKey)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// addrHost is the host of a host:port client address, so a caller's
// next connection, from another port, is the same client.
func addrHost(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// idempotencyFingerprint identifies the invocation a key was used for:
// the argv the runner would build, less the key itself. Flag values
// are rendered as the runner renders them, so the same call sent as a
// query string or as a JSON body fingerprints the same.
func idempotencyFingerprint(inv Invocation) string {
	if _, ok := inv.Flags[idempotencyKeyFlag]; ok {
		flags := make(map[string]any, len(inv.Flags))
		for k, v := range inv.Flags {
			if k != idempotencyKeyFlag {
				flags[k] = v
			}
		}
		inv.Flags = flags
	}
	sum := sha256.Sum256([]byte(strings.Join(buildArgs(inv), "\x00")))
	return hex.EncodeToString(sum[:])
}

func encodeIdemRecord(fp string, res Result) ([]byte, error) {
	rec := idemRecord{Fingerprint: fp, ExitCode: res.ExitCode, Stdout: res.Stdout, Stderr: res.Stderr}
	if res.Data != nil {
		raw, err := json.Marshal(res.Data)
		if err != nil {
			return nil, err
		}
		rec.Data = raw
	}
	return json.Marshal(rec)
}

// decodeIdemRecord reads a record. Numbers in Data keep their exact
// text, as the runner's own decoding does, so a replayed payload
// re-encodes byte for byte.
func decodeIdemRecord(raw []byte) (Result, string, error) {
	var rec idemRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return Result{}, "", err
	}
	if rec.Fingerprint == "" {
		return Result{}, "", errors.New("cmdsurface: idempotency record without fingerprint")
	}
	res := Result{ExitCode: rec.ExitCode, Stdout: rec.Stdout, Stderr: rec.Stderr}
	if len(rec.Data) > 0 {
		dec := json.NewDecoder(bytes.NewReader(rec.Data))
		dec.UseNumber()
		if err := dec.Decode(&res.Data); err != nil {
			return Result{}, "", err
		}
	}
	return res, rec.Fingerprint, nil
}

// markReplayed returns inv with the replay marker in Extra, for the
// audit record. The caller's map is not mutated.
func markReplayed(inv Invocation) Invocation {
	extra := make(map[string]string, len(inv.Meta.Extra)+1)
	for k, v := range inv.Meta.Extra {
		extra[k] = v
	}
	extra[idempotentReplayedExtra] = "true"
	inv.Meta.Extra = extra
	return inv
}

// replayEvents streams a replayed Result the way a runner streams a
// run: one event per output line, then the done event.
func replayEvents(res Result, out chan<- Event) {
	now := time.Now()
	for _, s := range []struct{ kind, text string }{{"stdout", res.Stdout}, {"stderr", res.Stderr}} {
		for _, line := range strings.SplitAfter(s.text, "\n") {
			if line == "" {
				continue
			}
			out <- Event{Kind: s.kind, Data: strings.TrimSuffix(line, "\n"), At: now}
		}
	}
	out <- Event{Kind: "done", Data: &res, At: now}
}
