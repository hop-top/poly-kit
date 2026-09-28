package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// CodeInsufficientScope is the refusal code of the built-in scope
// check: the caller is known, and its credential lacks a scope the
// command declares under kit/permissions. It is answered 403 with a
// WWW-Authenticate challenge naming the scope the command needs (see
// [WriteInsufficientScope]).
const CodeInsufficientScope = "insufficient_scope"

// ErrInsufficientScope reports that the permission gate's scope
// check refused the call. It wraps [ErrPermissionDenied], so code
// that predates the class still answers it 403. The projection
// answers it 403 [CodeInsufficientScope] with the scope challenge
// when the error carries the scopes the command requires: a value in
// its chain with a RequiredScopes() []string method, as the cmdsurface
// bridge's refusal has.
var ErrInsufficientScope error = insufficientScope{}

// insufficientScope is [ErrInsufficientScope]'s type: a sentinel of
// its own spelling that still unwraps to [ErrPermissionDenied].
type insufficientScope struct{}

func (insufficientScope) Error() string { return "api: insufficient scope" }
func (insufficientScope) Unwrap() error { return ErrPermissionDenied }

// InsufficientScopeChallenge returns the RFC 6750 WWW-Authenticate
// challenge for a caller whose token lacks scope: the bearer scheme,
// error="insufficient_scope", and the space-delimited scopes a token
// needs for this request. A scope that is not a valid RFC 6749
// scope-token is left out rather than quoted; with none left the
// challenge carries no scope attribute.
func InsufficientScopeChallenge(scopes []string) string {
	valid := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if isScopeToken(s) {
			valid = append(valid, s)
		}
	}
	challenge := DefaultAuthChallenge + ` error="` + CodeInsufficientScope + `"`
	if len(valid) > 0 {
		challenge += `, scope="` + strings.Join(valid, " ") + `"`
	}
	return challenge
}

// ScopeChallenge is [InsufficientScopeChallenge] for a request r: when
// a [ProtectedResource.Guard] passed r, the challenge also names the
// resource's metadata document (resource_metadata, RFC 9728 §5.1), as
// the MCP authorization spec asks of a 403 insufficient_scope, so a
// client stepping up its authorization finds the authorization server
// from the refusal alone.
func ScopeChallenge(r *http.Request, scopes []string) string {
	challenge := InsufficientScopeChallenge(scopes)
	if r == nil {
		return challenge
	}
	if p := ProtectedResourceFrom(r.Context()); p != nil {
		challenge += fmt.Sprintf(", resource_metadata=%q", p.MetadataURL())
	}
	return challenge
}

// BearerPresented reports whether r carries a bearer credential: an
// Authorization header of the Bearer scheme (RFC 6750 §2.1). A
// refusal a new token could lift — insufficient_scope — is answered
// with a bearer challenge only to a caller who presented one.
func BearerPresented(r *http.Request) bool {
	if r == nil {
		return false
	}
	scheme, _, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	return ok && strings.EqualFold(scheme, "Bearer")
}

// WriteInsufficientScope answers w with 403 [CodeInsufficientScope]
// and the WWW-Authenticate challenge [InsufficientScopeChallenge]
// builds from scopes, as every kit HTTP surface answers the class. A
// client holding a token without the scope reads the header to ask
// its authorization server for one that has it.
func WriteInsufficientScope(w http.ResponseWriter, scopes []string, msg string) {
	writeInsufficientScope(w, InsufficientScopeChallenge(scopes), msg)
}

// writeScopeRefusal is [WriteInsufficientScope] for a request r: behind
// a protected resource the challenge also names its metadata document
// ([ScopeChallenge]).
func writeScopeRefusal(w http.ResponseWriter, r *http.Request, scopes []string, msg string) {
	writeInsufficientScope(w, ScopeChallenge(r, scopes), msg)
}

// writeInsufficientScope answers w with 403 [CodeInsufficientScope]
// under challenge.
func writeInsufficientScope(w http.ResponseWriter, challenge, msg string) {
	w.Header().Set("WWW-Authenticate", challenge)
	Error(w, http.StatusForbidden, &APIError{
		Status:  http.StatusForbidden,
		Code:    CodeInsufficientScope,
		Message: msg,
	})
}

// requiredScopesOf returns the scopes err says the refused command
// requires, if it carries them.
func requiredScopesOf(err error) []string {
	var hint interface{ RequiredScopes() []string }
	if errors.As(err, &hint) {
		return hint.RequiredScopes()
	}
	return nil
}

// isScopeToken reports whether s is an RFC 6749 scope-token:
// 1*( %x21 / %x23-5B / %x5D-7E ), printable ASCII without space,
// double quote or backslash.
func isScopeToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}
