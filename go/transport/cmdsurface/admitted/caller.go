package admitted

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"

	"hop.top/kit/internal/admittedctx"
)

// Establishment is how a transport established a caller's identity,
// as recorded in cmdsurface's Meta.Established.
type Establishment string

const (
	// EstablishedNone is the zero value: the transport established
	// nothing. Caller and Tenant, when present, are claims.
	EstablishedNone Establishment = ""
	// EstablishedVerified: a verifier the deployment configured
	// accepted a credential the caller presented (an api.AuthFunc, a
	// socket Authenticator, a webhook signature, a signed URL, a
	// consumed OAuth state).
	EstablishedVerified Establishment = "verified"
	// EstablishedTransport: the transport proves the caller by
	// construction, with no credential to check. The socket's
	// owner-only file admits only the owner's processes; the stdio
	// peer spawned the process and runs as its user; a platform
	// invocation (Lambda) was authorized by the platform's IAM; a
	// cron job was scheduled by the operator.
	EstablishedTransport Establishment = "transport"
)

// Caller is the caller of the invocation the bridge admitted, as the
// command running it sees it. The bridge builds it from the admitted
// Meta (cmdsurface.CallerOf) and stamps it on the run's context before
// any Runner runs the call.
type Caller struct {
	// Established is how the transport established the caller.
	Established Establishment
	// Principal and Tenant name a verified caller; both are empty
	// unless Established is EstablishedVerified, because a name on a
	// call nobody verified is a claim.
	Principal string
	Tenant    string
	// Scopes are the scopes the caller's verified credential holds;
	// nil unless Established is EstablishedVerified.
	Scopes []string
	// IdempotencyScope is the opaque scope an --idempotency-key of
	// this invocation is confined to (cmdsurface.IdempotencyScope).
	IdempotencyScope string
}

// CallerFrom returns the caller of the admitted invocation ctx belongs
// to: the context an in-process command sees, whichever Runner runs
// it. ok is false outside a served run. The returned Scopes are a copy,
// so a command cannot change what the next reader sees.
func CallerFrom(ctx context.Context) (Caller, bool) {
	if ctx == nil {
		return Caller{}, false
	}
	c, ok := ctx.Value(admittedctx.CallerKey{}).(Caller)
	if !ok {
		return Caller{}, false
	}
	if c.Scopes != nil {
		c.Scopes = append([]string(nil), c.Scopes...)
	}
	return c, true
}

// EnvIdempotencyScope is the environment variable a subprocess runner
// (cmdsurface.SubprocessRunner) hands a child process: the
// IdempotencyScope of the invocation it runs. The child is a separate
// process with no context to carry the invocation's caller, so this is
// how its --idempotency-key replay middleware learns whose key it
// holds (see [ScopeIdempotencyKey]).
const EnvIdempotencyScope = "KIT_IDEMPOTENCY_SCOPE"

// servedKeyPrefix marks a store key derived for a served invocation.
// A key a local user typed is used as typed, so the prefix keeps the
// two namespaces visibly apart in the store.
const servedKeyPrefix = "served:"

// ScopeIdempotencyKey returns the store key an --idempotency-key
// replay middleware records and looks key up under.
//
// Outside a served invocation it is key itself: a local user's own
// keys are theirs. Inside one — ctx carries the admitted caller
// ([CallerFrom]), or the process is a subprocess child holding
// [EnvIdempotencyScope] — it is key confined to the caller's
// IdempotencyScope and hashed, so a caller who sends another caller's
// key is not answered with that caller's recorded output, and no
// served key meets a local one. An empty key stays empty.
func ScopeIdempotencyKey(ctx context.Context, key string) string {
	if key == "" {
		return ""
	}
	var scope string
	if c, ok := CallerFrom(ctx); ok {
		scope = c.IdempotencyScope
	} else if s := os.Getenv(EnvIdempotencyScope); s != "" {
		scope = s
	} else {
		return key
	}
	sum := sha256.Sum256([]byte(scope + "\x00" + key))
	return servedKeyPrefix + hex.EncodeToString(sum[:])
}
