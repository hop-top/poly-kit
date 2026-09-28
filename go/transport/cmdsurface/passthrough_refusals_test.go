package cmdsurface

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Passthrough surfaces (bus, webhook, OAuth callback, signed URL,
// Lambda) bind a leaf the server chose, so each answers the bridge's
// refusals in its own vocabulary. These tests pin the two refusals
// that sit beside the destructive ceiling: the code is always the
// sentinel's own (permission_denied, not_invocable), never the
// catch-all. A permission refusal is 403 on every HTTP passthrough; a
// non-invocable leaf takes the status the surface gives a binding it
// can never run (500 where the server configured the binding, as for
// unknown_command there; 404 on the signed URL, which shares the REST
// writer).

var (
	errPassthroughDenied       = fmt.Errorf("%w: locked on x: off limits", ErrPermissionDenied)
	errPassthroughNotInvocable = fmt.Errorf("%w: shell on x is interactive", ErrNotInvocable)
)

func TestPassthroughRefusals_Bus(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{errPassthroughDenied, "permission_denied"},
		{errPassthroughNotInvocable, "not_invocable"},
	} {
		if got := bridgeErrorCode(tc.err); got != tc.code {
			t.Errorf("bridgeErrorCode(%v) = %q, want %q", tc.err, got, tc.code)
		}
	}
}

func TestPassthroughRefusals_Lambda(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{errPassthroughDenied, http.StatusForbidden, "permission_denied"},
		{errPassthroughNotInvocable, http.StatusInternalServerError, "not_invocable"},
	} {
		status, code := lambdaHTTPErrorCode(tc.err)
		if status != tc.status || code != tc.code {
			t.Errorf("lambdaHTTPErrorCode(%v) = %d %q, want %d %q",
				tc.err, status, code, tc.status, tc.code)
		}
	}
}

// httpRefusal is one HTTP passthrough writer's expected answer.
type httpRefusal struct {
	err    error
	status int
	code   string
}

func checkHTTPRefusals(t *testing.T, write func(http.ResponseWriter, error), cases []httpRefusal) {
	t.Helper()
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		write(rec, tc.err)
		if rec.Code != tc.status {
			t.Errorf("%v: status = %d, want %d", tc.err, rec.Code, tc.status)
		}
		if body := rec.Body.String(); !strings.Contains(body, tc.code) {
			t.Errorf("%v: body %q lacks code %q", tc.err, body, tc.code)
		}
	}
}

func TestPassthroughRefusals_Webhook(t *testing.T) {
	checkHTTPRefusals(t, writeWebhookBridgeError, []httpRefusal{
		{errPassthroughDenied, http.StatusForbidden, "permission_denied"},
		{errPassthroughNotInvocable, http.StatusInternalServerError, "not_invocable"},
	})
}

func TestPassthroughRefusals_OAuth(t *testing.T) {
	write := func(w http.ResponseWriter, err error) {
		oauthWriteInvokeError(w, OAuthProvider{Name: "p"}, err)
	}
	checkHTTPRefusals(t, write, []httpRefusal{
		{errPassthroughDenied, http.StatusForbidden, "permission_denied"},
		{errPassthroughNotInvocable, http.StatusInternalServerError, "not_invocable"},
	})
}

// The signed URL verifier answers bridge refusals with the REST
// writer, so it inherits REST's statuses.
func TestPassthroughRefusals_Signed(t *testing.T) {
	checkHTTPRefusals(t, writeBridgeError, []httpRefusal{
		{errPassthroughDenied, http.StatusForbidden, "permission_denied"},
		{errPassthroughNotInvocable, http.StatusNotFound, "not_invocable"},
	})
}
