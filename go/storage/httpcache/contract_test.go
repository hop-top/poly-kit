package httpcache

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// contractFile resolves contracts/httpcache-v1/<name> from the package
// dir (go/storage/httpcache → repo root is three levels up).
func contractFile(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "contracts", "httpcache-v1", name)
}

func readContract(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(contractFile(t, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
}

// TestContract_Keying pins cache-key derivation against the shared
// vectors. These digests define the cross-language wire contract: a port
// that normalizes method case, host case, default ports, query order,
// dot segments, or fragments will fail here.
func TestContract_Keying(t *testing.T) {
	var vectors struct {
		DefaultPrefix string `json:"default_prefix"`
		Derivation    struct {
			Hash      string `json:"hash"`
			Separator string `json:"separator"`
			VaryAware bool   `json:"vary_aware"`
		} `json:"derivation"`
		Cases []struct {
			Name       string `json:"name"`
			Method     string `json:"method"`
			URL        string `json:"url"`
			WantURL    string `json:"want_url"`
			WantDigest string `json:"want_digest"`
			WantKey    string `json:"want_key"`
		} `json:"cases"`
		PrefixCases []struct {
			Name                string `json:"name"`
			Prefix              string `json:"prefix"`
			WantEffectivePrefix string `json:"want_effective_prefix"`
		} `json:"prefix_cases"`
	}
	readContract(t, "keying.json", &vectors)
	if len(vectors.Cases) == 0 {
		t.Fatal("keying.json has no cases")
	}

	if vectors.DefaultPrefix != defaultPrefix {
		t.Errorf("default_prefix = %q, implementation uses %q", vectors.DefaultPrefix, defaultPrefix)
	}
	if vectors.Derivation.Hash != "sha256" {
		t.Errorf("derivation.hash = %q, want sha256", vectors.Derivation.Hash)
	}
	if vectors.Derivation.Separator != " " {
		t.Errorf("derivation.separator = %q, want a single space", vectors.Derivation.Separator)
	}
	if vectors.Derivation.VaryAware {
		t.Error("derivation.vary_aware must be false: v1 keys on method+URL only")
	}

	tr := New(nil, http.DefaultTransport)
	for _, v := range vectors.Cases {
		t.Run(v.Name, func(t *testing.T) {
			req, err := http.NewRequest(v.Method, v.URL, nil)
			if err != nil {
				t.Fatalf("NewRequest(%q, %q): %v", v.Method, v.URL, err)
			}

			// The URL re-serialization is itself part of the contract:
			// the digest is only reproducible if want_url matches.
			if got := req.URL.String(); got != v.WantURL {
				t.Errorf("re-serialized URL = %q, want %q", got, v.WantURL)
			}
			if req.Method != v.Method {
				t.Errorf("method = %q, want %q carried verbatim (no case folding)", req.Method, v.Method)
			}
			if len(v.WantDigest) != 64 {
				t.Errorf("want_digest is %d chars, sha256 hex is 64", len(v.WantDigest))
			}

			got := tr.key(req)
			if got != v.WantKey {
				t.Errorf("key = %q, want %q", got, v.WantKey)
			}
			if want := vectors.DefaultPrefix + v.WantDigest; got != want {
				t.Errorf("key = %q, want default prefix + digest %q", got, want)
			}
		})
	}

	for _, v := range vectors.PrefixCases {
		t.Run("prefix/"+v.Name, func(t *testing.T) {
			tr := New(nil, http.DefaultTransport, WithPrefix(v.Prefix))
			req, err := http.NewRequest(http.MethodGet, "https://example.com/a", nil)
			if err != nil {
				t.Fatal(err)
			}
			want := v.WantEffectivePrefix + vectors.Cases[0].WantDigest
			if got := tr.key(req); got != want {
				t.Errorf("key = %q, want %q", got, want)
			}
		})
	}
}

// TestContract_Entry pins the on-store envelope: field names and order,
// header multi-value shape, standard-base64 body, and the framing-header
// strip/recompute rule.
func TestContract_Entry(t *testing.T) {
	var vectors struct {
		Framing struct {
			Strip                      []string `json:"strip"`
			StripIsCaseInsensitive     bool     `json:"strip_is_case_insensitive"`
			StripMutatesSourceResponse bool     `json:"strip_mutates_source_response"`
		} `json:"framing_headers"`
		EncodeCases []struct {
			Name       string              `json:"name"`
			Status     int                 `json:"status"`
			Headers    map[string][]string `json:"headers"`
			BodyUTF8   *string             `json:"body_utf8"`
			BodyBase64 *string             `json:"body_base64"`
			WantJSON   string              `json:"want_json"`
		} `json:"encode_cases"`
		DecodeCases []struct {
			Name              string              `json:"name"`
			JSON              string              `json:"json"`
			WantOK            bool                `json:"want_ok"`
			WantStatus        int                 `json:"want_status"`
			WantHeaders       map[string][]string `json:"want_headers"`
			WantBodyUTF8      string              `json:"want_body_utf8"`
			WantContentLength int64               `json:"want_content_length"`
		} `json:"decode_cases"`
	}
	readContract(t, "entry.json", &vectors)
	if len(vectors.EncodeCases) == 0 || len(vectors.DecodeCases) == 0 {
		t.Fatal("entry.json needs both encode_cases and decode_cases")
	}

	if !reflect.DeepEqual(vectors.Framing.Strip, framingHeaders) {
		t.Errorf("framing strip list = %v, implementation uses %v", vectors.Framing.Strip, framingHeaders)
	}
	if !vectors.Framing.StripIsCaseInsensitive {
		t.Error("framing strip is case-insensitive; fixture says otherwise")
	}
	if vectors.Framing.StripMutatesSourceResponse {
		t.Error("framing strip must not mutate the source response; fixture says otherwise")
	}

	for _, v := range vectors.EncodeCases {
		t.Run("encode/"+v.Name, func(t *testing.T) {
			body := contractBody(t, v.BodyUTF8, v.BodyBase64)

			hdr := http.Header{}
			for k, vals := range v.Headers {
				for _, val := range vals {
					hdr.Add(k, val)
				}
			}
			resp := &http.Response{
				StatusCode: v.Status,
				Header:     hdr,
				Body:       io.NopCloser(bytes.NewReader(body)),
			}

			raw, err := encodeEntry(resp)
			if err != nil {
				t.Fatalf("encodeEntry: %v", err)
			}
			// Byte-exact, not merely semantically equal: field order and
			// base64 spelling are what later ports have to reproduce.
			if string(raw) != v.WantJSON {
				t.Errorf("envelope =\n  %s\nwant\n  %s", raw, v.WantJSON)
			}

			// encodeEntry must refill the body so the caller can still
			// read it, and must not strip headers from resp itself.
			after, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("re-read body: %v", err)
			}
			if !bytes.Equal(after, body) {
				t.Errorf("source body after encode = %q, want %q", after, body)
			}
			for k, vals := range v.Headers {
				if got := resp.Header.Get(k); got != vals[0] {
					t.Errorf("source header %q = %q after encode, want %q (must not be mutated)", k, got, vals[0])
				}
			}
		})
	}

	req, err := http.NewRequest(http.MethodGet, "https://example.com/a", nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range vectors.DecodeCases {
		t.Run("decode/"+v.Name, func(t *testing.T) {
			resp, err := decodeEntry([]byte(v.JSON), req)
			if !v.WantOK {
				if err == nil {
					t.Fatal("malformed envelope must be rejected, got no error")
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeEntry: %v", err)
			}

			if resp.StatusCode != v.WantStatus {
				t.Errorf("StatusCode = %d, want %d", resp.StatusCode, v.WantStatus)
			}
			if want := http.StatusText(v.WantStatus); resp.Status != want {
				t.Errorf("Status = %q, want %q derived from the code (reason phrase is not stored)", resp.Status, want)
			}
			if resp.Proto != "HTTP/1.1" {
				t.Errorf("Proto = %q, want HTTP/1.1", resp.Proto)
			}
			if resp.Request != req {
				t.Error("decoded response must carry the originating request")
			}
			if resp.Header == nil {
				t.Error("Header must never be nil: null headers decode to an empty set")
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read decoded body: %v", err)
			}
			if string(body) != v.WantBodyUTF8 {
				t.Errorf("body = %q, want %q", body, v.WantBodyUTF8)
			}
			if resp.ContentLength != v.WantContentLength {
				t.Errorf("ContentLength = %d, want %d recomputed from the decoded body", resp.ContentLength, v.WantContentLength)
			}
			if v.WantHeaders != nil && !reflect.DeepEqual(resp.Header, http.Header(v.WantHeaders)) {
				t.Errorf("Header = %v, want %v", resp.Header, v.WantHeaders)
			}
		})
	}
}

// contractBody resolves a fixture body given either its UTF-8 spelling
// or its base64 spelling; exactly one must be present.
func contractBody(t *testing.T, utf8, b64 *string) []byte {
	t.Helper()
	switch {
	case utf8 != nil && b64 != nil:
		t.Fatal("fixture must set exactly one of body_utf8 / body_base64")
	case utf8 != nil:
		return []byte(*utf8)
	case b64 != nil:
		got, err := base64.StdEncoding.DecodeString(*b64)
		if err != nil {
			t.Fatalf("fixture body_base64 must be standard base64: %v", err)
		}
		return got
	}
	t.Fatal("fixture must set one of body_utf8 / body_base64")
	return nil
}
