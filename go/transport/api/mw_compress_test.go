package api_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// bigJSON is a JSON body comfortably above the default threshold.
var bigJSON = func() []byte {
	items := make([]map[string]string, 200)
	for i := range items {
		items[i] = map[string]string{"name": "item-" + strconv.Itoa(i), "summary": "a repeated summary line"}
	}
	b, _ := json.Marshal(map[string]any{"items": items})
	return b
}()

// rawClient never negotiates or decodes on its own, so each test sees
// the bytes and headers the server put on the wire.
func rawClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableCompression: true}}
}

func serveCompressed(t *testing.T, h http.HandlerFunc, opts ...api.CompressOption) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(api.Compress(opts...)(h))
	t.Cleanup(srv.Close)
	return srv
}

func jsonHandler(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

func get(t *testing.T, url, method string, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	require.NoError(t, err)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := rawClient().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, body
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	require.NoError(t, err)
	out, err := io.ReadAll(zr)
	require.NoError(t, err)
	return out
}

func unzstd(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := zstd.NewReader(bytes.NewReader(b))
	require.NoError(t, err)
	defer zr.Close()
	out, err := io.ReadAll(zr)
	require.NoError(t, err)
	return out
}

func hasVaryAE(resp *http.Response) bool {
	for _, v := range resp.Header.Values("Vary") {
		for _, f := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(f), "Accept-Encoding") {
				return true
			}
		}
	}
	return false
}

func TestCompress_GzipNegotiated(t *testing.T) {
	srv := serveCompressed(t, jsonHandler(bigJSON))
	resp, body := get(t, srv.URL, http.MethodGet, map[string]string{"Accept-Encoding": "gzip"})

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	assert.True(t, hasVaryAE(resp), "Vary: Accept-Encoding")
	assert.Less(t, len(body), len(bigJSON), "compressed body is smaller")
	assert.Equal(t, bigJSON, gunzip(t, body))
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		assert.Equal(t, strconv.Itoa(len(body)), cl, "Content-Length describes the encoded body")
	}
}

func TestCompress_ZstdNegotiated(t *testing.T) {
	srv := serveCompressed(t, jsonHandler(bigJSON))
	resp, body := get(t, srv.URL, http.MethodGet, map[string]string{"Accept-Encoding": "gzip;q=0.5, zstd"})

	assert.Equal(t, "zstd", resp.Header.Get("Content-Encoding"))
	assert.Equal(t, bigJSON, unzstd(t, body))
}

func TestCompress_QValues(t *testing.T) {
	srv := serveCompressed(t, jsonHandler(bigJSON))
	cases := map[string]string{
		"gzip;q=0.8, zstd;q=0.2": "gzip",
		"gzip;q=0, zstd;q=0":     "",
		"zstd;q=0, gzip":         "gzip",
		"br, identity":           "",
		"gzip, zstd":             "zstd",
	}
	for ae, want := range cases {
		t.Run(ae, func(t *testing.T) {
			resp, body := get(t, srv.URL, http.MethodGet, map[string]string{"Accept-Encoding": ae})
			assert.Equal(t, want, resp.Header.Get("Content-Encoding"))
			if want == "" {
				assert.Equal(t, bigJSON, body)
			}
		})
	}
}

func TestCompress_IdentityWhenNotAccepted(t *testing.T) {
	srv := serveCompressed(t, jsonHandler(bigJSON))
	resp, body := get(t, srv.URL, http.MethodGet, nil)

	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	assert.True(t, hasVaryAE(resp), "Vary is set even when identity is chosen")
	assert.Equal(t, bigJSON, body)
}

func TestCompress_BelowThresholdUntouched(t *testing.T) {
	small := []byte(`{"ok":true}`)
	srv := serveCompressed(t, jsonHandler(small))
	resp, body := get(t, srv.URL, http.MethodGet, map[string]string{"Accept-Encoding": "gzip, zstd"})

	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	assert.Equal(t, small, body)
	assert.Equal(t, strconv.Itoa(len(small)), resp.Header.Get("Content-Length"))
}

func TestCompress_MinBytesOption(t *testing.T) {
	small := []byte(`{"ok":true,"padding":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}`)
	srv := serveCompressed(t, jsonHandler(small), api.WithCompressMinBytes(16))
	resp, body := get(t, srv.URL, http.MethodGet, map[string]string{"Accept-Encoding": "gzip"})

	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	assert.Equal(t, small, gunzip(t, body))
}

func TestCompress_ContentTypeAllowlist(t *testing.T) {
	cases := map[string]bool{
		"application/json":                   true,
		"application/json; charset=utf-8":    true,
		"application/problem+json":           true,
		"application/vnd.oai.openapi+json":   true,
		"application/openapi+yaml":           true,
		"application/yaml":                   true,
		"text/plain; charset=utf-8":          true,
		"text/html":                          true,
		"application/octet-stream":           false,
		"image/png":                          false,
		"application/x-ndjson":               false,
		"application/grpc":                   false,
		"text/event-stream":                  false,
		"application/vnd.something+protobuf": false,
	}
	for ct, want := range cases {
		t.Run(ct, func(t *testing.T) {
			srv := serveCompressed(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", ct)
				_, _ = w.Write(bigJSON)
			})
			resp, body := get(t, srv.URL, http.MethodGet, map[string]string{"Accept-Encoding": "gzip"})
			if want {
				assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
			} else {
				assert.Empty(t, resp.Header.Get("Content-Encoding"))
				assert.Equal(t, bigJSON, body)
			}
		})
	}
}

func TestCompress_AlreadyEncodedUntouched(t *testing.T) {
	var pre bytes.Buffer
	zw := gzip.NewWriter(&pre)
	_, _ = zw.Write(bigJSON)
	_ = zw.Close()

	srv := serveCompressed(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(pre.Bytes())
	})
	resp, body := get(t, srv.URL, http.MethodGet, map[string]string{"Accept-Encoding": "zstd, gzip"})

	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	assert.Equal(t, bigJSON, gunzip(t, body), "encoded exactly once")
}

func TestCompress_HandlerContentLength(t *testing.T) {
	srv := serveCompressed(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(bigJSON)))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(bigJSON)
	})
	resp, body := get(t, srv.URL, http.MethodGet, map[string]string{"Accept-Encoding": "gzip"})

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	assert.NotEqual(t, strconv.Itoa(len(bigJSON)), resp.Header.Get("Content-Length"),
		"the identity length must not describe the encoded body")
	assert.Equal(t, bigJSON, gunzip(t, body))
}

func TestCompress_HEAD(t *testing.T) {
	srv := serveCompressed(t, jsonHandler(bigJSON))
	resp, body := get(t, srv.URL, http.MethodHead, map[string]string{"Accept-Encoding": "gzip"})

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	assert.True(t, hasVaryAE(resp))
	assert.Empty(t, body)
}

// TestCompress_SSEUntouchedAndIncremental proves an event stream is
// neither encoded nor held back: the headers reach the client before
// the first event exists, and each event reaches it before the next
// one is written.
func TestCompress_SSEUntouchedAndIncremental(t *testing.T) {
	for name, accept := range map[string]string{
		"accept header":    "text/event-stream",
		"no accept header": "",
	} {
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			done := make(chan struct{})
			srv := serveCompressed(t, func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				rc := http.NewResponseController(w)
				assert.NoError(t, rc.Flush())
				for i := 0; i < 3; i++ {
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					_, _ = io.WriteString(w, "data: event-"+strconv.Itoa(i)+"\n\n")
					assert.NoError(t, rc.Flush())
				}
			})

			req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
			require.NoError(t, err)
			req.Header.Set("Accept-Encoding", "gzip, zstd")
			if accept != "" {
				req.Header.Set("Accept", accept)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resp, err := rawClient().Do(req.WithContext(ctx))
			require.NoError(t, err, "headers arrive before any event is written")
			defer resp.Body.Close()

			assert.Empty(t, resp.Header.Get("Content-Encoding"))
			assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
			if accept != "" {
				assert.False(t, hasVaryAE(resp), "an event-stream request bypasses the encoder entirely")
			}

			br := bufio.NewReader(resp.Body)
			for i := 0; i < 3; i++ {
				release <- struct{}{}
				line, err := br.ReadString('\n')
				require.NoError(t, err)
				assert.Equal(t, "data: event-"+strconv.Itoa(i)+"\n", line)
				_, _ = br.ReadString('\n')
			}
			<-done
		})
	}
}

func TestCompress_WebSocketUpgradeUnaffected(t *testing.T) {
	hub := api.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	r := api.NewRouter(api.WithMiddleware(api.Compress()))
	r.Handle(http.MethodGet, "/ws", api.WSHandler(hub))
	srv := httptest.NewServer(r)
	defer srv.Close()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	c, resp, err := websocket.Dial(dialCtx, "ws"+srv.URL[len("http"):]+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Accept-Encoding": []string{"gzip, zstd"}},
	})
	require.NoError(t, err)
	defer c.Close(websocket.StatusNormalClosure, "")
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	assert.False(t, hasVaryAE(resp), "an upgrade is passed through untouched")

	_, data, err := c.Read(dialCtx)
	require.NoError(t, err)
	var msg api.WSMessage
	require.NoError(t, json.Unmarshal(data, &msg))
	assert.Equal(t, "welcome", msg.Type)
}

// TestCompress_ResponseController checks the wrapped writer still
// answers the optional interfaces a handler reaches through
// http.ResponseController while compression is active.
func TestCompress_ResponseController(t *testing.T) {
	errs := make(chan error, 2)
	srv := serveCompressed(t, func(w http.ResponseWriter, _ *http.Request) {
		rc := http.NewResponseController(w)
		errs <- rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bigJSON)
		errs <- rc.Flush()
	})
	resp, body := get(t, srv.URL, http.MethodGet, map[string]string{"Accept-Encoding": "gzip"})

	require.NoError(t, <-errs, "SetWriteDeadline")
	require.NoError(t, <-errs, "Flush")
	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	assert.Equal(t, bigJSON, gunzip(t, body))
}

// TestCompress_RPCProtocolsBypassed keeps the middleware off Connect
// and gRPC traffic, which negotiates its own compression.
func TestCompress_RPCProtocolsBypassed(t *testing.T) {
	srv := serveCompressed(t, jsonHandler(bigJSON))
	for name, hdr := range map[string]map[string]string{
		"connect unary":  {"Connect-Protocol-Version": "1", "Content-Type": "application/json"},
		"connect stream": {"Content-Type": "application/connect+json"},
		"grpc":           {"Content-Type": "application/grpc+proto"},
		"grpc-web":       {"Content-Type": "application/grpc-web+proto"},
	} {
		t.Run(name, func(t *testing.T) {
			hdr["Accept-Encoding"] = "gzip, zstd"
			resp, body := get(t, srv.URL, http.MethodGet, hdr)
			assert.Empty(t, resp.Header.Get("Content-Encoding"))
			assert.False(t, hasVaryAE(resp))
			assert.Equal(t, bigJSON, body)
		})
	}
}

func TestCompress_StatusPreserved(t *testing.T) {
	srv := serveCompressed(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(bigJSON)
	})
	resp, body := get(t, srv.URL, http.MethodGet, map[string]string{"Accept-Encoding": "gzip"})

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	assert.Equal(t, bigJSON, gunzip(t, body))
}
