package cmdsurface

import (
	"errors"
	"net/http"

	"connectrpc.com/connect"

	"hop.top/kit/go/transport/api"
)

// rpcIdempotencyError maps the idempotency refusals onto their Connect
// codes: a key still in flight is Aborted (409 on the Connect wire), a
// key reused for another invocation InvalidArgument (400; REST answers
// it 422).
func rpcIdempotencyError(err error) (*connect.Error, bool) {
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		return connect.NewError(connect.CodeAborted, err), true
	case errors.Is(err, ErrIdempotencyKeyReused):
		return connect.NewError(connect.CodeInvalidArgument, err), true
	}
	return nil, false
}

// rpcIdempotencyKey fills the key from the Idempotency-Key request
// header when the message's meta carries none.
func rpcIdempotencyKey(inv *Invocation, header http.Header) {
	if inv.Meta.IdempotencyKey == "" {
		inv.Meta.IdempotencyKey = header.Get(api.HeaderIdempotencyKey)
	}
}

// rpcMarkReplayed sets the replay marker on a response header.
func rpcMarkReplayed(h http.Header, replayed bool) {
	if replayed {
		h.Set(HeaderIdempotentReplayed, "true")
	}
}
