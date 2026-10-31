package webhook_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv/memory"
	"hop.top/kit/go/transport/webhook"
)

const vectorsPath = "../../../contracts/webhook-v1/vectors.json"

type corpus struct {
	Version     string   `json:"version"`
	Now         string   `json:"now"`
	SecretParts []string `json:"secret_parts"`
	HMACVectors []struct {
		Hash, Encoding, Prefix, Timestamp, Body, Expected string
	} `json:"hmac_vectors"`
	Cases []struct {
		Name     string         `json:"name"`
		Preset   string         `json:"preset"`
		Config   map[string]any `json:"config"`
		Requests []struct {
			Method  string            `json:"method"`
			Target  string            `json:"target"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
			Sign    map[string]string `json:"sign"`
			Tamper  bool              `json:"tamper"`
			Basic   map[string]string `json:"basic"`
			Want    struct {
				Status      int            `json:"status"`
				Code        string         `json:"code"`
				Body        *string        `json:"body"`
				BodyJSON    map[string]any `json:"body_json"`
				ContentType string         `json:"content_type"`
				Published   int            `json:"published"`
				Topic       string         `json:"topic"`
				Key         string         `json:"key"`
				Kind        string         `json:"kind"`
				Encoding    string         `json:"encoding"`
			} `json:"want"`
		} `json:"requests"`
	} `json:"cases"`
}

type event struct {
	topic   string
	payload map[string]any
}

type recorder struct {
	mu       sync.Mutex
	events   []event
	failures int
}

func (r *recorder) Publish(_ context.Context, topic, _ string, payload any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failures > 0 {
		r.failures--
		return errors.New("bus down")
	}
	r.events = append(r.events, event{topic, payload.(map[string]any)})
	return nil
}

func hasher(name string) func() hash.Hash {
	switch name {
	case "sha1":
		return sha1.New //nolint:gosec
	case "sha512":
		return sha512.New
	}
	return sha256.New
}

// digest is the signature a sender computes, written out here and not
// taken from the package under test.
func digest(hashName, enc, prefix, ts string, body []byte, secret string) string {
	m := hmac.New(hasher(hashName), []byte(secret))
	if ts != "" {
		m.Write([]byte(ts + "."))
	}
	m.Write(body)
	sum := m.Sum(nil)
	if enc == "base64" {
		return prefix + base64.StdEncoding.EncodeToString(sum)
	}
	return prefix + hex.EncodeToString(sum)
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func num(m map[string]any, k string) int    { f, _ := m[k].(float64); return int(f) }

func TestWebhookContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash(vectorsPath))
	require.NoError(t, err)
	var c corpus
	require.NoError(t, json.Unmarshal(raw, &c))
	require.Equal(t, "v1", c.Version)
	secret := strings.Join(c.SecretParts, "")
	sub := func(s string) string { return strings.ReplaceAll(s, "{{SECRET}}", secret) }
	now, err := time.Parse(time.RFC3339, c.Now)
	require.NoError(t, err)

	for _, v := range c.HMACVectors {
		got, err := webhook.SignHMAC(webhook.HMACConfig{
			Header: "X", Secret: []byte(secret), Hash: v.Hash, Encoding: v.Encoding, Prefix: v.Prefix,
			TimestampHeader: map[bool]string{true: "T", false: ""}[v.Timestamp != ""],
		}, v.Timestamp, []byte(v.Body))
		require.NoError(t, err)
		assert.Equal(t, v.Expected, got, "%s %s %q", v.Hash, v.Encoding, v.Body)
	}

	for _, tc := range c.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			rec := &recorder{failures: num(tc.Config, "publish_failures")}
			store := memory.New()
			opts := []webhook.Option{
				webhook.WithPublisher(rec),
				webhook.WithIdempotency(store, time.Hour, nil),
				webhook.WithClock(func() time.Time { return now }),
			}
			if n := num(tc.Config, "max_body"); n > 0 {
				opts = append(opts, webhook.WithMaxBody(int64(n)))
			}
			window := time.Duration(num(tc.Config, "replay_window_seconds")) * time.Second
			switch tc.Preset {
			case "graph":
				opts = append(opts, webhook.Graph(webhook.GraphConfig{ClientState: sub(str(tc.Config, "client_state"))})...)
			case "pipedrive":
				opts = append(opts, webhook.Pipedrive(webhook.PipedriveConfig{
					User: str(tc.Config, "user"), Password: sub(str(tc.Config, "password")), ReplayWindow: window,
				})...)
			case "hmac":
				h := webhook.HMACConfig{
					Header: str(tc.Config, "header"), Secret: []byte(sub(str(tc.Config, "secret"))),
					Hash: str(tc.Config, "hash"), Encoding: str(tc.Config, "encoding"),
					Prefix: str(tc.Config, "prefix"), TimestampHeader: str(tc.Config, "timestamp_header"),
				}
				o, err := webhook.GenericHMAC(webhook.GenericHMACConfig{
					HMAC: h, ReplayWindow: window, KeyHeader: str(tc.Config, "key_header"),
				})
				require.NoError(t, err)
				opts = append(opts, o...)
			default:
				t.Fatalf("preset %q", tc.Preset)
			}
			rc, err := webhook.New("fixture", opts...)
			require.NoError(t, err)

			for i, rq := range tc.Requests {
				body := sub(rq.Body)
				req := httptest.NewRequest(rq.Method, rq.Target, strings.NewReader(body))
				for k, v := range rq.Headers {
					req.Header.Set(k, sub(v))
				}
				if rq.Sign != nil {
					signed := []byte(body)
					if rq.Tamper {
						signed = append(signed, 'x')
					}
					req.Header.Set(rq.Sign["header"], digest(rq.Sign["hash"], rq.Sign["encoding"],
						rq.Sign["prefix"], req.Header.Get(rq.Sign["timestamp_header"]), signed, secret))
				}
				if rq.Basic != nil {
					req.SetBasicAuth(rq.Basic["user"], sub(rq.Basic["password"]))
				}
				w := httptest.NewRecorder()
				rc.ServeHTTP(w, req)

				ctx := tc.Name + " #" + string(rune('1'+i))
				assert.Equal(t, rq.Want.Status, w.Code, ctx)
				if rq.Want.Code != "" {
					var e map[string]any
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e), ctx)
					assert.Equal(t, rq.Want.Code, e["code"], ctx)
					assert.NotContains(t, w.Body.String(), secret, ctx)
				}
				if rq.Want.Body != nil {
					assert.Equal(t, *rq.Want.Body, w.Body.String(), ctx)
				}
				if rq.Want.BodyJSON != nil {
					var got map[string]any
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got), ctx)
					assert.Equal(t, rq.Want.BodyJSON, got, ctx)
				}
				if rq.Want.ContentType != "" {
					assert.Equal(t, rq.Want.ContentType, w.Header().Get("Content-Type"), ctx)
				}
				rec.mu.Lock()
				assert.Len(t, rec.events, rq.Want.Published, ctx)
				if rq.Want.Topic != "" && len(rec.events) > 0 {
					last := rec.events[len(rec.events)-1]
					assert.Equal(t, rq.Want.Topic, last.topic, ctx)
					if rq.Want.Key != "" {
						assert.Equal(t, rq.Want.Key, last.payload["key"], ctx)
					}
					assert.Equal(t, "fixture", last.payload["receiver"], ctx)
				}
				if rq.Want.Kind != "" && len(rec.events) > 0 {
					assert.Equal(t, rq.Want.Kind, rec.events[len(rec.events)-1].payload["kind"], ctx)
				}
				if rq.Want.Encoding != "" && len(rec.events) > 0 {
					p := rec.events[len(rec.events)-1].payload
					assert.Equal(t, rq.Want.Encoding, p["encoding"], ctx)
					if rq.Want.Encoding == "base64" {
						assert.Equal(t, base64.StdEncoding.EncodeToString([]byte(body)), p["body"], ctx)
					}
				}
				rec.mu.Unlock()
			}
		})
	}
}

var _ = http.StatusOK
