package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// addWait adds a read command that waits --for, returning early when
// its context ends; ann is merged into its annotations.
func addWait(r *Root, use string, ann map[string]string) {
	c := &cobra.Command{
		Use:         use,
		Short:       "wait a while",
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, _ := cmd.Flags().GetDuration("for")
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "started")
			select {
			case <-cmd.Context().Done():
				return cmd.Context().Err()
			case <-time.After(d):
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "finished")
				return nil
			}
		},
	}
	for k, v := range ann {
		c.Annotations[k] = v
	}
	c.Flags().Duration("for", 10*time.Second, "how long to wait")
	r.Cmd.AddCommand(c)
}

func TestServeServerTimeoutsResolution(t *testing.T) {
	// Per key, specificity before source: services.<svc>, then
	// services.all, then the service's code default.
	def := api.DefaultServerTimeouts()
	cases := []struct {
		name string
		keys map[string]any
		want api.ServerTimeouts
	}{
		{"kit default", nil, def},
		{"services.all", map[string]any{"services.all.timeouts.read": "30s"},
			api.ServerTimeouts{ReadHeader: 5 * time.Second, Read: 30 * time.Second, Write: 10 * time.Second}},
		{"service over services.all", map[string]any{
			"services.all.timeouts.write": "1m",
			"services.api.timeouts.write": "20s",
		}, api.ServerTimeouts{ReadHeader: 5 * time.Second, Read: 5 * time.Second, Write: 20 * time.Second}},
		{"keys merge one by one", map[string]any{
			"services.all.timeouts.idle":        "2m",
			"services.api.timeouts.read_header": "2s",
		}, api.ServerTimeouts{ReadHeader: 2 * time.Second, Read: 5 * time.Second, Write: 10 * time.Second, Idle: 2 * time.Minute}},
		{"zero disables", map[string]any{"services.api.timeouts.write": 0, "services.api.timeouts.read": "0"},
			api.ServerTimeouts{ReadHeader: 5 * time.Second}},
		{"a duration value", map[string]any{"services.api.timeouts.read": 7 * time.Second},
			api.ServerTimeouts{ReadHeader: 5 * time.Second, Read: 7 * time.Second, Write: 10 * time.Second}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateHome(t)
			r := authRoot(t, WithAPI(APIConfig{}))
			for k, v := range c.keys {
				r.Viper.Set(k, v)
			}
			got, err := serveServerTimeouts(r.Viper, APIServiceName, def)
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestServeCommandTimeoutResolution(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{}))
	got, err := serveCommandTimeout(r.Viper, APIServiceName)
	require.NoError(t, err)
	assert.Zero(t, got, "no command deadline by default")

	r.Viper.Set("services.all.timeouts.command", "1m")
	got, err = serveCommandTimeout(r.Viper, APIServiceName)
	require.NoError(t, err)
	assert.Equal(t, time.Minute, got)

	r.Viper.Set("services.api.timeouts.command", "30s")
	got, err = serveCommandTimeout(r.Viper, APIServiceName)
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, got)
}

func TestServeTimeoutsValidation(t *testing.T) {
	cases := map[string]any{
		"services.api.timeouts.read":        "soon",
		"services.api.timeouts.write":       5,
		"services.api.timeouts.idle":        "-1s",
		"services.api.timeouts.command":     true,
		"services.all.timeouts.read_header": "5 seconds",
		"services.api.timeouts.reads":       "5s",
		"services.all.timeouts.deadline":    "5s",
	}
	for key, bad := range cases {
		t.Run(key, func(t *testing.T) {
			isolateHome(t)
			r := authRoot(t, WithAPI(APIConfig{}))
			r.Viper.Set(key, bad)
			err := apiSvc(t, r).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), key)
		})
	}
}

func TestServeTimeoutsRefusedAtExit2(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	r.Viper.Set("services.api.timeouts.write", "soon")
	err := runServeExpect(t, r, []string{"serve", "api"}, 2*time.Second)
	var oe *output.Error
	require.True(t, errors.As(err, &oe), "err = %v", err)
	assert.Equal(t, 2, oe.ExitCode)
	assert.Contains(t, oe.Error(), "services.api.timeouts.write")
}

func TestServeTimeoutsRefuseAMalformedAnnotation(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{}))
	addWait(r, "wait", map[string]string{cmdsurface.AnnotationTimeout: "soon"})
	err := apiSvc(t, r).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kit/timeout")
	assert.Contains(t, err.Error(), "wait")
}

func TestAPIServiceAppliesServerTimeouts(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	r.Viper.Set("services.api.timeouts.read_header", "1s")
	r.Viper.Set("services.api.timeouts.read", "2s")
	r.Viper.Set("services.all.timeouts.write", "3s")
	r.Viper.Set("services.api.timeouts.idle", "4s")
	_, stop := serveAPI(t, r)
	defer stop()

	a := apiSvc(t, r)
	a.mu.Lock()
	srv := a.srv
	a.mu.Unlock()
	require.NotNil(t, srv)
	assert.Equal(t, api.ServerTimeouts{
		ReadHeader: time.Second, Read: 2 * time.Second, Write: 3 * time.Second, Idle: 4 * time.Second,
	}, api.ServerTimeoutsOf(srv))
}

func TestAPIServiceDefaultServerTimeouts(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	_, stop := serveAPI(t, r)
	defer stop()

	a := apiSvc(t, r)
	a.mu.Lock()
	srv := a.srv
	a.mu.Unlock()
	assert.Equal(t, api.DefaultServerTimeouts(), api.ServerTimeoutsOf(srv))
}

func requireDeadlineExceeded(t *testing.T, status int, body []byte) {
	t.Helper()
	require.Equal(t, http.StatusGatewayTimeout, status, string(body))
	var e api.APIError
	require.NoError(t, json.Unmarshal(body, &e), string(body))
	assert.Equal(t, api.CodeDeadlineExceeded, e.Code)
	assert.Equal(t, http.StatusGatewayTimeout, e.Status)
}

func TestAPIServiceCommandDeadline(t *testing.T) {
	for name, setup := range map[string]func(*Root){
		"timeouts.command": func(r *Root) {
			addWait(r, "wait", nil)
			r.Viper.Set("services.api.timeouts.command", "100ms")
		},
		"kit/timeout": func(r *Root) {
			addWait(r, "wait", map[string]string{cmdsurface.AnnotationTimeout: "100ms"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			isolateHome(t)
			rec := &auditRecorder{}
			r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), WithAuditSinks(rec.spec()))
			setup(r)
			base, stop := serveAPI(t, r)
			defer stop()

			start := time.Now()
			resp, body := doStream(t, http.MethodGet, base+"/v1/commands/wait", "", nil)
			requireDeadlineExceeded(t, resp.StatusCode, body)
			assert.Less(t, time.Since(start), 5*time.Second, "the deadline canceled the command")
			assert.Contains(t, string(body), "wait ran past its 100ms deadline")

			_, _, err := rec.last(t)
			assert.ErrorIs(t, err, cmdsurface.ErrDeadlineExceeded, "the audit record carries the class")

			// A call inside the deadline is untouched.
			resp, body = doStream(t, http.MethodGet, base+"/v1/commands/wait?for=10ms", "", nil)
			assert.Equal(t, http.StatusOK, resp.StatusCode, string(body))

			// The stream route ends with an error frame carrying the
			// same status and code.
			resp, body = doStream(t, http.MethodGet, base+"/v1/commands/wait/stream", "", nil)
			require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
			frames := readStream(t, strings.NewReader(string(body)))
			require.NotEmpty(t, frames, string(body))
			last := frames[len(frames)-1]
			require.Equal(t, api.SSEEventError, last.event, string(body))
			var e api.APIError
			require.NoError(t, json.Unmarshal([]byte(last.data), &e))
			assert.Equal(t, api.CodeDeadlineExceeded, e.Code)
			assert.Equal(t, http.StatusGatewayTimeout, e.Status)
		})
	}
}
