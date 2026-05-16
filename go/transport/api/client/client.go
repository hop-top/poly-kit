package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"hop.top/kit/go/transport/api"
)

// Option configures a Client.
type Option func(*options)

type options struct {
	httpClient *http.Client
	authToken  string
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) { o.httpClient = c }
}

// WithAuth sets a Bearer token for the Authorization header.
func WithAuth(token string) Option {
	return func(o *options) { o.authToken = token }
}

// Client is a typed REST client for a single resource endpoint.
type Client[T api.Entity] struct {
	baseURL string
	http    *http.Client
	auth    string
}

// New creates a Client targeting baseURL (e.g. "http://host/items").
func New[T api.Entity](baseURL string, opts ...Option) *Client[T] {
	o := &options{httpClient: http.DefaultClient}
	for _, fn := range opts {
		fn(o)
	}
	return &Client[T]{
		baseURL: baseURL,
		http:    o.httpClient,
		auth:    o.authToken,
	}
}

// Create sends POST baseURL/ with entity as JSON body.
func (c *Client[T]) Create(ctx context.Context, entity T) (T, error) {
	return c.doJSON(ctx, http.MethodPost, c.baseURL+"/", entity)
}

// Get sends GET baseURL/{id}.
func (c *Client[T]) Get(ctx context.Context, id string) (T, error) {
	var zero T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+id, nil)
	if err != nil {
		return zero, err
	}
	c.setHeaders(req)
	return c.send(req)
}

// List sends GET baseURL/ with query parameters.
func (c *Client[T]) List(ctx context.Context, q api.Query) ([]T, error) {
	u, err := url.Parse(c.baseURL + "/")
	if err != nil {
		return nil, err
	}
	params := u.Query()
	if q.Limit > 0 {
		params.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		params.Set("offset", strconv.Itoa(q.Offset))
	}
	if q.Sort != "" {
		params.Set("sort", q.Sort)
	}
	if q.Search != "" {
		params.Set("search", q.Search)
	}
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var items []T
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}
	return items, nil
}

// Update sends PUT baseURL/{id} with entity as JSON body.
func (c *Client[T]) Update(ctx context.Context, entity T) (T, error) {
	return c.doJSON(ctx, http.MethodPut, c.baseURL+"/"+entity.GetID(), entity)
}

// Delete sends DELETE baseURL/{id}.
func (c *Client[T]) Delete(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/"+id, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return checkStatus(resp)
}

func (c *Client[T]) doJSON(ctx context.Context, method, url string, body T) (T, error) {
	var zero T
	buf, err := json.Marshal(body)
	if err != nil {
		return zero, err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(buf))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req)
	return c.send(req)
}

func (c *Client[T]) send(req *http.Request) (T, error) {
	var zero T
	resp, err := c.http.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return zero, err
	}

	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return zero, fmt.Errorf("decode response: %w", err)
	}
	return v, nil
}

func (c *Client[T]) setHeaders(req *http.Request) {
	if c.auth != "" {
		req.Header.Set("Authorization", "Bearer "+c.auth)
	}
}

// checkStatus returns an *api.APIError for non-2xx responses.
func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	var ae api.APIError
	if json.Unmarshal(body, &ae) == nil && ae.Code != "" {
		return &ae
	}
	return &api.APIError{
		Status:  resp.StatusCode,
		Code:    "http_error",
		Message: string(body),
	}
}
