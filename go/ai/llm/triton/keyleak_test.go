package triton

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// baseURLSecret is a credential a caller embedded in the base URL
// (userinfo or a query param); any echo of it in an error is a leak.
const baseURLSecret = "fake-base-url-secret-must-not-leak"

// Transport and request-build failures quote the request URL, which
// starts with the base URL; credentials in it come back masked.
func TestScore_BaseURLCredentialsNotInErrors(t *testing.T) {
	host := refusingHost(t)

	for name, base := range map[string]string{
		"userinfo token": "http://" + baseURLSecret + "@" + host,
		"userinfo pass":  "http://user:" + baseURLSecret + "@" + host,
		"query key":      "http://" + host + "?key=" + baseURLSecret,
		"unparseable":    "http://h\x7f?key=" + baseURLSecret,
	} {
		t.Run(name, func(t *testing.T) {
			p, err := New(llm.ResolvedConfig{
				Provider: llm.ProviderConfig{BaseURL: base, Model: "mf"},
			})
			require.NoError(t, err)
			_, err = p.(*Client).Score(context.Background(), []float32{1})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), baseURLSecret)
		})
	}
}

// refusingHost returns a loopback host:port held for the whole test
// that closes every connection before answering: each request fails in
// transport, and no other listener can take the port, as one can the
// port of a closed server.
func refusingHost(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}
