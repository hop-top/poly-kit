// Package httpwrap wraps net/http.Client so every request whose
// response will be surfaced to the caller as a wrapper-stamped value
// records its URL and response Date into the kit/provenance Tracker.
//
// Adopters use Client when fetching cached or authoritative data they
// will pair with a Cached[T] or plain-T output field. The wrapper
// stamps URL + FetchedAt against the JSON-pointer path of the output
// field so AssertProvenanceMatchesCassette becomes a string compare
// after Normalize.
//
// Example:
//
//	c := httpwrap.New(http.DefaultClient)
//	resp, prov, err := c.Get(ctx, "/users/0/cohort", "https://api/.../cohort")
//	if err != nil { return err }
//	defer resp.Body.Close()
//	cohort, _ := io.ReadAll(resp.Body)
//	out.Cohort = provenance.NewCached(string(cohort), prov)
package httpwrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"hop.top/kit/go/runtime/provenance"
)

// Client wraps net/http.Client. Every request records a Provenance
// entry against the JSON-pointer path passed at the call site.
type Client struct {
	inner *http.Client
	tag   provenance.SourceTier
}

// New returns a Client tagging responses as SourceAuthoritative. Pass
// http.DefaultClient or a kit-blessed *http.Client; New does not own
// the inner client's lifecycle.
func New(inner *http.Client) *Client {
	if inner == nil {
		inner = http.DefaultClient
	}
	return &Client{inner: inner, tag: provenance.SourceAuthoritative}
}

// NewCacheClient returns a Client tagging responses as SourceCached.
// Use this when the inner client is itself a cache layer (e.g., a
// disk-backed HTTP cache).
func NewCacheClient(inner *http.Client) *Client {
	if inner == nil {
		inner = http.DefaultClient
	}
	return &Client{inner: inner, tag: provenance.SourceCached}
}

// Get fetches url and records a Provenance entry against path in the
// Tracker on ctx. Returns the response, the Provenance the caller can
// attach to a wrapper, and any error.
//
// The caller must Close resp.Body.
func (c *Client) Get(ctx context.Context, path, url string) (*http.Response, provenance.Provenance, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, provenance.Provenance{}, err
	}
	return c.Do(ctx, path, req)
}

// Do is the generic verb; behaves like (*http.Client).Do but stamps
// provenance via path. The request URL is normalised before recording.
func (c *Client) Do(ctx context.Context, path string, req *http.Request) (*http.Response, provenance.Provenance, error) {
	if c.inner == nil {
		c.inner = http.DefaultClient
	}
	if provenance.CurrentModeFromContext(ctx) == provenance.ModeOff {
		// Skip the recording in ModeOff for zero-cost transparency.
		resp, err := c.inner.Do(req)
		return resp, provenance.Provenance{}, err
	}
	resp, err := c.inner.Do(req)
	if err != nil {
		return nil, provenance.Provenance{}, err
	}
	rawURL := req.URL.String()
	normURL, nerr := provenance.Normalize(rawURL)
	if nerr != nil {
		normURL = rawURL // fall back to raw
	}
	fetchedAt := time.Now().UTC()
	if dh := resp.Header.Get("Date"); dh != "" {
		if t, perr := http.ParseTime(dh); perr == nil {
			fetchedAt = t.UTC()
		}
	}
	prov := provenance.Provenance{
		SchemaVersion: provenance.SchemaVersion,
		Source:        c.tag,
		URL:           normURL,
		FetchedAt:     fetchedAt,
		Version:       resp.Header.Get("ETag"),
	}
	tr := provenance.Track(ctx)
	if path != "" {
		if rerr := tr.Record(c.tag, path, prov); rerr != nil {
			// Recording failure is non-fatal at the wrapper level; the
			// strict-mode Render boundary surfaces it later.
			_ = rerr
		}
	}
	return resp, prov, nil
}

// ReadAll is a small helper that combines Do + io.ReadAll + close, for
// the common "fetch this URL, give me its body and provenance" case.
func (c *Client) ReadAll(ctx context.Context, path, url string) ([]byte, provenance.Provenance, error) {
	resp, prov, err := c.Get(ctx, path, url)
	if err != nil {
		return nil, prov, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, prov, fmt.Errorf("httpwrap: GET %s: status %d", url, resp.StatusCode)
	}
	body, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		return nil, prov, rerr
	}
	return body, prov, nil
}
