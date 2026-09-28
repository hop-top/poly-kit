package client

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	crudv1 "hop.top/kit/contracts/proto/crud/v1"
	"hop.top/kit/contracts/proto/crud/v1/crudv1connect"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/rpc"
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

// WithAuth sets a Bearer token sent via the Authorization header.
func WithAuth(token string) Option {
	return func(o *options) { o.authToken = token }
}

// Client is a typed ConnectRPC client for a single entity resource.
type Client[T api.Entity] struct {
	raw  crudv1connect.EntityServiceClient
	auth string
}

// New creates a Client targeting baseURL.
func New[T api.Entity](baseURL string, opts ...Option) *Client[T] {
	o := &options{httpClient: http.DefaultClient}
	for _, fn := range opts {
		fn(o)
	}
	raw := crudv1connect.NewEntityServiceClient(o.httpClient, baseURL)
	return &Client[T]{raw: raw, auth: o.authToken}
}

// Create persists a new entity and returns the stored result.
func (c *Client[T]) Create(ctx context.Context, entity T) (T, error) {
	var zero T
	s, err := rpc.EntityToStruct(entity)
	if err != nil {
		return zero, err
	}
	req := connect.NewRequest(&crudv1.CreateRequest{Entity: s})
	c.setHeaders(req)
	resp, err := c.raw.Create(ctx, req)
	if err != nil {
		return zero, mapConnectError(err)
	}
	return rpc.StructToEntity[T](resp.Msg.GetEntity())
}

// Get retrieves a single entity by ID.
func (c *Client[T]) Get(ctx context.Context, id string) (T, error) {
	var zero T
	req := connect.NewRequest(&crudv1.GetRequest{Id: id})
	c.setHeaders(req)
	resp, err := c.raw.Get(ctx, req)
	if err != nil {
		return zero, mapConnectError(err)
	}
	return rpc.StructToEntity[T](resp.Msg.GetEntity())
}

// List returns entities matching the query parameters.
func (c *Client[T]) List(ctx context.Context, q api.Query) ([]T, error) {
	req := connect.NewRequest(&crudv1.ListRequest{
		Limit:  int32(q.Limit),
		Offset: int32(q.Offset),
		Sort:   q.Sort,
		Search: q.Search,
	})
	c.setHeaders(req)
	resp, err := c.raw.List(ctx, req)
	if err != nil {
		return nil, mapConnectError(err)
	}
	items := make([]T, 0, len(resp.Msg.GetEntities()))
	for _, s := range resp.Msg.GetEntities() {
		e, err := rpc.StructToEntity[T](s)
		if err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, nil
}

// Update replaces an existing entity and returns the result.
func (c *Client[T]) Update(ctx context.Context, entity T) (T, error) {
	var zero T
	s, err := rpc.EntityToStruct(entity)
	if err != nil {
		return zero, err
	}
	req := connect.NewRequest(&crudv1.UpdateRequest{Entity: s})
	c.setHeaders(req)
	resp, err := c.raw.Update(ctx, req)
	if err != nil {
		return zero, mapConnectError(err)
	}
	return rpc.StructToEntity[T](resp.Msg.GetEntity())
}

// Delete removes an entity by ID.
func (c *Client[T]) Delete(ctx context.Context, id string) error {
	req := connect.NewRequest(&crudv1.DeleteRequest{Id: id})
	c.setHeaders(req)
	_, err := c.raw.Delete(ctx, req)
	if err != nil {
		return mapConnectError(err)
	}
	return nil
}

func (c *Client[T]) setHeaders(req connect.AnyRequest) {
	if c.auth != "" {
		req.Header().Set("Authorization", "Bearer "+c.auth)
	}
}

// RateLimitedError is a call the server's rate limit refused:
// Connect ResourceExhausted carrying a Retry-After in its metadata, as
// kit's rate-limit gate answers. It is an [*api.APIError] with status
// 429 and code rate_limited (errors.As reaches it), errors.Is
// [api.ErrRateLimited] holds, and RetryAfter is how long the server
// asked the caller to wait.
type RateLimitedError struct {
	*api.APIError
	// RetryAfter is the server's retry hint, never below one second.
	RetryAfter time.Duration
}

// Unwrap exposes the API error and [api.ErrRateLimited].
func (e *RateLimitedError) Unwrap() []error { return []error{e.APIError, api.ErrRateLimited} }

// RetryAfterHint reports RetryAfter, the hint a retry wrapper reads
// (cmdsurface.RetryAfter reads the same method).
func (e *RateLimitedError) RetryAfterHint() time.Duration { return e.RetryAfter }

// mapConnectError converts a Connect error to an *api.APIError. A
// ResourceExhausted carrying Retry-After is a [*RateLimitedError].
func mapConnectError(err error) error {
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return &api.APIError{
			Status:  500,
			Code:    "internal_error",
			Message: err.Error(),
		}
	}
	ae := &api.APIError{
		Status:  codeToStatus(ce.Code()),
		Code:    ce.Code().String(),
		Message: ce.Message(),
	}
	if ce.Code() == connect.CodeResourceExhausted {
		if wait, ok := parseRetryAfter(ce.Meta().Get("Retry-After"), time.Now()); ok {
			ae.Code = api.CodeRateLimited
			return &RateLimitedError{APIError: ae, RetryAfter: wait}
		}
	}
	return ae
}

// parseRetryAfter reads a Retry-After value — delay seconds or an
// HTTP date — as a wait of at least one second. ok is false when v is
// absent or neither form.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return max(time.Duration(secs)*time.Second, time.Second), true
	}
	if at, err := http.ParseTime(v); err == nil {
		return max(at.Sub(now).Round(time.Second), time.Second), true
	}
	return 0, false
}

func codeToStatus(code connect.Code) int {
	switch code {
	case connect.CodeNotFound:
		return http.StatusNotFound
	case connect.CodeAlreadyExists:
		return http.StatusConflict
	case connect.CodeInvalidArgument:
		return http.StatusBadRequest
	case connect.CodeFailedPrecondition:
		return http.StatusPreconditionFailed
	case connect.CodeUnauthenticated:
		return http.StatusUnauthorized
	case connect.CodePermissionDenied:
		return http.StatusForbidden
	case connect.CodeUnimplemented:
		return http.StatusNotImplemented
	case connect.CodeResourceExhausted:
		// Connect's HTTP mapping: a rate limit, a quota, a body limit.
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}
