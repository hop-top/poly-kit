package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultMaxCassetteSize is the body cap applied when WithMaxCassetteSize
// is not set. 50 MiB matches design.md §2.
const DefaultMaxCassetteSize int64 = 50 * 1024 * 1024

// New constructs a Client targeting baseURL. baseURL is the bare
// service origin (e.g. "https://12fcc.example.com"); endpoint paths
// are appended internally. Trailing slashes are trimmed.
//
// New does not read environment variables — that is the CLI leaf's
// job. Callers embedding the client in a test binary thereby avoid
// surprise env reads.
func New(baseURL string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, ServiceUsageError("baseURL is required", "",
			"pass a URL to client.New or set --service")
	}
	c := &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		http:        &http.Client{},
		userAgent:   "kit-conformance-client/" + ClientVersion,
		maxAttempts: 3,
		backoff:     defaultBackoff(),
		maxCassette: DefaultMaxCassetteSize,
		now:         time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Grade uploads a packed cassette to the configured svc URL and
// returns the typed Result. If svc responds with 202 + a poll URL,
// Grade polls until 200 or ctx expires.
//
// req.CassetteDir is required. If the manifest cannot be loaded or
// the cassette dir cannot be walked, Grade returns ErrCassettePack or
// ErrManifestParse. Network/service errors are retried per the
// backoff policy (default 3 attempts).
func (c *Client) Grade(ctx context.Context, req GradeRequest) (*Result, error) {
	if req.CassetteDir == "" {
		return nil, ServiceUsageError("cassetteDir is required", "", "")
	}

	manifest, err := LoadManifest(req.CassetteDir)
	if err != nil {
		return nil, err
	}
	// Apply overrides.
	if req.ScenarioID != "" {
		manifest.ScenarioID = req.ScenarioID
	}
	if req.StoryPath != "" {
		manifest.StoryPath = req.StoryPath
	}
	if req.Tier > 0 {
		manifest.Tier = req.Tier
	}

	// Pack. Pack returns a deterministic body + the idempotency key.
	body, idemKey, err := Pack(req.CassetteDir, manifest, c.maxCassette)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, CassettePackError("read packed body", err.Error())
	}

	endpoint := c.baseURL + "/v1/grade"

	var lastResp *Result
	var lastErr error
	for attempt := 0; attempt < c.maxAttempts; attempt++ {
		if attempt > 0 {
			delay := c.backoff.delay(attempt - 1)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ServiceUnavailableError("ctx canceled mid-retry",
					ctx.Err().Error(), "")
			}
		}
		res, retryAfter, err := c.postGrade(ctx, endpoint, bodyBytes, idemKey, manifest)
		if err == nil {
			if res != nil && res.gradeID != "" && res.result == nil {
				// 202 — poll loop.
				pollURL := res.pollURL
				if pollURL == "" {
					pollURL = "/v1/grade/" + res.gradeID
				}
				return c.pollResult(ctx, pollURL, retryAfter)
			}
			if res != nil && res.result != nil {
				return res.result, nil
			}
			// Shouldn't reach here, but if so report a usage error so
			// it doesn't become an infinite retry.
			return nil, ServiceUsageError("empty response body from /v1/grade", "", "")
		}
		if !IsRetryable(err) {
			return lastResp, err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = ServiceUnavailableError("retry budget exhausted", "",
			"increase --retries or check service health")
	}
	return nil, lastErr
}

// Status fetches the result of an in-flight or completed grade by ID.
// Used by adopters who fire Grade with a short context, persist the
// grade-id, and reconnect later.
func (c *Client) Status(ctx context.Context, gradeID string) (*Result, error) {
	if gradeID == "" {
		return nil, ServiceUsageError("gradeID is required", "", "")
	}
	pollURL := "/v1/grade/" + gradeID
	return c.pollResult(ctx, pollURL, 0)
}

// gradeResp is the in-flight envelope returned by postGrade. result
// is non-nil only on synchronous 200; on 202, gradeID + pollURL carry
// the polling info.
type gradeResp struct {
	result  *Result
	gradeID string
	pollURL string
}

// postGrade performs one POST attempt and decodes the response. The
// retryAfter return is the parsed Retry-After value (0 if absent or
// unparseable); the retry loop honors it on 429.
func (c *Client) postGrade(ctx context.Context, url string, body []byte, idemKey string, manifest *Manifest) (*gradeResp, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, 0, ServiceUsageError("build request", err.Error(), "")
	}
	req.Header.Set("Content-Type", CassetteMIMEType)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("X-Kit-Client-Version", ClientVersion)
	req.Header.Set("X-Kit-Cassette-Schema-Version", CassetteSchemaVersion)
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.ContentLength = int64(len(body))

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, ServiceUnavailableError("POST /v1/grade", err.Error(),
			"check --service URL and network reachability")
	}
	defer resp.Body.Close()
	return c.decodeGradeResponse(resp)
}

// decodeGradeResponse maps an HTTP response to a (*gradeResp,
// retryAfter, err) triple. 4xx other than 429 are terminal; 429 +
// 5xx + network errors are retryable.
func (c *Client) decodeGradeResponse(resp *http.Response) (*gradeResp, time.Duration, error) {
	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), c.now())

	switch {
	case resp.StatusCode == http.StatusOK:
		var env struct {
			GradeID string          `json:"grade_id"`
			Result  json.RawMessage `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			return nil, 0, ServiceUsageError("decode 200 body", err.Error(), "")
		}
		if len(env.Result) == 0 {
			return nil, 0, ServiceUsageError("200 missing result", "", "")
		}
		var r Result
		if err := json.Unmarshal(env.Result, &r); err != nil {
			return nil, 0, ServiceUsageError("unmarshal result", err.Error(), "")
		}
		return &gradeResp{result: &r, gradeID: env.GradeID}, retryAfter, nil

	case resp.StatusCode == http.StatusAccepted:
		var env struct {
			GradeID           string `json:"grade_id"`
			PollURL           string `json:"poll_url"`
			RetryAfterSeconds int    `json:"retry_after_seconds"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			return nil, 0, ServiceUsageError("decode 202 body", err.Error(), "")
		}
		ra := retryAfter
		if env.RetryAfterSeconds > 0 {
			ra = time.Duration(env.RetryAfterSeconds) * time.Second
		}
		return &gradeResp{gradeID: env.GradeID, pollURL: env.PollURL}, ra, nil

	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, retryAfter, RateLimitedError("429 from grade service")

	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		body, _ := io.ReadAll(resp.Body)
		return nil, 0, ServiceAuthFailedError(
			fmt.Sprintf("svc returned %d", resp.StatusCode),
			strings.TrimSpace(string(body)),
			"set KIT_CONFORMANCE_TOKEN or pass --token")

	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		body, _ := io.ReadAll(resp.Body)
		return nil, 0, ServiceUsageError(
			fmt.Sprintf("svc returned %d", resp.StatusCode),
			strings.TrimSpace(string(body)), "")

	case resp.StatusCode >= 500:
		body, _ := io.ReadAll(resp.Body)
		return nil, retryAfter, ServiceUnavailableError(
			fmt.Sprintf("svc returned %d", resp.StatusCode),
			strings.TrimSpace(string(body)), "")
	}
	return nil, 0, ServiceUsageError(
		fmt.Sprintf("unexpected status %d", resp.StatusCode), "", "")
}

// pollResult polls a 202-returned poll URL until 200 or ctx expires.
// The first retryAfter value is honored as the initial wait; subsequent
// waits follow the same retry-after header (or fall back to 1s).
func (c *Client) pollResult(ctx context.Context, pollURL string, initialWait time.Duration) (*Result, error) {
	full := pollURL
	if !strings.HasPrefix(full, "http") {
		full = c.baseURL + pollURL
	}
	wait := initialWait
	if wait <= 0 {
		wait = time.Second
	}
	for {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ServiceUnavailableError("ctx canceled while polling",
				ctx.Err().Error(), "")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
		if err != nil {
			return nil, ServiceUsageError("build poll request", err.Error(), "")
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, ServiceUnavailableError("poll GET", err.Error(), "")
		}
		switch resp.StatusCode {
		case http.StatusOK:
			var env struct {
				Result json.RawMessage `json:"result"`
			}
			err := json.NewDecoder(resp.Body).Decode(&env)
			resp.Body.Close()
			if err != nil {
				return nil, ServiceUsageError("decode poll body", err.Error(), "")
			}
			var r Result
			if err := json.Unmarshal(env.Result, &r); err != nil {
				return nil, ServiceUsageError("unmarshal poll result", err.Error(), "")
			}
			return &r, nil
		case http.StatusAccepted:
			var env struct {
				RetryAfterSeconds int `json:"retry_after_seconds"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&env)
			resp.Body.Close()
			if env.RetryAfterSeconds > 0 {
				wait = time.Duration(env.RetryAfterSeconds) * time.Second
			} else if ra := parseRetryAfter(resp.Header.Get("Retry-After"), c.now()); ra > 0 {
				wait = ra
			} else {
				wait = 2 * wait
				if wait > c.backoff.MaxBackoff && c.backoff.MaxBackoff > 0 {
					wait = c.backoff.MaxBackoff
				}
			}
		default:
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				return nil, ServiceAuthFailedError(
					fmt.Sprintf("poll returned %d", resp.StatusCode),
					strings.TrimSpace(string(body)), "")
			}
			return nil, ServiceUsageError(
				fmt.Sprintf("poll returned %d", resp.StatusCode),
				strings.TrimSpace(string(body)), "")
		}
	}
}

// delay returns the next backoff duration with jitter applied.
// attempt is 0-indexed (delay(0) is the first retry's wait).
func (b backoffPolicy) delay(attempt int) time.Duration {
	if b.InitialBackoff <= 0 {
		return 0
	}
	d := float64(b.InitialBackoff)
	mult := b.BackoffMultiplier
	if mult <= 0 {
		mult = 2.0
	}
	for i := 0; i < attempt; i++ {
		d *= mult
	}
	maxBackoff := float64(b.MaxBackoff)
	if maxBackoff > 0 && d > maxBackoff {
		d = maxBackoff
	}
	if b.BackoffJitter > 0 {
		jitter := 1 - b.BackoffJitter + 2*b.BackoffJitter*rand.Float64()
		d *= jitter
	}
	return time.Duration(d)
}

// parseRetryAfter understands both numeric (seconds) and HTTP-date
// forms of the Retry-After header. Returns 0 when the header is
// missing or malformed.
func parseRetryAfter(h string, now time.Time) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
