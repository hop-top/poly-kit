// HTTP delivery: signature/bearer auth, fail-open/closed semantics.
//
// Auth precedence (spec §3): KIT_BUS_SIGNING_KEY (HMAC-SHA256 in
// X-Kit-Bus-Signature) wins over KIT_BUS_TOKEN (Authorization: Bearer).
// If both are set, the signing key is used and the bearer header is
// omitted. If neither is set, the request is sent without auth (the
// ingress is free to reject; that surfaces as a non-2xx).
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"
)

// PostOpts groups the delivery knobs.
type PostOpts struct {
	IngressURL string
	SigningKey string // HMAC-SHA256 key; takes precedence over Token
	Token      string // bearer; used only if SigningKey is empty
	Strict     bool   // true → non-2xx returns error (fail-closed)
	HTTPClient *http.Client
	Topic      string
}

// signatureHeader returns the value to set under X-Kit-Bus-Signature
// for the given body and key. Format: "sha256=<hex digest>".
func signatureHeader(key string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Post sends body to opts.IngressURL with the chosen auth header.
// Returns (statusCode, responseBytes, err). Caller decides whether to
// surface err as a workflow-step failure based on opts.Strict.
//
// Network errors are returned regardless of Strict — the caller logs
// the message and exits 0 under fail-open mode.
func Post(ctx context.Context, opts PostOpts, body []byte) (int, []byte, error) {
	if opts.IngressURL == "" {
		return 0, nil, fmt.Errorf("kit-bus-emit: ingress URL is empty")
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.IngressURL,
		bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("kit-bus-emit: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if opts.Topic != "" {
		req.Header.Set("X-Kit-Bus-Topic", opts.Topic)
	}

	// Auth precedence: signing key beats bearer.
	switch {
	case opts.SigningKey != "":
		req.Header.Set("X-Kit-Bus-Signature", signatureHeader(opts.SigningKey, body))
	case opts.Token != "":
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("kit-bus-emit: POST: %w", err)
	}
	defer resp.Body.Close()

	// io.ReadAll(io.LimitReader) loops until EOF or the cap, so the
	// captured error body is complete across multi-chunk responses.
	// A single resp.Body.Read returns whatever the network had
	// buffered at that moment, which truncates error messages
	// unpredictably.
	const respCap = 4096
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, respCap))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, respBody,
			fmt.Errorf("kit-bus-emit: ingress returned %d: %s",
				resp.StatusCode, string(respBody))
	}
	return resp.StatusCode, respBody, nil
}
