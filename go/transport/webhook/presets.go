package webhook

import (
	"encoding/json"
	"time"
)

// GraphConfig configures [Graph].
type GraphConfig struct {
	// ClientState is the secret set on the subscription.
	ClientState string
}

// Graph returns the options for a Microsoft Graph change-notification
// endpoint: the validationToken handshake, clientState verification,
// and lifecycle notices (value[].lifecycleEvent) routed to their own
// topic. Dedupe, publisher and the rest are the caller's.
func Graph(cfg GraphConfig) []Option {
	return []Option{
		WithHandshake(GraphValidationToken()),
		WithVerifier(ClientState(cfg.ClientState)),
		WithClassifier(func(body []byte) string {
			var doc struct {
				Value []struct {
					LifecycleEvent string `json:"lifecycleEvent"`
				} `json:"value"`
			}
			if json.Unmarshal(body, &doc) == nil {
				for _, n := range doc.Value {
					if n.LifecycleEvent != "" {
						return KindLifecycle
					}
				}
			}
			return KindNotification
		}),
	}
}

// PipedriveConfig configures [Pipedrive].
type PipedriveConfig struct {
	// User and Password are the webhook's HTTP basic credentials.
	User, Password string
	// ReplayWindow, when positive, refuses events whose meta.timestamp
	// is further than this from now.
	ReplayWindow time.Duration
}

// Pipedrive returns the options for a Pipedrive v2 webhook: HTTP basic
// auth, meta.id as the idempotency key (the body hash if absent), and
// meta.timestamp for the replay window.
func Pipedrive(cfg PipedriveConfig) []Option {
	opts := []Option{
		WithVerifier(BasicAuth(cfg.User, cfg.Password)),
		func(r *Receiver) error { r.key = JSONKey("/meta/id"); return nil },
	}
	if cfg.ReplayWindow > 0 {
		opts = append(opts, WithReplayWindow(cfg.ReplayWindow, JSONTimestamp("/meta/timestamp")))
	}
	return opts
}

// GenericHMACConfig configures [GenericHMAC].
type GenericHMACConfig struct {
	HMAC HMACConfig
	// ReplayWindow, when positive and HMAC.TimestampHeader is set,
	// refuses deliveries whose signed timestamp is too far from now.
	ReplayWindow time.Duration
	// KeyHeader, when set, names the idempotency key header.
	KeyHeader string
}

// GenericHMAC returns the options for a plain HMAC-signed webhook.
func GenericHMAC(cfg GenericHMACConfig) ([]Option, error) {
	v, err := HMAC(cfg.HMAC)
	if err != nil {
		return nil, err
	}
	opts := []Option{WithVerifier(v)}
	if cfg.KeyHeader != "" {
		opts = append(opts, func(r *Receiver) error { r.key = HeaderKey(cfg.KeyHeader); return nil })
	}
	if cfg.ReplayWindow > 0 && cfg.HMAC.TimestampHeader != "" {
		opts = append(opts, WithReplayWindow(cfg.ReplayWindow, HeaderUnixTimestamp(cfg.HMAC.TimestampHeader)))
	}
	return opts, nil
}
