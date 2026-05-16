package cmdsurface

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WebhookSink POSTs invocation results to URL. The body is a JSON
// envelope:
//
//	{"invocation":<Invocation>,"result":<Result>,"error":<string|null>}
//
// Non-2xx responses, network errors, and context cancellation all
// surface as a non-nil error from Emit.
type WebhookSink struct {
	// URL is the POST target. Required.
	URL string
	// Client is the HTTP client used to POST. If nil, an internal
	// default with a 10s timeout is used.
	Client *http.Client
	// Headers is added to every request. May be nil.
	Headers map[string]string
	// Sign, when set, is called with the marshaled body and returns
	// the header name and value to attach (for HMAC or similar
	// schemes). The header is added after Headers, so signed values
	// win on conflict.
	Sign func(body []byte) (header string, value string)
}

// webhookSinkEnvelope is the JSON shape POSTed for one invocation.
type webhookSinkEnvelope struct {
	Invocation Invocation `json:"invocation"`
	Result     Result     `json:"result"`
	Error      *string    `json:"error"`
}

// defaultWebhookClient is shared across WebhookSink instances that
// don't supply their own *http.Client.
var defaultWebhookClient = &http.Client{Timeout: 10 * time.Second}

// Emit marshals the envelope, POSTs it to w.URL, and returns nil on
// any 2xx response or an error otherwise. The request inherits ctx;
// callers can cancel mid-flight.
func (w *WebhookSink) Emit(ctx context.Context, inv Invocation, res Result, callErr error) error {
	env := webhookSinkEnvelope{Invocation: inv, Result: res}
	if callErr != nil {
		s := callErr.Error()
		env.Error = &s
	}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("cmdsurface: webhook marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cmdsurface: webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range w.Headers {
		req.Header.Set(k, v)
	}
	if w.Sign != nil {
		hk, hv := w.Sign(body)
		if hk != "" {
			req.Header.Set(hk, hv)
		}
	}

	client := w.Client
	if client == nil {
		client = defaultWebhookClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cmdsurface: webhook POST: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a small prefix of the body to surface server errors.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("cmdsurface: webhook status %d: %s",
			resp.StatusCode, bytes.TrimSpace(snippet))
	}
	// Discard remainder so the connection can be reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
