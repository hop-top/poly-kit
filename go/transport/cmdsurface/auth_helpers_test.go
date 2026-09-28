package cmdsurface

import (
	"errors"
	"net/http"

	"hop.top/kit/go/transport/api"
)

// goodBearer is the one credential verifyGood accepts.
const goodBearer = "Bearer good"

// verifyGood is an api.AuthFunc that accepts goodBearer as alice and
// refuses everything else.
func verifyGood(r *http.Request) (any, error) {
	if r.Header.Get("Authorization") != goodBearer {
		return nil, errors.New("bad credential")
	}
	return api.Claims{Subject: "alice", Tenant: "acme", Scopes: []string{"items:read"}}, nil
}

// verifyGoodOnly runs api.Auth(verifyGood) for a request presenting
// goodBearer and passes every other request through unverified, so one
// test server answers both a verified and an unverified caller.
func verifyGoodOnly(next http.Handler) http.Handler {
	verified := api.Auth(verifyGood)(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == goodBearer {
			verified.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
