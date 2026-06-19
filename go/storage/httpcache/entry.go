package httpcache

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// entry is the on-store representation of a cached response. It is a
// language-neutral JSON envelope — NOT a Go-specific wire dump — so the
// TS and Python parity ports serialize to the identical shape and can
// share both a kv backend and the cross-language test vectors.
//
// Contract (contracts/httpcache-v1): field names are lowercase, Body is
// standard base64 (encoding/json's []byte default), Headers preserves
// multi-value semantics as a map of slices.
type entry struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

// framingHeaders describe the wire framing of the original transfer,
// not the cached payload: net/http has already dechunked the body and
// the stored bytes are authoritative for length. Carrying them into a
// reconstructed response produces inconsistent state (e.g. a chunked
// Transfer-Encoding alongside an explicit ContentLength, which the HTTP
// spec forbids), so they are dropped at encode time. decodeEntry sets
// ContentLength from the stored bytes.
var framingHeaders = []string{"Content-Length", "Transfer-Encoding", "Connection"}

// encodeEntry serializes resp into the JSON envelope. It drains
// resp.Body and refills it with a replayable buffer so the response
// remains usable by the original caller after caching. Framing headers
// are stripped from the stored copy (not from resp itself).
func encodeEntry(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))

	hdr := resp.Header.Clone()
	if hdr == nil {
		hdr = http.Header{}
	}
	for _, h := range framingHeaders {
		hdr.Del(h)
	}

	return json.Marshal(entry{
		Status:  resp.StatusCode,
		Headers: map[string][]string(hdr),
		Body:    body,
	})
}

// decodeEntry reconstructs an *http.Response from a stored envelope,
// associating it with req so the response carries its originating
// request (as http.Client expects). The returned Body is a fresh
// reader over the cached bytes.
func decodeEntry(raw []byte, req *http.Request) (*http.Response, error) {
	var e entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	hdr := http.Header(e.Headers)
	if hdr == nil {
		hdr = http.Header{}
	}
	return &http.Response{
		Status:        http.StatusText(e.Status),
		StatusCode:    e.Status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        hdr,
		Body:          io.NopCloser(bytes.NewReader(e.Body)),
		ContentLength: int64(len(e.Body)),
		Request:       req,
	}, nil
}
