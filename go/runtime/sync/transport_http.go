package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// HTTPTransport pushes/pulls diffs over HTTP.
type HTTPTransport struct {
	baseURL    string
	httpClient *http.Client
	authToken  string
}

// NewHTTPTransport creates a transport targeting the given base URL.
func NewHTTPTransport(baseURL string, opts ...TransportOption) *HTTPTransport {
	var o transportOpts
	for _, fn := range opts {
		fn(&o)
	}
	return &HTTPTransport{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		authToken:  o.authToken,
	}
}

// Push POSTs diffs to /sync/push.
func (t *HTTPTransport) Push(ctx context.Context, diffs []Diff) error {
	body, err := json.Marshal(diffs)
	if err != nil {
		return fmt.Errorf("marshal diffs: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.baseURL+"/sync/push", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	t.setAuth(req)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("push: status %d", resp.StatusCode)
	}
	return nil
}

// Pull GETs diffs from /sync/pull?since=<timestamp>.
func (t *HTTPTransport) Pull(ctx context.Context, since Timestamp) ([]Diff, error) {
	u, err := url.Parse(t.baseURL + "/sync/pull")
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	q := u.Query()
	q.Set("since_physical", fmt.Sprintf("%d", since.Physical))
	q.Set("since_logical", fmt.Sprintf("%d", since.Logical))
	q.Set("since_node", since.NodeID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	t.setAuth(req)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pull: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pull: status %d", resp.StatusCode)
	}

	var diffs []Diff
	if err := json.NewDecoder(resp.Body).Decode(&diffs); err != nil {
		return nil, fmt.Errorf("decode diffs: %w", err)
	}
	return diffs, nil
}

// Ping checks connectivity via GET /sync/ping.
func (t *HTTPTransport) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		t.baseURL+"/sync/ping", nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	t.setAuth(req)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping: status %d", resp.StatusCode)
	}
	return nil
}

// Close is a no-op for HTTP transports.
func (t *HTTPTransport) Close() error { return nil }

func (t *HTTPTransport) setAuth(req *http.Request) {
	if t.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+t.authToken)
	}
}
