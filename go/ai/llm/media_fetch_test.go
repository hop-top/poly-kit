package llm_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
	"hop.top/kit/go/core/netpolicy"
)

// URLSource fetches are guarded against SSRF: http(s) only, public
// addresses only (checked on the address actually dialed), no
// redirects, a size cap, and the offline policy.

func readURL(t *testing.T, raw string, opts ...llm.URLSourceOption) ([]byte, error) {
	t.Helper()
	return readURLCtx(context.Background(), raw, opts...)
}

func readURLCtx(ctx context.Context, raw string, opts ...llm.URLSourceOption) ([]byte, error) {
	rc, err := llm.URLSource(raw, opts...).Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// countingServer answers every request with h and counts the requests.
func countingServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestURLSource_NonHTTPSchemesRefused(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"ftp://example.com/x.png",
		"data:image/png;base64,AAAA",
		"gopher://example.com/x",
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := readURL(t, raw)
			require.Error(t, err)
			assert.ErrorIs(t, err, llm.ErrMediaURLRefused)
			assert.Contains(t, err.Error(), "only http or https URLs are fetched")
		})
	}
}

func TestURLSource_NonPublicAddressesRefused(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/x.png",
		"http://localhost/x.png",
		"http://[::1]/x.png",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/x.png",
		"http://192.168.1.1/x.png",
		"http://172.16.0.1/x.png",
		"http://0.0.0.0/x.png",
		"http://[::ffff:127.0.0.1]/x.png",
		"http://[::ffff:169.254.169.254]/x.png",
		"http://[fc00::1]/x.png",
		"http://[fd12:3456::1]/x.png",
		"http://[fe80::1]/x.png",
		"http://224.0.0.1/x.png",
		"http://100.64.0.1/x.png",
		"http://[64:ff9b::a9fe:a9fe]/x.png",
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := readURL(t, raw)
			require.Error(t, err)
			assert.ErrorIs(t, err, llm.ErrMediaURLRefused)
			assert.Contains(t, err.Error(), "non-public address")
		})
	}
}

func TestURLSource_LiteralRefusalMessage(t *testing.T) {
	_, err := readURL(t, "http://169.254.169.254/latest/meta-data/?X-Amz-Signature=sekrit")
	require.Error(t, err)
	assert.Equal(t,
		"media: refusing to fetch http://169.254.169.254/latest/meta-data/: non-public address 169.254.169.254",
		err.Error())
}

func TestURLSource_UserinfoRefused(t *testing.T) {
	_, err := readURL(t, "https://user:pw@example.com/x.png")
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrMediaURLRefused)
	assert.Contains(t, err.Error(), "userinfo")
	assert.NotContains(t, err.Error(), "pw")
}

func TestURLSource_NoHostRefused(t *testing.T) {
	_, err := readURL(t, "http:///x.png")
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrMediaURLRefused)
	assert.Contains(t, err.Error(), "URL has no host")
}

func TestURLSource_LoopbackServerRefusedByDefault(t *testing.T) {
	srv, n := countingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("PNG"))
	})
	_, err := readURL(t, srv.URL+"/x.png")
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrMediaURLRefused)
	assert.Equal(t, int32(0), n.Load(), "no request may reach the server")
}

// A name that resolves to a private address is refused at dial time,
// before any connection: the check runs on the resolved address.
func TestURLSource_HostnameResolvingPrivateRefused(t *testing.T) {
	for name, addrs := range map[string][]string{
		"private":      {"10.1.2.3"},
		"metadata":     {"169.254.169.254"},
		"loopback v6":  {"::1"},
		"mapped":       {"::ffff:192.168.0.1"},
		"one of many":  {"93.184.216.34", "127.0.0.1"},
		"ula":          {"fd00::5"},
		"no addresses": {},
	} {
		t.Run(name, func(t *testing.T) {
			llm.FakeMediaLookup(t, map[string][]string{"media.example": addrs})
			_, err := readURL(t, "http://media.example/x.png")
			require.Error(t, err)
			assert.ErrorIs(t, err, llm.ErrMediaURLRefused)
			assert.Contains(t, err.Error(), "media: refusing to fetch http://media.example/x.png")
		})
	}
}

// The connection goes to the vetted address: a second lookup that
// would answer differently (DNS rebinding) is never made.
func TestURLSource_ConnectionPinnedToVettedAddress(t *testing.T) {
	llm.AllowLoopbackMedia(t)
	srv, n := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "media.example", strings.Split(r.Host, ":")[0])
		_, _ = w.Write([]byte("PNG"))
	})
	port := strconv.Itoa(srv.Listener.Addr().(*net.TCPAddr).Port)
	lookups := llm.FakeMediaLookupSeq(t, "media.example", [][]string{{"127.0.0.1"}, {"10.0.0.1"}})
	got, err := readURL(t, "http://media.example:"+port+"/x.png")
	require.NoError(t, err)
	assert.Equal(t, "PNG", string(got))
	assert.Equal(t, int32(1), n.Load())
	assert.Equal(t, 1, lookups(), "the host is resolved once, then dialed by address")
}

func TestURLSource_FetchPublic(t *testing.T) {
	llm.AllowLoopbackMedia(t)
	var path string
	srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNG"))
	})
	got, err := readURL(t, srv.URL+"/x.png")
	require.NoError(t, err)
	assert.Equal(t, "PNG", string(got))
	assert.Equal(t, "/x.png", path)
}

func TestURLSource_RedirectNotFollowed(t *testing.T) {
	llm.AllowLoopbackMedia(t)
	srv, n := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	_, err := readURL(t, srv.URL+"/x.png")
	require.Error(t, err)
	var hs *llmerrors.HTTPStatusError
	require.ErrorAs(t, err, &hs)
	assert.Equal(t, http.StatusFound, hs.StatusCode)
	assert.Contains(t, err.Error(), "redirect not followed")
	assert.Equal(t, int32(1), n.Load())
}

// A redirect to a loopback address is not followed either: the first
// hop is the only one.
func TestURLSource_RedirectToSameServerNotFollowed(t *testing.T) {
	llm.AllowLoopbackMedia(t)
	srv, n := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final.png" {
			_, _ = w.Write([]byte("PNG"))
			return
		}
		http.Redirect(w, r, "/final.png", http.StatusMovedPermanently)
	})
	_, err := readURL(t, srv.URL+"/x.png")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redirect not followed")
	assert.Equal(t, int32(1), n.Load())
}

func TestURLSource_ErrorStatus(t *testing.T) {
	llm.AllowLoopbackMedia(t)
	srv, _ := countingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	_, err := readURL(t, srv.URL+"/x.png")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http 404")
}

func TestURLSource_OversizeRefused(t *testing.T) {
	llm.AllowLoopbackMedia(t)
	body := strings.Repeat("x", 2048)
	for name, c := range map[string]struct {
		h        http.HandlerFunc
		wantSize int64
		opened   bool
	}{
		// A declared length over the cap fails before the body is read.
		"declared length": {func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}, 2048, false},
		// An undeclared one fails on the first byte past the cap.
		"chunked": {func(w http.ResponseWriter, _ *http.Request) {
			for i := 0; i < 4; i++ {
				_, _ = w.Write([]byte(body[:512]))
				w.(http.Flusher).Flush()
			}
		}, 1025, true},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := countingServer(t, c.h)
			rc, err := llm.URLSource(srv.URL+"/x.png", llm.MediaMaxBytes(1024)).Reader(context.Background())
			assert.Equal(t, c.opened, err == nil, "Reader error: %v", err)
			if err == nil {
				got, rerr := io.ReadAll(rc)
				_ = rc.Close()
				assert.Len(t, got, 1024, "bytes up to the cap are delivered")
				err = rerr
			}
			require.Error(t, err)
			var tl *llmerrors.ErrMediaTooLarge
			require.ErrorAs(t, err, &tl)
			assert.Equal(t, int64(1024), tl.Limit)
			assert.Equal(t, c.wantSize, tl.Size)
		})
	}
}

func TestURLSource_ExactlyAtLimit(t *testing.T) {
	llm.AllowLoopbackMedia(t)
	srv, _ := countingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 1024)))
	})
	got, err := readURL(t, srv.URL+"/x.png", llm.MediaMaxBytes(1024))
	require.NoError(t, err)
	assert.Len(t, got, 1024)
}

func TestURLSource_DefaultLimit(t *testing.T) {
	assert.Equal(t, int64(32<<20), llm.DefaultMaxMediaBytes)
}

func TestURLSource_OfflineRefused(t *testing.T) {
	llm.FakeMediaLookup(t, map[string][]string{"media.example": {"93.184.216.34"}})
	ctx := netpolicy.WithOffline(context.Background(), true)
	_, err := readURLCtx(ctx, "https://media.example/x.png")
	require.Error(t, err)
	assert.True(t, errors.Is(err, netpolicy.ErrOffline), "got %v", err)
}

func TestURLSource_CredentialsInURLNotEchoed(t *testing.T) {
	_, err := readURL(t, "http://127.0.0.1/x.png?X-Amz-Signature=sekrit")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "sekrit")
}

func TestIsPublicMediaAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"93.184.216.34":        true,
		"8.8.8.8":              true,
		"2606:4700::1111":      true,
		"127.0.0.1":            false,
		"10.0.0.1":             false,
		"172.31.255.255":       false,
		"192.168.0.1":          false,
		"169.254.169.254":      false,
		"100.64.0.1":           false,
		"0.0.0.0":              false,
		"0.1.2.3":              false,
		"192.0.2.1":            false,
		"198.18.0.1":           false,
		"198.51.100.1":         false,
		"203.0.113.1":          false,
		"224.0.0.1":            false,
		"240.0.0.1":            false,
		"255.255.255.255":      false,
		"::":                   false,
		"::1":                  false,
		"::ffff:127.0.0.1":     false,
		"::ffff:10.0.0.1":      false,
		"::ffff:100.64.0.1":    false,
		"::ffff:192.0.2.1":     false,
		"::ffff:93.184.216.34": true,
		"::127.0.0.1":          false,
		"fc00::1":              false,
		"fdff::1":              false,
		"fe80::1":              false,
		"fec0::1":              false,
		"ff02::1":              false,
		"2001:db8::1":          false,
		"2002:7f00:1::1":       false,
		"64:ff9b::7f00:1":      false,
		"64:ff9b::5db8:d822":   true,
		"64:ff9b:1::1":         false,
		"100::1":               false,
		"fe80::1%en0":          false,
	} {
		t.Run(addr, func(t *testing.T) {
			assert.Equal(t, want, llm.IsPublicMediaAddr(netip.MustParseAddr(addr)))
		})
	}
}

// The dialer's Control hook is the check on the address a socket
// actually connects to; dialPublic only hands it vetted addresses, so
// it is exercised directly.
func TestMediaDialControl(t *testing.T) {
	for addr, refused := range map[string]bool{
		"93.184.216.34:443":     false,
		"[2606:4700::1111]:443": false,
		"127.0.0.1:80":          true,
		"169.254.169.254:80":    true,
		"[::ffff:10.0.0.1]:80":  true,
		"[fe80::1%en0]:80":      true,
		"not-an-address":        true,
	} {
		t.Run(addr, func(t *testing.T) {
			err := llm.MediaDialControl("tcp4", addr, nil)
			if refused {
				assert.ErrorIs(t, err, llm.ErrMediaURLRefused)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
