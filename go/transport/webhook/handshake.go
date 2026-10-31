package webhook

import (
	"net/http"
)

// Handshake answers a sender's subscription-validation request. It
// runs before verification: such requests carry no signature.
type Handshake interface {
	// Handle answers the request and returns true, or returns false
	// having written nothing.
	Handle(w http.ResponseWriter, r *http.Request, body []byte) bool
}

// HandshakeFunc adapts a function to [Handshake].
type HandshakeFunc func(w http.ResponseWriter, r *http.Request, body []byte) bool

// Handle implements [Handshake].
func (f HandshakeFunc) Handle(w http.ResponseWriter, r *http.Request, body []byte) bool {
	return f(w, r, body)
}

// maxValidationToken bounds the echoed token.
const maxValidationToken = 2048

// GraphValidationToken answers Microsoft Graph's subscription
// validation: a request with a validationToken query parameter is
// answered 200 text/plain with the token, URL-decoded, as the
// subscription handshake requires (within 10 seconds, the body the
// token alone). An overlong token is refused 400.
func GraphValidationToken() Handshake {
	return HandshakeFunc(func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		token := r.URL.Query().Get("validationToken")
		if token == "" {
			return false
		}
		h := w.Header()
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("X-Content-Type-Options", "nosniff")
		if len(token) > maxValidationToken {
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(token))
		return true
	})
}
