package webhook

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/transport/api"
)

// Defaults.
const (
	// DefaultMaxBody bounds a delivery.
	DefaultMaxBody int64 = 1 << 20
	// DefaultAckBudget is how long a sender waits for the answer at most.
	DefaultAckBudget = 2500 * time.Millisecond
	// DefaultDedupeTTL is how long an idempotency key stays claimed.
	DefaultDedupeTTL = 24 * time.Hour
	// Source is the event source of published deliveries.
	Source = "webhook.receiver"
	// KindNotification and KindLifecycle are the delivery kinds.
	KindNotification = "notification"
	KindLifecycle    = "lifecycle"
)

// Refusal codes.
const (
	CodeUnverified = "webhook_unverified"
	CodeStale      = "webhook_stale"
)

// DefaultTopics are the topics deliveries are published on, by kind.
var DefaultTopics = map[string]bus.Topic{
	KindNotification: "kit.webhook.notification.received",
	KindLifecycle:    "kit.webhook.lifecycle.received",
}

var nameRE = regexp.MustCompile(`^[a-z0-9_-]+$`)

// Receiver is an http.Handler for one webhook endpoint.
type Receiver struct {
	name       string
	verifiers  []Verifier
	handshakes []Handshake
	window     time.Duration
	timestamp  TimestampFunc
	key        KeyFunc
	keyInBody  bool
	sigTS      TimestampFunc
	dedupe     kv.TTLInserter
	dedupeTTL  time.Duration
	pub        api.EventPublisher
	topics     map[string]bus.Topic
	classify   func(body []byte) string
	maxBody    int64
	ackBudget  time.Duration
	now        func() time.Time
	onRefused  func(r *http.Request, code string, err error)
	onError    func(err error)
}

// Option configures [New].
type Option func(*Receiver) error

// New returns a receiver named name (it namespaces idempotency keys and
// is published as "receiver"). It needs a publisher and at least one
// verifier: an open endpoint is a mistake, not a default.
func New(name string, opts ...Option) (*Receiver, error) {
	r := &Receiver{
		name:      name,
		dedupeTTL: DefaultDedupeTTL,
		topics:    map[string]bus.Topic{},
		maxBody:   DefaultMaxBody,
		ackBudget: DefaultAckBudget,
		now:       time.Now,
		key:       BodyHashKey,
		classify:  func([]byte) string { return KindNotification },
	}
	for k, v := range DefaultTopics {
		r.topics[k] = v
	}
	if !nameRE.MatchString(name) {
		return nil, fmt.Errorf("webhook: receiver name %q: want lowercase letters, digits, '-' and '_'", name)
	}
	for _, o := range opts {
		if err := o(r); err != nil {
			return nil, err
		}
	}
	switch {
	case r.pub == nil:
		return nil, errors.New("webhook: a receiver needs a publisher")
	case len(r.verifiers) == 0:
		return nil, errors.New("webhook: a receiver needs a verifier")
	case r.window > 0 && r.timestamp == nil:
		return nil, errors.New("webhook: a replay window needs a timestamp source")
	}
	return r, nil
}

// WithVerifier adds verifiers; every one must accept a delivery.
func WithVerifier(v ...Verifier) Option {
	return func(r *Receiver) error { r.verifiers = append(r.verifiers, v...); return nil }
}

// WithHandshake adds handshakes, tried in order before verification.
func WithHandshake(h ...Handshake) Option {
	return func(r *Receiver) error { r.handshakes = append(r.handshakes, h...); return nil }
}

// WithReplayWindow refuses a delivery whose timestamp is more than
// window from now, either way.
func WithReplayWindow(window time.Duration, ts TimestampFunc) Option {
	return func(r *Receiver) error { r.window, r.timestamp = window, ts; return nil }
}

// WithIdempotency claims each delivery's key in store for ttl (zero
// means [DefaultDedupeTTL]); a repeat is acknowledged and not
// published. key nil keeps the default, [BodyHashKey].
func WithIdempotency(store kv.TTLInserter, ttl time.Duration, key KeyFunc) Option {
	return func(r *Receiver) error {
		r.dedupe = store
		if ttl > 0 {
			r.dedupeTTL = ttl
		}
		if key != nil {
			r.key = key
		}
		return nil
	}
}

// WithPublisher sets where deliveries are published.
func WithPublisher(p api.EventPublisher) Option {
	return func(r *Receiver) error { r.pub = p; return nil }
}

// WithTopic sets the topic of a kind; it must pass the bus grammar.
func WithTopic(kind string, t bus.Topic) Option {
	return func(r *Receiver) error {
		if err := bus.ValidateTopic(t); err != nil {
			return fmt.Errorf("webhook: topic for %q: %w", kind, err)
		}
		r.topics[kind] = t
		return nil
	}
}

// WithTopicPrefix publishes notifications on <prefix>.received and
// lifecycle notices on <lifecyclePrefix>.received (3-segment prefixes).
func WithTopicPrefix(prefix, lifecyclePrefix string) Option {
	return func(r *Receiver) error {
		for kind, p := range map[string]string{KindNotification: prefix, KindLifecycle: lifecyclePrefix} {
			if p == "" {
				continue
			}
			tm, err := bus.PrefixTopics(p, []string{"received"})
			if err != nil {
				return fmt.Errorf("webhook: topic prefix %q: %w", p, err)
			}
			r.topics[kind] = tm["received"]
		}
		return nil
	}
}

// WithClassifier names the kind of a delivery, which picks its topic.
func WithClassifier(fn func(body []byte) string) Option {
	return func(r *Receiver) error { r.classify = fn; return nil }
}

// WithMaxBody bounds the delivery body; zero or less keeps the default.
func WithMaxBody(n int64) Option {
	return func(r *Receiver) error {
		if n > 0 {
			r.maxBody = n
		}
		return nil
	}
}

// WithAckBudget sets how long the sender waits for the answer at most.
func WithAckBudget(d time.Duration) Option {
	return func(r *Receiver) error {
		if d > 0 {
			r.ackBudget = d
		}
		return nil
	}
}

// WithClock sets the clock; for tests.
func WithClock(now func() time.Time) Option {
	return func(r *Receiver) error { r.now = now; return nil }
}

// OnRefused is called with every refusal's code and reason.
func OnRefused(fn func(r *http.Request, code string, err error)) Option {
	return func(r *Receiver) error { r.onRefused = fn; return nil }
}

// OnError is called with a publish or store failure that no longer has
// a response to ride on.
func OnError(fn func(err error)) Option {
	return func(r *Receiver) error { r.onError = fn; return nil }
}

// BodyHashKey keys a delivery on the SHA-256 of its body: an exact
// resend is a repeat.
func BodyHashKey(_ *http.Request, body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (rc *Receiver) refuse(w http.ResponseWriter, r *http.Request, status int, code, msg string, err error) {
	api.RecordRefusal(r, code)
	if rc.onRefused != nil {
		rc.onRefused(r, code, err)
	}
	api.Error(w, status, &api.APIError{Status: status, Code: code, Message: msg})
}

// ServeHTTP implements http.Handler.
func (rc *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Graph validates with GET or POST; everything else is a POST.
	if r.Method != http.MethodPost && r.URL.Query().Get("validationToken") == "" {
		w.Header().Set("Allow", http.MethodPost)
		api.Error(w, http.StatusMethodNotAllowed, &api.APIError{
			Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "webhooks are POSTed",
		})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, rc.maxBody))
	if err != nil {
		if limit, over := api.AsBodyTooLarge(err); over {
			api.WriteBodyTooLarge(w, limit)
			return
		}
		api.Error(w, http.StatusBadRequest, &api.APIError{Status: http.StatusBadRequest, Code: "bad_request", Message: "unreadable body"})
		return
	}

	for _, h := range rc.handshakes {
		if h.Handle(w, r, body) {
			return
		}
	}
	for _, v := range rc.verifiers {
		if err := v.Verify(r, body); err != nil {
			rc.refuse(w, r, http.StatusUnauthorized, CodeUnverified, "delivery not verified", err)
			return
		}
	}
	if rc.window > 0 {
		ts, ok := rc.timestamp(r, body)
		d := rc.now().Sub(ts)
		if !ok || d > rc.window || -d > rc.window {
			rc.refuse(w, r, http.StatusUnauthorized, CodeStale, "delivery outside the replay window",
				fmt.Errorf("%w: timestamp missing or outside %s", ErrUnverified, rc.window))
			return
		}
	}

	key := rc.key(r, body)
	if key == "" {
		key = BodyHashKey(r, body)
	}
	claimed := ""
	if rc.dedupe != nil {
		claimed = rc.claimKey(r, key, body)
		ok, err := rc.dedupe.PutIfAbsentWithTTL(r.Context(), claimed, []byte("1"), rc.dedupeTTL)
		if err != nil {
			api.Error(w, http.StatusServiceUnavailable, &api.APIError{
				Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "try again",
			})
			if rc.onError != nil {
				rc.onError(err)
			}
			return
		}
		if !ok {
			api.JSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
			return
		}
	}

	kind := rc.classify(body)
	topic, ok := rc.topics[kind]
	if !ok {
		topic = rc.topics[KindNotification]
	}
	payload := rc.payload(r, kind, key, body)

	// Publish detached from the request, and answer when it finishes or
	// the ack budget runs out, whichever is first. A failed publish
	// releases the claim, so the sender's retry is not taken for a repeat.
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err := rc.pub.Publish(ctx, string(topic), Source, payload)
		if err != nil {
			if claimed != "" {
				_ = rc.dedupe.Delete(ctx, claimed)
			}
			if rc.onError != nil {
				rc.onError(err)
			}
		}
		done <- err
	}()
	timer := time.NewTimer(rc.ackBudget)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			api.Error(w, http.StatusServiceUnavailable, &api.APIError{
				Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "try again",
			})
			return
		}
	case <-timer.C:
	}
	api.JSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

// claimKey is the kv key a delivery is claimed under: a hash, so no
// receiver name and key can join to the same string. A key that does not
// come from the body (a header) is not trusted: the claim is the body's
// hash and the signed timestamp, so only an identical delivery repeats.
func (rc *Receiver) claimKey(r *http.Request, key string, body []byte) string {
	material := key
	if !rc.keyInBody {
		// The header's value is not authenticated: it is published, but
		// the claim is the content alone.
		material = BodyHashKey(r, body)
		if rc.sigTS != nil {
			if t, ok := rc.sigTS(r, body); ok {
				material += "\x00" + strconv.FormatInt(t.Unix(), 10)
			}
		}
	}
	sum := sha256.Sum256([]byte(material))
	return "webhook/" + rc.name + "/" + hex.EncodeToString(sum[:])
}

func (rc *Receiver) payload(r *http.Request, kind, key string, body []byte) map[string]any {
	p := map[string]any{
		"receiver":     rc.name,
		"key":          key,
		"kind":         kind,
		"received_at":  rc.now().UTC().Format(time.RFC3339Nano),
		"content_type": r.Header.Get("Content-Type"),
	}
	var js json.RawMessage
	if utf8.Valid(body) && json.Unmarshal(body, &js) == nil {
		p["encoding"], p["body"] = "json", js
	} else {
		p["encoding"], p["body"] = "base64", base64.StdEncoding.EncodeToString(body)
	}
	return p
}
