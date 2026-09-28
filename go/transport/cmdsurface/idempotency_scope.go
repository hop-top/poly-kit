package cmdsurface

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"strings"
)

// EnvIdempotencyScope is the environment variable [SubprocessRunner]
// hands a child process: the [IdempotencyScope] of the invocation it
// runs. The child is a separate process with no context to carry the
// invocation's Meta, so this is how its --idempotency-key replay
// middleware learns whose key it holds (see [ScopeIdempotencyKey]).
const EnvIdempotencyScope = "KIT_IDEMPOTENCY_SCOPE"

// servedKeyPrefix marks a store key derived for a served invocation.
// A key a local user typed is used as typed, so the prefix keeps the
// two namespaces visibly apart in the store.
const servedKeyPrefix = "served:"

type metaKey struct{}

// ContextWithMeta returns ctx carrying m as the Meta of the served
// invocation running on it. [InProcessRunner] stamps every invocation
// it runs, so a command's context tells it that it is served and on
// whose behalf; a custom in-process Runner does the same.
func ContextWithMeta(ctx context.Context, m Meta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

// MetaFromContext returns the Meta of the served invocation running
// on ctx, and false when ctx carries none: the command was run from
// its own command line, not served.
func MetaFromContext(ctx context.Context) (Meta, bool) {
	if ctx == nil {
		return Meta{}, false
	}
	m, ok := ctx.Value(metaKey{}).(Meta)
	return m, ok
}

// callerScope is who a per-caller store or bucket counts a call
// against: the one decision idempotency ([IdempotencyScope]), the
// result cache, the rate limit and quota ([QuotaKey]) share, so no two
// of them can disagree about whose call it is.
//
// A caller a verifier established ([EstablishedVerified]) with a
// principal is scoped to its tenant and principal. A caller the
// transport itself vouches for ([EstablishedTransport]: the owner-only
// socket, the stdio spawn, a cron firing, an IAM-signed Lambda call)
// is the server's owner, scoped to that transport as
// transport/<surface>, never by the name or tenant it claims. Anything
// else is unestablished; each consumer says what an unestablished call
// shares, because that is where they legitimately differ: a claim
// never splits a rate-limit bucket or a quota, isolates an idempotency
// record, and shares the anonymous cache entry.
type callerScope struct {
	// established is EstablishedVerified, EstablishedTransport, or
	// EstablishedNone for every other call.
	established Establishment
	// tenant and principal name a verified caller.
	tenant, principal string
	// surface names a transport caller's transport.
	surface Surface
}

func scopeOf(m Meta) callerScope {
	switch {
	case m.Established == EstablishedVerified && m.Caller != "":
		return callerScope{established: EstablishedVerified, tenant: m.Tenant, principal: m.Caller}
	case m.Established == EstablishedTransport:
		return callerScope{established: EstablishedTransport, surface: m.Surface}
	default:
		return callerScope{established: EstablishedNone}
	}
}

// IdempotencyScope returns the opaque scope an idempotency key is
// confined to for the caller m describes, so one caller's key never
// answers another.
//
// The caller is scoped as every per-caller gate scopes it: a verified
// principal to its tenant and principal alone, so its key answers it
// on every surface; a caller the transport vouches for to that
// transport, never by the name it claims. Any other call is scoped to
// its surface, tenant and claimed caller; one without a caller to its
// client host too. The kinds of scope never meet: no claimed name,
// whichever transport carries it, reaches a verified caller's records,
// nor the reverse. Request and trace ids never scope.
//
// The bridge's idempotency ledger, its read-tier result cache and a
// command's own --idempotency-key middleware ([ScopeIdempotencyKey])
// all scope by it.
func IdempotencyScope(m Meta) string {
	var parts []string
	switch s := scopeOf(m); s.established {
	case EstablishedVerified:
		parts = []string{"verified", s.tenant, s.principal}
	case EstablishedTransport:
		parts = []string{"transport", string(s.surface)}
	default:
		parts = []string{"claimed", string(m.Surface), m.Tenant, m.Caller}
		if m.Caller == "" {
			// The host alone: a caller's next connection, from
			// another port, is the same client.
			host := m.Extra["remote_addr"]
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			parts = append(parts, host)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// ScopeIdempotencyKey returns the store key an --idempotency-key
// replay middleware records and looks key up under.
//
// Outside a served invocation it is key itself: a local user's own
// keys are theirs. Inside one — ctx carries the invocation's Meta, or
// the process is a [SubprocessRunner] child holding
// [EnvIdempotencyScope] — it is key confined to the caller's
// [IdempotencyScope] and hashed, so a caller who sends another
// caller's key is not answered with that caller's recorded output,
// and no served key meets a local one. An empty key stays empty.
func ScopeIdempotencyKey(ctx context.Context, key string) string {
	if key == "" {
		return ""
	}
	var scope string
	if m, ok := MetaFromContext(ctx); ok {
		scope = IdempotencyScope(m)
	} else if s := os.Getenv(EnvIdempotencyScope); s != "" {
		scope = s
	} else {
		return key
	}
	sum := sha256.Sum256([]byte(scope + "\x00" + key))
	return servedKeyPrefix + hex.EncodeToString(sum[:])
}
