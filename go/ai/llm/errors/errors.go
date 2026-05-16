// Package errors defines structured error types for the llm package.
//
// All error types are compatible with [errors.Is] and [errors.As].
// Use [IsFallbackable] to determine whether an error should trigger
// fallback to the next provider.
package errors

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// ErrProviderNotFound indicates the URI scheme is not registered.
type ErrProviderNotFound struct {
	Scheme string
}

func (e *ErrProviderNotFound) Error() string {
	return fmt.Sprintf("provider not found: scheme %q", e.Scheme)
}

func (e *ErrProviderNotFound) Unwrap() error { return nil }

// NewProviderNotFound creates an [ErrProviderNotFound].
func NewProviderNotFound(scheme string) error {
	return &ErrProviderNotFound{Scheme: scheme}
}

// ErrCapabilityNotSupported indicates the adapter does not implement
// the requested interface.
type ErrCapabilityNotSupported struct {
	Capability string
	Provider   string
}

func (e *ErrCapabilityNotSupported) Error() string {
	return fmt.Sprintf("capability %q not supported by provider %q",
		e.Capability, e.Provider)
}

func (e *ErrCapabilityNotSupported) Unwrap() error { return nil }

// NewCapabilityNotSupported creates an [ErrCapabilityNotSupported].
func NewCapabilityNotSupported(capability, provider string) error {
	return &ErrCapabilityNotSupported{
		Capability: capability,
		Provider:   provider,
	}
}

// ErrAuth indicates an authentication/authorization failure (401/403).
type ErrAuth struct {
	Provider string
	Err      error
}

func (e *ErrAuth) Error() string {
	return fmt.Sprintf("auth error (provider %q): %v", e.Provider, e.Err)
}

func (e *ErrAuth) Unwrap() error { return e.Err }

// NewAuth creates an [ErrAuth] wrapping the underlying cause.
func NewAuth(provider string, err error) error {
	return &ErrAuth{Provider: provider, Err: err}
}

// ErrRateLimit indicates a 429 response.
type ErrRateLimit struct {
	Provider   string
	RetryAfter time.Duration
}

func (e *ErrRateLimit) Error() string {
	if e.RetryAfter == 0 {
		return fmt.Sprintf("rate limited (provider %q)", e.Provider)
	}
	return fmt.Sprintf("rate limited (provider %q, retry after %s)",
		e.Provider, e.RetryAfter)
}

func (e *ErrRateLimit) Unwrap() error { return nil }

// NewRateLimit creates an [ErrRateLimit].
func NewRateLimit(provider string, retryAfter time.Duration) error {
	return &ErrRateLimit{Provider: provider, RetryAfter: retryAfter}
}

// ErrContext indicates a context cancellation or deadline exceeded.
type ErrContext struct {
	Err error
}

func (e *ErrContext) Error() string {
	return fmt.Sprintf("context error: %v", e.Err)
}

func (e *ErrContext) Unwrap() error { return e.Err }

// NewContext creates an [ErrContext] wrapping the underlying cause.
func NewContext(err error) error {
	return &ErrContext{Err: err}
}

// ErrModel indicates the model is not found or not available.
type ErrModel struct {
	Model    string
	Provider string
}

func (e *ErrModel) Error() string {
	return fmt.Sprintf("model %q not available (provider %q)",
		e.Model, e.Provider)
}

func (e *ErrModel) Unwrap() error { return nil }

// NewModel creates an [ErrModel].
func NewModel(model, provider string) error {
	return &ErrModel{Model: model, Provider: provider}
}

// ErrFallbackExhausted indicates all providers in the fallback chain
// have failed. The Errors field holds each provider's error.
type ErrFallbackExhausted struct {
	Errors []error
}

func (e *ErrFallbackExhausted) Error() string {
	return fmt.Sprintf("all providers failed (%d errors)", len(e.Errors))
}

// Unwrap returns nil; individual errors are accessible via Errors
// field through [errors.As].
func (e *ErrFallbackExhausted) Unwrap() error { return nil }

// NewFallbackExhausted creates an [ErrFallbackExhausted].
func NewFallbackExhausted(errs []error) error {
	return &ErrFallbackExhausted{Errors: errs}
}

// HTTPStatusError represents an HTTP error response. Used to classify
// 5xx errors as fallbackable.
type HTTPStatusError struct {
	StatusCode int
	Status     string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("http %d: %s", e.StatusCode, e.Status)
}

// NewHTTPStatusError creates an [HTTPStatusError].
func NewHTTPStatusError(code int, status string) error {
	return &HTTPStatusError{StatusCode: code, Status: status}
}

// ErrUnsupportedModality indicates the provider does not support
// the requested modality (e.g. image, audio, video).
type ErrUnsupportedModality struct {
	Modality string
	Provider string
	Err      error
}

func (e *ErrUnsupportedModality) Error() string {
	return fmt.Sprintf("modality %q not supported by provider %q: %v",
		e.Modality, e.Provider, e.Err)
}

func (e *ErrUnsupportedModality) Unwrap() error { return e.Err }

// NewUnsupportedModality creates an [ErrUnsupportedModality].
func NewUnsupportedModality(modality, provider string, err error) error {
	return &ErrUnsupportedModality{Modality: modality, Provider: provider, Err: err}
}

// ErrMediaTooLarge indicates the media payload exceeds the provider limit.
type ErrMediaTooLarge struct {
	Size     int64
	Limit    int64
	Provider string
	Err      error
}

func (e *ErrMediaTooLarge) Error() string {
	return fmt.Sprintf("media too large (%d bytes, limit %d) for provider %q: %v",
		e.Size, e.Limit, e.Provider, e.Err)
}

func (e *ErrMediaTooLarge) Unwrap() error { return e.Err }

// NewMediaTooLarge creates an [ErrMediaTooLarge].
func NewMediaTooLarge(size, limit int64, provider string, err error) error {
	return &ErrMediaTooLarge{Size: size, Limit: limit, Provider: provider, Err: err}
}

// ErrInvalidFormat indicates the media format is not accepted by the provider.
type ErrInvalidFormat struct {
	Format   string
	Provider string
	Err      error
}

func (e *ErrInvalidFormat) Error() string {
	return fmt.Sprintf("invalid format %q for provider %q: %v",
		e.Format, e.Provider, e.Err)
}

func (e *ErrInvalidFormat) Unwrap() error { return e.Err }

// NewInvalidFormat creates an [ErrInvalidFormat].
func NewInvalidFormat(format, provider string, err error) error {
	return &ErrInvalidFormat{Format: format, Provider: provider, Err: err}
}

// ErrRouterUnavailable indicates the router server is not running or
// unreachable. Fallbackable — should degrade to the strong model.
type ErrRouterUnavailable struct {
	Router string
	Err    error
}

func (e *ErrRouterUnavailable) Error() string {
	return fmt.Sprintf("router %q unavailable: %v", e.Router, e.Err)
}

func (e *ErrRouterUnavailable) Unwrap() error { return e.Err }

// NewRouterUnavailable creates an [ErrRouterUnavailable].
func NewRouterUnavailable(router string, err error) error {
	return &ErrRouterUnavailable{Router: router, Err: err}
}

// ErrRouterTimeout indicates that scoring took too long.
// Fallbackable — should degrade to the strong model.
type ErrRouterTimeout struct {
	Router  string
	Timeout time.Duration
}

func (e *ErrRouterTimeout) Error() string {
	return fmt.Sprintf("router %q timed out after %s", e.Router, e.Timeout)
}

func (e *ErrRouterTimeout) Unwrap() error { return nil }

// NewRouterTimeout creates an [ErrRouterTimeout].
func NewRouterTimeout(router string, timeout time.Duration) error {
	return &ErrRouterTimeout{Router: router, Timeout: timeout}
}

// ErrContractViolation indicates an eva contract failed validation.
// NOT fallbackable.
type ErrContractViolation struct {
	Contract   string
	Violations []string
}

func (e *ErrContractViolation) Error() string {
	return fmt.Sprintf("contract %q violated: %s",
		e.Contract, strings.Join(e.Violations, "; "))
}

func (e *ErrContractViolation) Unwrap() error { return nil }

// NewContractViolation creates an [ErrContractViolation].
func NewContractViolation(contract string, violations []string) error {
	return &ErrContractViolation{Contract: contract, Violations: violations}
}

// ErrThresholdInvalid indicates the threshold is outside the [0,1] range.
// NOT fallbackable.
type ErrThresholdInvalid struct {
	Threshold float64
}

func (e *ErrThresholdInvalid) Error() string {
	return fmt.Sprintf("threshold %.4f out of [0,1] range", e.Threshold)
}

func (e *ErrThresholdInvalid) Unwrap() error { return nil }

// NewThresholdInvalid creates an [ErrThresholdInvalid].
func NewThresholdInvalid(threshold float64) error {
	return &ErrThresholdInvalid{Threshold: threshold}
}

// IsFallbackable returns true when the error should trigger fallback
// to the next provider. Network errors, rate limits, and 5xx responses
// are fallbackable. Auth errors, model errors, capability errors, and
// context errors are not.
func IsFallbackable(err error) bool {
	if err == nil {
		return false
	}

	// Rate limit — fallbackable.
	var rl *ErrRateLimit
	if errors.As(err, &rl) {
		return true
	}

	// Router unavailable — fallbackable (degrade to strong model).
	var ruErr *ErrRouterUnavailable
	if errors.As(err, &ruErr) {
		return true
	}

	// Router timeout — fallbackable (degrade to strong model).
	var rtErr *ErrRouterTimeout
	if errors.As(err, &rtErr) {
		return true
	}

	// Network error — fallbackable.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	// 5xx HTTP status — fallbackable.
	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode >= 500
	}

	// Non-fallbackable error types.
	var authErr *ErrAuth
	if errors.As(err, &authErr) {
		return false
	}
	var modelErr *ErrModel
	if errors.As(err, &modelErr) {
		return false
	}
	var capErr *ErrCapabilityNotSupported
	if errors.As(err, &capErr) {
		return false
	}
	var ctxErr *ErrContext
	if errors.As(err, &ctxErr) {
		return false
	}
	var modalErr *ErrUnsupportedModality
	if errors.As(err, &modalErr) {
		return false
	}
	var sizeErr *ErrMediaTooLarge
	if errors.As(err, &sizeErr) {
		return false
	}
	var fmtErr *ErrInvalidFormat
	if errors.As(err, &fmtErr) {
		return false
	}
	var cvErr *ErrContractViolation
	if errors.As(err, &cvErr) {
		return false
	}
	var thErr *ErrThresholdInvalid
	if errors.As(err, &thErr) {
		return false
	}

	return false
}
