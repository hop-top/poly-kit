package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
)

func TestServeAuditRedaction_Resolution(t *testing.T) {
	v := viper.New()
	got, err := serveAuditRedaction(v, APIServiceName)
	require.NoError(t, err)
	assert.Empty(t, got.SecretFlags, "nothing configured adds nothing")
	assert.Empty(t, got.Rules)

	v.Set("services.all.audit.redact.secret_flags", []string{"dsn"})
	v.Set("services.all.audit.redact.patterns", []string{`acme_[a-z0-9]{12}`})
	got, err = serveAuditRedaction(v, APIServiceName)
	require.NoError(t, err)
	assert.Equal(t, []string{"dsn"}, got.SecretFlags, "services.all applies to a service with no key of its own")
	require.Len(t, got.Rules, 1)

	// The service's key wins, per key: its list replaces the shared
	// one, and the key it does not set still comes from services.all.
	v.Set("services.api.audit.redact.secret_flags", []string{"conn"})
	got, err = serveAuditRedaction(v, APIServiceName)
	require.NoError(t, err)
	assert.Equal(t, []string{"conn"}, got.SecretFlags)
	assert.Len(t, got.Rules, 1)

	got, err = serveAuditRedaction(v, SocketServiceName)
	require.NoError(t, err)
	assert.Equal(t, []string{"dsn"}, got.SecretFlags, "another service keeps the shared value")
}

func TestServeAuditRedaction_RefusesUnknownKeysAndBadPatterns(t *testing.T) {
	v := viper.New()
	v.Set("services.api.audit.redact.enabled", false)
	_, err := serveAuditRedaction(v, APIServiceName)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `services.api.audit.redact.enabled: unknown key "enabled"`)
	assert.Contains(t, err.Error(), "cannot be switched off")

	v = viper.New()
	v.Set("services.all.audit.redact.patterns", []string{"ok", "(unclosed"})
	_, err = serveAuditRedaction(v, SocketServiceName)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "services.all.audit.redact.patterns[1]")
}

// A secret passed to a served command reaches the audit sink masked:
// by name (--token), by annotation (--dsn), and by the operator's
// audit.redact block (--conn), including the command's echo of it.
func TestSocketAuditRedactsSecretFlags(t *testing.T) {
	isolateHome(t)
	rec := &auditRecorder{}
	path := tmpSocket(t)
	r := authRoot(t, WithSocket(SocketConfig{Path: path}), WithAuditSinks(rec.spec()))
	connect := &cobra.Command{
		Use:         "connect",
		Short:       "connect to the database",
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn, _ := cmd.Flags().GetString("dsn")
			conn, _ := cmd.Flags().GetString("conn")
			cmd.Printf("dsn=%s conn=%s", dsn, conn)
			return nil
		},
	}
	connect.Flags().String("token", "", "api token")
	connect.Flags().String("dsn", "", "database dsn")
	connect.Flags().String("conn", "", "connection string")
	connect.Flags().String("region", "", "region")
	require.NoError(t, cmdsurface.MarkFlagSecret(connect.Flags(), "dsn"))
	r.Cmd.AddCommand(connect)
	r.Viper.Set("services.socket.audit.redact.secret_flags", []string{"conn"})

	stop := serveSocket(t, r, path)
	defer stop()

	const token, dsn, conn = "s3cr3t-tok-1", "postgres://u:pw-9f2a@db/x", "host=db password=pw-77aa"
	resp := socketCall(t, path, socket.Request{
		Path:  []string{"connect"},
		Flags: map[string]any{"token": token, "dsn": dsn, "conn": conn, "region": "eu-west-1"},
	})
	require.True(t, resp.Ok, "%+v", resp.Error)

	inv, res, _ := rec.last(t)
	assert.Equal(t, "eu-west-1", inv.Flags["region"])
	for _, secret := range []string{token, dsn, conn} {
		for k, v := range inv.Flags {
			assert.NotContains(t, v, secret, "flag %s", k)
		}
		assert.NotContains(t, res.Stdout, secret)
	}
}

func TestSocketAuditRedactRefusedAtValidate(t *testing.T) {
	isolateHome(t)
	path := tmpSocket(t)
	r := authRoot(t, WithSocket(SocketConfig{Path: path}))
	r.Viper.Set("services.all.audit.redact.enabled", false)
	oe := usageErr(t, runServeExpect(t, r, []string{"serve", "socket"}, 2*time.Second))
	assert.True(t, strings.Contains(oe.Message, `services.all.audit.redact.enabled: unknown key "enabled"`), oe.Message)
}

// A service outside this package (mcp, rpc) gets its own audit.redact
// block through ServeBridgeOptions, and ValidateServeBridge refuses a
// block the options would refuse: a service whose bridge options
// fail to build must never start without its permission gate.
func TestServeBridgeOptionsCarryTheServiceRedaction(t *testing.T) {
	isolateHome(t)
	rec := &auditRecorder{}
	r := authRoot(t, WithAuditSinks(rec.spec()))
	connect := &cobra.Command{
		Use:         "connect",
		Short:       "connect to the database",
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE:        func(*cobra.Command, []string) error { return nil },
	}
	connect.Flags().String("conn", "", "connection string")
	r.Cmd.AddCommand(connect)
	r.Viper.Set("services.mcp.audit.redact.secret_flags", []string{"conn"})

	opts, err := ServeBridgeOptions(r, "mcp")
	require.NoError(t, err)
	b := cmdsurface.New(r.Cmd, opts...)
	b.Expose("*", cmdsurface.SurfaceMCP)
	const conn = "host=db password=pw-77aa"
	_, _ = b.Invoke(t.Context(), cmdsurface.Invocation{
		Path:  []string{"connect"},
		Flags: map[string]any{"conn": conn},
		Meta:  cmdsurface.Meta{Surface: cmdsurface.SurfaceMCP},
	})
	inv, _, _ := rec.last(t)
	assert.NotContains(t, inv.Flags["conn"], conn, "services.mcp.audit.redact applies to the mcp bridge")

	require.NoError(t, ValidateServeBridge(r, "mcp"))
	r.Viper.Set("services.rpc.audit.redact.patterns", []string{"(unclosed"})
	err = ValidateServeBridge(r, "rpc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "services.rpc.audit.redact.patterns[0]")
	_, err = ServeBridgeOptions(r, "rpc")
	require.Error(t, err)
}
