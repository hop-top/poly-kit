// Package jev is a judge.Judge backed by Jev, TypeSafe's System One
// decision model, through OpenRouter's /systemone route. Jev returns typed
// answers with probabilities and never text, so every answer is native:
// no sampling, no verbalized confidence. The backend is always cloud:
// each Judge call sends the case's state and questions to the service.
//
// Question mapping: choice sends {type: choice, criteria: {value:
// description}}; score sends {type: score, criteria: anchors}; yes/no
// sends {type: noul}; grounding (visual grounding, Jev's "box", preview)
// sends {type: box} and answers with a bounding box. Jev has no abstain:
// a grounding question that allows it abstains when the service returns no
// box.
//
// Images: every image in Case.Assets is sent base64 (PNG or JPEG) under
// "assets", referenced from the state by its name (see judge.ImageNames),
// with the TypeSafe-Danger-Zone-Images header the service requires for
// images (an early feature, preview). Other asset types and image formats
// fail with an llmerrors.ErrCapabilityNotSupported before anything is
// sent.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
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

	// provider is the llm scheme whose key and endpoint trust apply. No
	// adapter is registered for it here: linking one would pull a
	// provider SDK into every importer, so the key is resolved below.
	provider = "openrouter"
	// maxScoreLevels is the service's cap on score criteria.
	maxScoreLevels = 10
	// imagesHeader opts a request into the service's image handling.
	imagesHeader = "TypeSafe-Danger-Zone-Images"
	// maxImage bounds one image's bytes.
	maxImage = 20 << 20
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
	hermetic  bool
	transport http.RoundTripper
}

// WithURL sets the endpoint. The key is sent only to DefaultURL's origin,
// a loopback host, or an origin llm.yaml base_url / LLM_BASE_URL names
// (not under Hermetic); New fails with llm.ErrKeyEndpoint for any other.
func WithURL(u string) Option { return func(c *config) { c.url = u } }

// WithModel sets the model on every request; default DefaultModel.
func WithModel(m string) Option { return func(c *config) { c.model = m } }

// WithAPIKey sets the OpenRouter key; it outranks every other source.
// Unset (and not Hermetic), the key resolves from llm.yaml
// providers.openrouter (api_key, then the variable api_key_env names),
// then OPENROUTER_API_KEY, then LLM_API_KEY. Blank values count as unset.
func WithAPIKey(k string) Option { return func(c *config) { c.key = k } }

// Hermetic makes the judge read neither the environment nor llm.yaml: the
// key must come from WithAPIKey (New fails with llm.ErrMissingKey
// otherwise) and the endpoint is trusted only when it shares DefaultURL's
// origin or is loopback; a configured base_url or LLM_BASE_URL is not
// read. For tools that pass their own credentials and take nothing from
// the host.
func Hermetic() Option { return func(c *config) { c.hermetic = true } }

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
		k, err := resolveKey(c.hermetic)
		if err != nil {
			return nil, fmt.Errorf("jev: %w", err)
		}
		c.key = k
	}
	if err := llm.CheckKeyEndpoint(provider, c.key, c.url); err != nil {
		return nil, err
	}
	if !trusted(c.url, c.hermetic) {
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

// keyEnvNames are the environment variables consulted, in order, when
// llm.yaml names no key.
var keyEnvNames = []string{"OPENROUTER_API_KEY", llm.FallbackEnvKey}

// resolveKey finds the OpenRouter key without an llm adapter: llm.yaml
// providers.openrouter (api_key, api_key_env) through llm.SecretFor, then
// keyEnvNames. Hermetic reads nothing and always fails.
func resolveKey(hermetic bool) (string, error) {
	if hermetic {
		return "", fmt.Errorf("%w: Hermetic needs WithAPIKey", llm.ErrMissingKey)
	}
	if k, err := llm.SecretFor(context.Background(), nil, provider); err == nil {
		return k, nil
	}
	for _, name := range keyEnvNames {
		if k := os.Getenv(name); strings.TrimSpace(k) != "" {
			return k, nil
		}
	}
	return "", &llm.MissingKeyError{Scheme: provider, EnvVars: keyEnvNames}
}

// trusted reports whether the key may go to endpoint. Hermetic trusts
// DefaultURL's origin and loopback only; otherwise llm adds the origins
// llm.yaml base_url and LLM_BASE_URL name.
func trusted(endpoint string, hermetic bool) bool {
	if !hermetic {
		return llm.CredentialsTrusted(provider, endpoint, DefaultURL)
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return false
	}
	if isLoopback(u.Hostname()) {
		return true
	}
	d, err := url.Parse(DefaultURL)
	return err == nil && strings.EqualFold(u.Scheme, d.Scheme) &&
		strings.EqualFold(u.Host, d.Host)
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Judge answers every question of c in one request. Question kinds,
// asset types and image formats the service cannot take fail with an
// llmerrors.ErrCapabilityNotSupported; a malformed
// case fails with judge.ErrInvalid; a response that does not answer the
// case fails with ErrMalformed. HTTP failures map to llm errors: 401 and
// 403 to ErrAuth and ErrPermissionDenied, 404 to ErrModel, 429 to
// ErrRateLimit (Retry-After in seconds), 5xx to HTTPStatusError. There is
// no retry here; wrap the judge if you want one.
func (j *jevJudge) Judge(ctx context.Context, c judge.Case) (judge.Verdict, error) {
	for _, q := range c.Questions {
		switch q.Kind {
		case judge.KindChoice, judge.KindScore, judge.KindYesNo, judge.KindGrounding:
		default:
			return judge.Verdict{}, llmerrors.NewCapabilityNotSupported(
				fmt.Sprintf("question kind %q", q.Kind), Backend)
		}
	}
	for _, a := range c.Assets {
		if a.Type != llm.PartTypeImage {
			return judge.Verdict{}, llmerrors.NewCapabilityNotSupported(
				fmt.Sprintf("asset type %q", a.Type), Backend)
		}
		if _, ok := imageFormat(a); !ok {
			return judge.Verdict{}, llmerrors.NewCapabilityNotSupported(
				fmt.Sprintf("image type %q (png and jpeg only)", mimeOf(a)), Backend)
		}
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

	req, err := buildRequest(ctx, j.model, c)
	if err != nil {
		return judge.Verdict{}, err
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return judge.Verdict{}, fmt.Errorf("jev: encode request: %w", err)
	}
	status, header, body, err := j.post(ctx, payload, len(req.Assets) > 0)
	if err != nil {
		return judge.Verdict{}, err
	}
	if status != http.StatusOK {
		return judge.Verdict{}, j.statusError(status, header, body)
	}
	return parseResponse(c, j.model, body)
}

// post sends payload and returns the status, headers and bounded body.
func (j *jevJudge) post(ctx context.Context, payload []byte, images bool) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.url, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("jev: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+j.key)
	req.Header.Set("Content-Type", "application/json")
	if images {
		req.Header.Set(imagesHeader, "true")
	}
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
