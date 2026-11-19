// Package jev is a judge.Judge backed by Jev, TypeSafe's System One
// decision model, through OpenRouter's /systemone route. Jev returns typed
// answers with probabilities and never text, so every answer is native:
// no sampling, no verbalized confidence. The backend is always cloud:
// each Judge call sends the case's state and questions to the service.
//
// Question mapping: choice sends {type: choice, criteria: {value:
// description}}; score sends {type: score, criteria: anchors}; yes/no
// sends {type: noul}. Jev has no abstain and no box (image localization)
// support here; a box question or a case with assets fails with an
// llmerrors.ErrCapabilityNotSupported before anything is sent.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"

	// Registers the openrouter scheme, so llm.ProviderKeyFor and
	// llm.SecretFor know where its key lives.
	_ "hop.top/kit/go/ai/llm/openai"
)

const (
	// Backend is Verdict.Backend.
	Backend = "jev"
	// SourceNative marks answers read straight off the service's output.
	SourceNative = "native"

	// DefaultURL is OpenRouter's System One route; the only endpoint (bar
	// loopback) the key goes to unless llm.yaml base_url or LLM_BASE_URL
	// trust another.
	DefaultURL = "https://openrouter.ai/api/v1/systemone"
	// DefaultModel is a decisions model reachable on DefaultURL. The
	// chat-listed router model typesafe/jev-router is not served there.
	DefaultModel = "typesafe/jev-1.14"

	// provider is the llm scheme whose key and endpoint trust apply.
	provider = "openrouter"
	// maxScoreLevels is the service's cap on score criteria.
	maxScoreLevels = 10
	// maxBody bounds a response body.
	maxBody = 4 << 20
	// maxDetail bounds the error body quoted in an error message.
	maxDetail = 300
)

// ErrMalformed matches (errors.Is) a response that is not a valid answer
// to the case: undecodable, wrong shape, missing or extra answers,
// probabilities that are not a distribution over the question's labels.
var ErrMalformed = errors.New("jev: malformed response")

// Option configures a judge.
type Option func(*config)

type config struct {
	url       string
	model     string
	key       string
	transport http.RoundTripper
}

// WithURL sets the endpoint. The key is sent only to DefaultURL's origin,
// a loopback host, or an origin llm.yaml base_url / LLM_BASE_URL names;
// New fails with llm.ErrKeyEndpoint for any other.
func WithURL(u string) Option { return func(c *config) { c.url = u } }

// WithModel sets the model on every request; default DefaultModel.
func WithModel(m string) Option { return func(c *config) { c.model = m } }

// WithAPIKey sets the OpenRouter key; unset resolves it as
// llm.SecretFor does for "openrouter" (llm.yaml, secret store, then the
// names llm.ProviderKeyFor lists, OPENROUTER_API_KEY first).
func WithAPIKey(k string) Option { return func(c *config) { c.key = k } }

// WithTransport sends requests through rt, as llm.WithTransport does: a
// proxy, a cassette, a fake. Nil keeps the process default. Redirects are
// never followed.
func WithTransport(rt http.RoundTripper) Option { return func(c *config) { c.transport = rt } }

type jevJudge struct {
	url    string
	model  string
	key    string
	client *http.Client
}

// New returns a judge for the service. It fails when no key resolves
// (errors.Is llm.ErrMissingKey) or the key may not go to the endpoint
// (errors.Is llm.ErrKeyEndpoint); no request is made.
func New(opts ...Option) (judge.Judge, error) {
	c := config{url: DefaultURL, model: DefaultModel}
	for _, o := range opts {
		o(&c)
	}
	if strings.TrimSpace(c.key) == "" {
		k, err := llm.SecretFor(context.Background(), nil, provider)
		if err != nil {
			return nil, fmt.Errorf("jev: %w", err)
		}
		c.key = k
	}
	if err := llm.CheckKeyEndpoint(provider, c.key, c.url); err != nil {
		return nil, err
	}
	if !llm.CredentialsTrusted(provider, c.url, DefaultURL) {
		return nil, &llm.KeyEndpointError{Scheme: provider, Endpoint: llm.RedactURI(c.url)}
	}
	rt := c.transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	return &jevJudge{
		url:   c.url,
		model: c.model,
		key:   c.key,
		client: &http.Client{
			Transport: rt,
			// A redirect would move the key to a host the server names.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Judge answers every question of c in one request. Box questions and
// assets fail with an llmerrors.ErrCapabilityNotSupported; a malformed
// case fails with judge.ErrInvalid; a response that does not answer the
// case fails with ErrMalformed. HTTP failures map to llm errors: 401 and
// 403 to ErrAuth and ErrPermissionDenied, 404 to ErrModel, 429 to
// ErrRateLimit (Retry-After in seconds), 5xx to HTTPStatusError. There is
// no retry here; wrap the judge if you want one.
func (j *jevJudge) Judge(ctx context.Context, c judge.Case) (judge.Verdict, error) {
	for _, q := range c.Questions {
		switch q.Kind {
		case judge.KindChoice, judge.KindScore, judge.KindYesNo:
		default:
			return judge.Verdict{}, llmerrors.NewCapabilityNotSupported(
				fmt.Sprintf("question kind %q", q.Kind), Backend)
		}
	}
	if len(c.Assets) > 0 {
		return judge.Verdict{}, llmerrors.NewCapabilityNotSupported("assets", Backend)
	}
	if err := judge.Validate(c); err != nil {
		return judge.Verdict{}, err
	}
	for _, q := range c.Questions {
		if q.Kind == judge.KindScore && len(q.Anchors) > maxScoreLevels {
			return judge.Verdict{}, fmt.Errorf("%w: question %q: jev takes at most %d score anchors, got %d",
				judge.ErrInvalid, q.Name, maxScoreLevels, len(q.Anchors))
		}
	}

	payload, err := json.Marshal(buildRequest(j.model, c))
	if err != nil {
		return judge.Verdict{}, fmt.Errorf("jev: encode request: %w", err)
	}
	status, header, body, err := j.post(ctx, payload)
	if err != nil {
		return judge.Verdict{}, err
	}
	if status != http.StatusOK {
		return judge.Verdict{}, j.statusError(status, header, body)
	}
	return parseResponse(c, j.model, body)
}

// post sends payload and returns the status, headers and bounded body.
func (j *jevJudge) post(ctx context.Context, payload []byte) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.url, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("jev: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+j.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := j.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, llmerrors.NewContext(err)
		}
		return 0, nil, nil, fmt.Errorf("jev: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, llmerrors.NewContext(err)
		}
		return 0, nil, nil, fmt.Errorf("jev: read response: %w", err)
	}
	if len(body) > maxBody {
		return 0, nil, nil, llmerrors.NewResponseTooLarge(Backend, "body", maxBody)
	}
	return resp.StatusCode, resp.Header, body, nil
}

// statusError maps a non-200 response to an llm error.
func (j *jevJudge) statusError(status int, h http.Header, body []byte) error {
	text := fmt.Sprintf("%d %s", status, http.StatusText(status))
	switch {
	case status == http.StatusUnauthorized:
		return llmerrors.NewAuth(Backend, errors.New(text))
	case status == http.StatusForbidden:
		return llmerrors.NewPermissionDenied(Backend, errors.New(text))
	case status == http.StatusNotFound:
		return llmerrors.NewModel(j.model, Backend)
	case status == http.StatusTooManyRequests:
		return llmerrors.NewRateLimit(Backend, retryAfter(h))
	case status >= 500:
		return llmerrors.NewHTTPStatusError(status, http.StatusText(status))
	}
	detail := strings.ReplaceAll(string(body), j.key, "<redacted>")
	if len(detail) > maxDetail {
		detail = detail[:maxDetail] + "..."
	}
	return fmt.Errorf("jev: unexpected status %s: %s", text, strings.TrimSpace(detail))
}

// retryAfter reads a Retry-After in whole seconds; anything else is 0.
func retryAfter(h http.Header) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After")))
	if err != nil || n < 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}
