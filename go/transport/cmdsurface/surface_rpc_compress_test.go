package cmdsurface_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// invokeRaw posts one Connect unary JSON call for the echo leaf and
// returns the response's Content-Encoding and decoded stdout. The
// client neither advertises nor decodes on its own behalf beyond the
// Accept-Encoding set here, so the wire encoding is observable.
func invokeRaw(t *testing.T, f *testFixture) (encoding, stdout string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost,
		f.ts.URL+cmdsurface.RPCInvokeProcedure, strings.NewReader(`{"path":["echo"]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}

	var body io.Reader = resp.Body
	encoding = resp.Header.Get("Content-Encoding")
	if encoding == "gzip" {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		body = zr
	}
	var out struct {
		Stdout string `json:"stdout"`
	}
	if err := json.NewDecoder(body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return encoding, out.Stdout
}

func TestRPCCompression(t *testing.T) {
	floor := func(n int) []cmdsurface.RPCOption {
		return []cmdsurface.RPCOption{cmdsurface.WithRPCCompression(n)}
	}
	large := strings.Repeat("a line of command output\n", 200)

	cases := []struct {
		name   string
		stdout string
		opts   []cmdsurface.RPCOption
		want   string
	}{
		{name: "off by default", stdout: large, want: ""},
		{name: "opted in: large response is gzipped", stdout: large, opts: floor(api.DefaultCompressMinBytes), want: "gzip"},
		{name: "opted in: small response stays identity", stdout: "hi", opts: floor(api.DefaultCompressMinBytes), want: ""},
		{name: "opted in at zero: every response is gzipped", stdout: "hi", opts: floor(0), want: "gzip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.runner.RunFn = func(context.Context, cmdsurface.Invocation) (cmdsurface.Result, error) {
				return cmdsurface.Result{Stdout: tc.stdout}, nil
			}
			f.start(tc.opts...)

			enc, got := invokeRaw(t, f)
			if enc != tc.want {
				t.Errorf("Content-Encoding=%q want=%q", enc, tc.want)
			}
			if got != tc.stdout {
				t.Errorf("stdout mismatch: got %d bytes want %d", len(got), len(tc.stdout))
			}
		})
	}
}
