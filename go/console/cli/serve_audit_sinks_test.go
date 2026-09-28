package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/cmdreflect"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/security"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
)

func TestServeAuditSinkConfigs_Resolution(t *testing.T) {
	isolateHome(t)
	v := viper.New()
	got, err := serveAuditSinkConfigs(v, "tool", APIServiceName)
	require.NoError(t, err)
	assert.Empty(t, got, "nothing configured adds nothing")

	// The bare type is an entry with every default.
	v.Set("services.all.audit.sinks", []string{"chain"})
	got, err = serveAuditSinkConfigs(v, "tool", APIServiceName)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, filepath.Join(os.Getenv("XDG_STATE_HOME"), "tool", "audit.chain"), got[0].path)
	assert.Equal(t, security.SyncNever, got[0].opts.Sync)
	assert.True(t, got[0].onOK)
	assert.True(t, got[0].onError)

	// The service's own list replaces the shared one.
	custom := filepath.Join(t.TempDir(), "api.chain")
	v.Set("services.api.audit.sinks", []any{map[string]any{
		"type": "chain", "path": custom, "fsync": "250ms",
		"max_bytes": 1024, "max_files": 3,
		"on": []any{"error"}, "surfaces": []any{"rest"}, "paths": []any{"list"},
	}})
	got, err = serveAuditSinkConfigs(v, "tool", APIServiceName)
	require.NoError(t, err)
	require.Len(t, got, 1)
	c := got[0]
	assert.Equal(t, custom, c.path)
	assert.Equal(t, security.SyncPeriodic, c.opts.Sync)
	assert.Equal(t, 250*time.Millisecond, c.opts.SyncInterval)
	assert.Equal(t, int64(1024), c.opts.MaxBytes)
	assert.Equal(t, 3, c.opts.MaxSegments)
	assert.False(t, c.onOK)
	assert.True(t, c.onError)
	assert.Equal(t, []cmdsurface.Surface{cmdsurface.SurfaceREST}, c.surfaces)
	assert.Equal(t, []string{"list"}, c.paths)

	got, err = serveAuditSinkConfigs(v, "tool", SocketServiceName)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].path, "audit.chain", "another service keeps the shared list")

	v.Set("services.api.audit.sinks", []any{map[string]any{"type": "chain", "fsync": "always"}})
	got, err = serveAuditSinkConfigs(v, "tool", APIServiceName)
	require.NoError(t, err)
	assert.Equal(t, security.SyncAlways, got[0].opts.Sync)
}

func TestServeAuditSinkConfigs_Refusals(t *testing.T) {
	isolateHome(t)
	cases := map[string]struct {
		value any
		want  string
	}{
		"unknown type":   {[]string{"file"}, `services.api.audit.sinks[0].type: unknown sink type "file"`},
		"unknown key":    {[]any{map[string]any{"type": "chain", "rotate": true}}, "unknown key rotate"},
		"bad fsync":      {[]any{map[string]any{"type": "chain", "fsync": "sometimes"}}, "services.api.audit.sinks[0].fsync"},
		"negative size":  {[]any{map[string]any{"type": "chain", "max_bytes": -1}}, "max_bytes"},
		"bad outcome":    {[]any{map[string]any{"type": "chain", "on": []any{"always"}}}, `unknown outcome "always"`},
		"bad surface":    {[]any{map[string]any{"type": "chain", "surfaces": []any{"carrier-pigeon"}}}, `unknown surface "carrier-pigeon"`},
		"not a list":     {map[string]any{"type": "chain"}, "want a list of sinks"},
		"entry not map":  {[]any{42}, "want a sink type or a map"},
		"type missing":   {[]any{map[string]any{"path": "/x"}}, `unknown sink type ""`},
		"negative files": {[]any{map[string]any{"type": "chain", "max_files": "-2"}}, "max_files"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			v := viper.New()
			v.Set("services.api.audit.sinks", tc.value)
			_, err := serveAuditSinkConfigs(v, "tool", APIServiceName)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// Services that name one file share one open log, so their records
// form one chain; a second entry naming it with other options is
// refused rather than silently ignored.
func TestServeConfiguredAuditSinks_SharedAcrossServices(t *testing.T) {
	isolateHome(t)
	r := authRoot(t)
	path := filepath.Join(t.TempDir(), "audit.chain")
	r.Viper.Set("services.all.audit.sinks", []any{map[string]any{"type": "chain", "path": path}})
	api, err := r.serveConfiguredAuditSinks(APIServiceName)
	require.NoError(t, err)
	sock, err := r.serveConfiguredAuditSinks(SocketServiceName)
	require.NoError(t, err)
	require.Len(t, api, 1)
	require.Len(t, sock, 1)
	assert.Same(t, api[0].Sink, sock[0].Sink)

	r.Viper.Set("services.socket.audit.sinks", []any{map[string]any{"type": "chain", "path": path, "fsync": "always"}})
	_, err = r.serveConfiguredAuditSinks(SocketServiceName)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already open with different")

	require.NoError(t, r.closeAuditChains())
	l, err := security.OpenAuditLog(path, security.AuditLogOptions{})
	require.NoError(t, err, "closing released the chain")
	require.NoError(t, l.Close())
}

// connectCmd echoes its secret flags, so a leak into the chain would
// come from either the flags or the output.
func connectCmd(t *testing.T) *cobra.Command {
	t.Helper()
	c := &cobra.Command{
		Use:         "connect",
		Short:       "connect to the database",
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			tok, _ := cmd.Flags().GetString("token")
			dsn, _ := cmd.Flags().GetString("dsn")
			cmd.Printf("token=%s dsn=%s", tok, dsn)
			return nil
		},
	}
	c.Flags().String("token", "", "api token")
	c.Flags().String("dsn", "", "database dsn")
	c.Flags().String("region", "", "region")
	require.NoError(t, cmdsurface.MarkFlagSecret(c.Flags(), "dsn"))
	return c
}

// chainRoot is a socket-serving root with the audit command and a chain
// configured for every service.
func chainRoot(t *testing.T, sock, chain string) *Root {
	t.Helper()
	r := authRoot(t, WithSocket(SocketConfig{Path: sock}), WithAuditCommand())
	r.Cmd.AddCommand(connectCmd(t))
	r.Viper.Set("services.all.audit.sinks", []any{map[string]any{"type": "chain", "path": chain}})
	return r
}

// runVerify runs `tool audit verify` on a fresh root and returns its
// stdout and error.
func runVerify(t *testing.T, sock, chain string, args ...string) (string, error) {
	t.Helper()
	r := chainRoot(t, sock, chain)
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.SetArgs(append([]string{"audit", "verify"}, args...))
	err := r.Execute(t.Context())
	return out.String(), err
}

// A served call lands in the chain redacted; verify passes on the
// untouched chain and names the break once a record is edited.
func TestSocketAuditChain_RedactedThenVerified(t *testing.T) {
	isolateHome(t)
	sock := tmpSocket(t)
	chain := filepath.Join(t.TempDir(), "audit.chain")
	r := chainRoot(t, sock, chain)

	stop := serveSocket(t, r, sock)
	const token, dsn = "s3cr3t-tok-1", "postgres://u:pw-9f2a@db/x"
	for range 3 {
		resp := socketCall(t, sock, socket.Request{
			Path:  []string{"connect"},
			Flags: map[string]any{"token": token, "dsn": dsn, "region": "eu-west-1"},
		})
		require.True(t, resp.Ok, "%+v", resp.Error)
	}
	stop()

	raw, err := os.ReadFile(chain)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), token)
	assert.NotContains(t, string(raw), dsn)
	assert.Contains(t, string(raw), `"region":"eu-west-1"`)
	assert.Contains(t, string(raw), `"surface":"socket"`)

	out, err := runVerify(t, sock, chain, "--format", "json")
	require.NoError(t, err)
	assert.Contains(t, out, `"status": "ok"`)
	assert.Contains(t, out, `"records": 3`)

	// Edit the second record's region.
	edited := strings.Replace(string(raw), `"region":"eu-west-1"`, `"region":"us-east-1"`, 2)
	edited = strings.Replace(edited, `"region":"us-east-1"`, `"region":"eu-west-1"`, 1)
	require.NoError(t, os.WriteFile(chain, []byte(edited), 0o600))

	out, err = runVerify(t, sock, chain, "--format", "json")
	var oe *output.Error
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, security.CodeTamperDetected, oe.Code)
	assert.Equal(t, security.ExitTamperDetected, oe.ExitCode)
	assert.Equal(t, output.TransiencePermanent, oe.Transience)
	assert.Contains(t, oe.Message, chain+":2:")
	assert.Contains(t, out, `"status": "tampered"`)
}

// A chain another process holds fails the socket service at
// validation, before it serves a single unaudited call.
func TestSocketAuditChain_BusyChainRefusedAtValidate(t *testing.T) {
	isolateHome(t)
	sock := tmpSocket(t)
	chain := filepath.Join(t.TempDir(), "audit.chain")
	holder, err := security.OpenAuditLog(chain, security.AuditLogOptions{})
	require.NoError(t, err)
	defer holder.Close()

	r := chainRoot(t, sock, chain)
	err = runServeExpect(t, r, []string{"serve", "socket"}, 2*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open in another process")
}

func TestSocketAuditChain_BadSinkRefusedAtValidate(t *testing.T) {
	isolateHome(t)
	sock := tmpSocket(t)
	r := authRoot(t, WithSocket(SocketConfig{Path: sock}))
	r.Viper.Set("services.all.audit.sinks", []string{"file"})
	oe := usageErr(t, runServeExpect(t, r, []string{"serve", "socket"}, 2*time.Second))
	assert.Contains(t, oe.Message, `unknown sink type "file"`)
}

// The registry checks the list's shape under every service, so a
// malformed list for a service not being served is refused too.
func TestAuditSinks_ShapeRefusedForEveryService(t *testing.T) {
	isolateHome(t)
	sock := tmpSocket(t)
	r := authRoot(t, WithSocket(SocketConfig{Path: sock}))
	r.Viper.Set("services.api.audit.sinks", []any{map[string]any{"type": "chain", "rotate": true}})
	oe := usageErr(t, runServeExpect(t, r, []string{"serve", "socket"}, 2*time.Second))
	assert.Contains(t, oe.Message, `services.api.audit.sinks[0]: unknown key rotate`)
}

func TestAuditVerify_NotFound(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAuditCommand())
	r.SetArgs([]string{"audit", "verify"})
	var oe *output.Error
	require.ErrorAs(t, r.Execute(t.Context()), &oe)
	assert.Equal(t, output.ExitNotFound, oe.ExitCode, oe.Message)

	_, err := runVerify(t, tmpSocket(t), filepath.Join(t.TempDir(), "absent.chain"))
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, output.ExitNotFound, oe.ExitCode, oe.Message)
}

// Mounted by cli.New, audit is kit-reserved and every served surface
// describes `audit verify` as management-only.
func TestAuditVerify_ManagementOnly(t *testing.T) {
	r := authRoot(t, WithAuditCommand())
	assert.True(t, r.IsReserved("audit"))
	tree := cmdreflect.Reflect(r.Cmd, cmdreflect.WithReserved(r))
	var found bool
	for _, d := range tree.Descriptors {
		if strings.Join(d.Path[1:], " ") != "audit verify" {
			continue
		}
		found = true
		assert.False(t, d.Invocable)
		assert.Equal(t, cmdreflect.ReasonManagementOnly, d.Reason)
	}
	assert.True(t, found, "audit verify must be described")
}

// A service outside this package (mcp, rpc) gets its audit.sinks
// through ServeBridgeOptions and has them checked and opened by
// ValidateServeBridge. Should the options still fail at Start, the
// ones returned refuse every call: never a bridge without its gate.
func TestServeBridgeOptions_AuditSinksForOutOfPackageServices(t *testing.T) {
	isolateHome(t)
	r := authRoot(t)
	r.Cmd.AddCommand(connectCmd(t))
	path := filepath.Join(t.TempDir(), "audit.chain")
	r.Viper.Set("services.mcp.audit.sinks", []any{map[string]any{"type": "chain", "path": path}})
	require.NoError(t, ValidateServeBridge(r, "mcp"))

	opts, err := ServeBridgeOptions(r, "mcp")
	require.NoError(t, err)
	b := cmdsurface.New(r.Cmd, opts...)
	b.Expose("*", cmdsurface.SurfaceMCP)
	_, _ = b.Invoke(t.Context(), cmdsurface.Invocation{
		Path: []string{"connect"}, Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceMCP},
	})
	require.NoError(t, r.closeAuditChains())
	rep, err := security.VerifyAuditLog(path)
	require.NoError(t, err)
	assert.Nil(t, rep.Break)
	assert.Equal(t, uint64(1), rep.Records, "the mcp call is chained")

	r.Viper.Set("services.rpc.audit.sinks", []string{"file"})
	err = ValidateServeBridge(r, "rpc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `services.rpc.audit.sinks[0].type`)

	opts, err = ServeBridgeOptions(r, "rpc")
	require.Error(t, err)
	b = cmdsurface.New(r.Cmd, opts...)
	b.Expose("*", cmdsurface.SurfaceRPC)
	_, err = b.Invoke(t.Context(), cmdsurface.Invocation{
		Path: []string{"connect"}, Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceRPC},
	})
	assert.ErrorIs(t, err, cmdsurface.ErrPermissionDenied, "a bridge whose options failed refuses every call")
}
