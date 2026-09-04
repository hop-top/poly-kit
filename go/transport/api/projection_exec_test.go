package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

func TestGetFlagsComeFromQuery(t *testing.T) {
	ex := &stubExecutor{}
	r := newProjectedRouter(t, ex)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/commands/list?limit=25&all=true&filter=name&arg=one&arg=two", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	// Values are coerced to their declared types, not passed as the
	// raw strings a query string produces.
	assert.Equal(t, int64(25), ex.got.Flags["limit"])
	assert.Equal(t, true, ex.got.Flags["all"])
	assert.Equal(t, "name", ex.got.Flags["filter"])
	assert.Equal(t, []string{"one", "two"}, ex.got.Args)
	assert.Equal(t, []string{"list"}, ex.got.Path)
}

func TestBareBoolFlagIsTrue(t *testing.T) {
	// "?all" with no value is the query spelling of "--all".
	ex := &stubExecutor{}
	r := newProjectedRouter(t, ex)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/commands/list?all&filter=x", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, true, ex.got.Flags["all"])
}

func TestPostFlagsComeFromBody(t *testing.T) {
	ex := &stubExecutor{}
	r := newProjectedRouter(t, ex)

	body := `{"flags":{"force":true},"args":["gadget","a note"]}`
	req := httptest.NewRequest(http.MethodPost,
		"/v1/commands/widget/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, true, ex.got.Flags["force"])
	assert.Equal(t, []string{"gadget", "a note"}, ex.got.Args)
	assert.Equal(t, []string{"widget", "add"}, ex.got.Path)
}

func TestBodyWinsOverQueryFlags(t *testing.T) {
	ex := &stubExecutor{}
	r := newProjectedRouter(t, ex)

	req := httptest.NewRequest(http.MethodPost,
		"/v1/commands/widget/add?force=false",
		strings.NewReader(`{"flags":{"force":true},"args":["g"]}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, true, ex.got.Flags["force"],
		"body is the more explicit statement and must win")
}

func TestUnknownFlagIsRejected(t *testing.T) {
	ex := &stubExecutor{}
	r := newProjectedRouter(t, ex)

	req := httptest.NewRequest(http.MethodPost,
		"/v1/commands/widget/add",
		strings.NewReader(`{"flags":{"nope":1},"args":["g"]}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "nope",
		"the error must name the offending flag")
	assert.Zero(t, ex.calls)
}

func TestUndeclaredQueryFlagIsIgnored(t *testing.T) {
	// A stray query parameter on a GET is dropped rather than
	// forwarded: query strings collect junk (tracking params,
	// cache-busters) that must not reach the command.
	ex := &stubExecutor{}
	r := newProjectedRouter(t, ex)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/commands/list?filter=x&utm_source=mail", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, ex.got.Flags, "utm_source")
}

func TestMalformedFlagValueIsBadRequest(t *testing.T) {
	ex := &stubExecutor{}
	r := newProjectedRouter(t, ex)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/commands/list?limit=many&filter=x", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Zero(t, ex.calls, "a bad value must not reach the command")
}

func TestMissingRequiredArgIsBadRequest(t *testing.T) {
	ex := &stubExecutor{}
	r := newProjectedRouter(t, ex)

	req := httptest.NewRequest(http.MethodPost,
		"/v1/commands/widget/add", strings.NewReader(`{"flags":{}}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "name",
		"the error must name the missing argument")
	assert.Zero(t, ex.calls)
}

func TestConfirmTokenHeaderReachesExecutor(t *testing.T) {
	ex := &stubExecutor{}
	r := newProjectedRouter(t, ex)

	req := httptest.NewRequest(http.MethodPost, "/v1/commands/widget/delete", nil)
	req.Header.Set(api.ConfirmTokenHeader, "tok-123")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "tok-123", ex.got.ConfirmToken)
}

func TestStructuredDataIsTheResponseBody(t *testing.T) {
	ex := &stubExecutor{result: api.CommandResult{
		Data: map[string]any{"id": "w-1", "count": float64(3)},
	}}
	r := newProjectedRouter(t, ex)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/commands/list?filter=x", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var got api.CommandResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, map[string]any{"id": "w-1", "count": float64(3)}, got.Data)
}

func TestExitCodeToStatusTable(t *testing.T) {
	// The mapping is contract. Flipping a row is the mutation that
	// must turn this red.
	cases := map[int]int{
		0:  http.StatusOK,
		1:  http.StatusInternalServerError,
		2:  http.StatusBadRequest,
		3:  http.StatusNotFound,
		4:  http.StatusConflict,
		5:  http.StatusForbidden,
		6:  http.StatusServiceUnavailable,
		64: http.StatusTooManyRequests,
		65: http.StatusUnprocessableEntity,
		// Undefined codes are unclassified failures.
		99: http.StatusInternalServerError,
	}
	for exit, want := range cases {
		assert.Equal(t, want, api.StatusForExitCode(exit),
			"exit %d", exit)
	}
}

func TestExitCodeDrivesResponseStatus(t *testing.T) {
	// End-to-end: the table is what the handler actually applies.
	for exit, want := range map[int]int{
		0: http.StatusOK,
		2: http.StatusBadRequest,
		3: http.StatusNotFound,
		5: http.StatusForbidden,
	} {
		ex := &stubExecutor{result: api.CommandResult{ExitCode: exit}}
		r := newProjectedRouter(t, ex)

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/v1/commands/list?filter=x", nil))
		assert.Equal(t, want, rec.Code, "exit %d", exit)
	}
}

func TestPolicyRefusalsMapToStableStatuses(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{
			name:   "interactive or withheld",
			err:    api.ErrCommandNotInvocable,
			status: http.StatusNotFound,
			code:   api.CodeNotInvocable,
		},
		{
			name:   "unauthorized destructive",
			err:    api.ErrDestructiveBlocked,
			status: http.StatusForbidden,
			code:   api.CodeDestructiveBlocked,
		},
		{
			name:   "confirmation required",
			err:    api.ErrConfirmationRequired,
			status: http.StatusPreconditionRequired,
			code:   api.CodeConfirmationRequired,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ex := &stubExecutor{err: c.err}
			r := newProjectedRouter(t, ex)

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
				"/v1/commands/list?filter=x", nil))

			assert.Equal(t, c.status, rec.Code)
			var ae api.APIError
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ae))
			assert.Equal(t, c.code, ae.Code)
		})
	}
}

func TestNotInvocableRefusalCarriesReason(t *testing.T) {
	// The status alone cannot distinguish "no such command" from
	// "withheld"; the body must carry the stable reason.
	descs := []api.CommandDescriptor{{
		Path:       []string{"list"},
		SideEffect: api.SideEffectRead,
		Invocable:  true,
		Reason:     "",
	}}
	descs[0].Reason = "unauthorized-destructive"

	r := api.NewRouter()
	api.MountCommandProjection(r, api.ProjectionConfig{
		Descriptors: descs,
		Executor:    &stubExecutor{err: api.ErrCommandNotInvocable},
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands/list", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "unauthorized-destructive")
}

func TestUnmappedExecutorErrorIsInternal(t *testing.T) {
	ex := &stubExecutor{err: errors.New("boom")}
	r := newProjectedRouter(t, ex)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/commands/list?filter=x", nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
