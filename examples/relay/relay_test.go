package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/transport/authn"
)

func newTestRoot(t *testing.T) *cli.Root {
	t.Helper()
	return newRoot(cli.Config{Name: "relay", Version: "0.1.0", Short: "test", DisableValidate: true},
		cli.IdentityConfig{Dir: filepath.Join(t.TempDir(), "id")})
}

func TestCommandsExist(t *testing.T) {
	root := newTestRoot(t)
	b := bus.New()
	defer b.Close(context.Background())
	addCommands(root, b)

	names := map[string]bool{}
	for _, c := range root.Cmd.Commands() {
		names[c.Name()] = true
	}
	for _, name := range []string{"listen", "connect", "publish", "subscribe", "pubkey", "token"} {
		assert.True(t, names[name], "command %q should exist", name)
	}
	listen, _, err := root.Cmd.Find([]string{"listen"})
	require.NoError(t, err)
	assert.NotNil(t, listen.Flags().Lookup("trust"))
	assert.NotNil(t, listen.Flags().Lookup("allow-anonymous"))
	connect, _, err := root.Cmd.Find([]string{"connect"})
	require.NoError(t, err)
	assert.NotNil(t, connect.Flags().Lookup("auth"))
	create, _, err := root.Cmd.Find([]string{"token", "create"})
	require.NoError(t, err)
	assert.Equal(t, "create", create.Name(), "the identity mounts the kit's self-signed token leaves")
}

func TestBusRoundtrip(t *testing.T) {
	b := bus.New()
	defer b.Close(context.Background())

	received := make(chan bus.Event, 1)
	unsub := b.Subscribe("test.#", func(_ context.Context, e bus.Event) error {
		received <- e
		return nil
	})
	defer unsub()

	e := bus.NewEvent("test.hello", "test", "world")
	require.NoError(t, b.Publish(context.Background(), e))

	select {
	case got := <-received:
		assert.Equal(t, bus.Topic("test.hello"), got.Topic)
		assert.Equal(t, "world", got.Payload)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func genKey(t *testing.T) *identity.Keypair {
	t.Helper()
	kp, err := identity.Generate()
	require.NoError(t, err)
	return kp
}

// TestRelayAuthSelfSignedTokens pins the relay's credential: a short
// lived token the connecting relay's identity signs, which a listener
// trusting that relay's public key accepts, and nothing else.
func TestRelayAuthSelfSignedTokens(t *testing.T) {
	a, other := genKey(t), genKey(t)
	tok, err := newRelayAuth(a).Token()
	require.NoError(t, err)

	claims, err := identity.VerifyJWT(tok, a.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, a.PublicKeyID(), claims.Subject)
	assert.Equal(t, relayIssuer, claims.Issuer)
	assert.LessOrEqual(t, claims.ExpiresAt, time.Now().Add(relayTokenTTL).Unix())

	listener := newRelayAuth(nil, a.PublicKey)
	assert.NoError(t, listener.Verify(tok))

	expired, err := a.SignJWT(identity.Claims{Subject: a.PublicKeyID(), Issuer: relayIssuer,
		IssuedAt: time.Now().Add(-time.Hour).Unix(), ExpiresAt: time.Now().Add(-time.Minute).Unix()})
	require.NoError(t, err)
	noExpiry, err := a.SignJWT(identity.Claims{Subject: a.PublicKeyID(), Issuer: relayIssuer, IssuedAt: time.Now().Unix()})
	require.NoError(t, err)
	fromOther, err := newRelayAuth(other).Token()
	require.NoError(t, err)
	parts := strings.Split(tok, ".")
	parts[2] = base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	for name, bad := range map[string]string{
		"another key": fromOther,
		"tampered":    strings.Join(parts, "."),
		"expired":     expired,
		"no expiry":   noExpiry,
		"garbage":     "x.y.z",
	} {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, listener.Verify(bad), bus.ErrAuthFailed)
		})
	}

	_, err = newRelayAuth(nil, a.PublicKey).Token()
	assert.Error(t, err, "a listener-only authenticator signs nothing")
	assert.ErrorIs(t, newRelayAuth(a).Verify(tok), bus.ErrAuthFailed, "trusting no key accepts nothing")
}

// TestRelayTokenIsTheOneAServedToolAccepts pins that the relay's token
// is the self-signed token any kit service accepts under auth.mode jwt
// with the relay's public key in auth.jwt.public_key_files.
func TestRelayTokenIsTheOneAServedToolAccepts(t *testing.T) {
	a := genKey(t)
	tok, err := newRelayAuth(a).Token()
	require.NoError(t, err)
	pem, err := a.MarshalPublicKey()
	require.NoError(t, err)
	key, err := authn.ParsePublicKeyPEM(pem)
	require.NoError(t, err)
	v, err := authn.NewJWT([]authn.Key{key}, authn.Options{Issuer: relayIssuer})
	require.NoError(t, err)
	got, err := v.Verify(t.Context(), tok)
	require.NoError(t, err)
	assert.Equal(t, a.PublicKeyID(), got.KeyID)
}

// TestRelayAuthenticatesOverTheBus pins the wiring end to end: a
// listener trusting relay A's public key file accepts A's connection
// and its events; relay B, untrusted, is refused.
func TestRelayAuthenticatesOverTheBus(t *testing.T) {
	a, b := genKey(t), genKey(t)
	pem, err := a.MarshalPublicKey()
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "a.pub")
	require.NoError(t, os.WriteFile(file, pem, 0o600))
	trusted, err := loadTrusted([]string{file})
	require.NoError(t, err)

	hubBus := bus.New()
	hub := bus.NewNetworkAdapter(hubBus, bus.WithAuth(newRelayAuth(nil, trusted...)))
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()
	defer hub.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/bus"

	var got atomic.Bool
	hubBus.Subscribe("app.hello", func(context.Context, bus.Event) error { got.Store(true); return nil })

	aBus := bus.New()
	aNet := bus.NewNetworkAdapter(aBus, bus.WithAuth(newRelayAuth(a)))
	defer aNet.Close()
	require.NoError(t, aNet.Connect(context.Background(), url))
	require.Eventually(t, func() bool {
		_ = aBus.Publish(context.Background(), bus.NewEvent("app.hello", "a", nil))
		return got.Load()
	}, 3*time.Second, 50*time.Millisecond)

	bNet := bus.NewNetworkAdapter(bus.New(), bus.WithAuth(newRelayAuth(b)))
	defer bNet.Close()
	err = bNet.Connect(context.Background(), url)
	assert.True(t, errors.Is(err, bus.ErrAuthFailed), "untrusted relay: %v", err)
}

func TestLoadTrustedRefusesWhatIsNotAnEd25519Key(t *testing.T) {
	file := filepath.Join(t.TempDir(), "x.pub")
	require.NoError(t, os.WriteFile(file, []byte("not pem"), 0o600))
	_, err := loadTrusted([]string{file})
	assert.Error(t, err)
	_, err = loadTrusted([]string{filepath.Join(t.TempDir(), "gone.pub")})
	assert.Error(t, err)
}

// runListen executes `relay listen <args>` and returns the error.
func runListen(t *testing.T, args ...string) error {
	t.Helper()
	root := newTestRoot(t)
	b := bus.New()
	defer b.Close(context.Background())
	addCommands(root, b)
	root.Cmd.SetOut(io.Discard)
	root.Cmd.SetErr(io.Discard)
	root.SetArgs(append([]string{"listen"}, args...))
	done := make(chan error, 1)
	go func() { done <- root.Execute(context.Background()) }()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("listen started serving instead of returning")
		return nil
	}
}

// TestListenRefusesWithoutTrust pins the secure default: listen with
// neither --trust nor --allow-anonymous refuses to start, a usage error
// (exit 2) naming both flags.
func TestListenRefusesWithoutTrust(t *testing.T) {
	err := runListen(t, "--addr", "127.0.0.1:0")
	require.Error(t, err)
	var oe *output.Error
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, output.ExitUsage, oe.ExitCode)
	assert.Equal(t, output.ExitUsage, exitCode(err), "main exits with the error's code")
	assert.Contains(t, oe.Message, "--trust")
	assert.Contains(t, oe.Message, "--allow-anonymous")
}

// TestListenAllowAnonymousGetsPastTheGate pins the explicit opt-out:
// with --allow-anonymous listen goes on to bind, here an address that
// cannot be bound, so it fails there rather than at the gate.
func TestListenAllowAnonymousGetsPastTheGate(t *testing.T) {
	err := runListen(t, "--allow-anonymous", "--addr", "127.0.0.1:-1")
	require.Error(t, err)
	var oe *output.Error
	assert.False(t, errors.As(err, &oe) && oe.ExitCode == output.ExitUsage, "refused at the gate: %v", err)
	assert.Equal(t, 1, exitCode(err))
}
