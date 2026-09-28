package cmdsurface

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// This file turns the per-command webhook:, bus: and cron: blocks
// into the inputs MountWebhooks, MountBus and MountCron take, so a
// block declared in YAML reaches its mount in one call:
//
//	cfg, _ := cmdsurface.LoadFile(path)
//	b, _ := cmdsurface.FromConfig(root, cfg)
//	hooks, err := cfg.WebhookMappings(os.LookupEnv)
//	...
//	err = cmdsurface.MountWebhooks(b, r, hooks)
//
// FromConfig itself still mounts nothing: a surface needs things only
// the caller has (a router, a Subscriber, a CronEngine, secrets).
//
// Each translator walks the command patterns in sorted order, so its
// result is deterministic. A block binds one command, so it must sit
// under an exact path, never a wildcard. A block with every key empty
// is no block; a block with its required key empty and another key set
// is refused, rather than silently doing nothing. What the mount
// itself checks (the command exists, the surface is enabled on it, the
// destructive ceiling, auth and confirmation rules) is left to the
// mount.

// WebhookMappings returns one WebhookMapping per webhook: block.
//
// lookupEnv resolves secret_env and token_env; pass os.LookupEnv, or a
// lookup over your own secret store. A variable that is unset or empty
// is an error here rather than a 401 on every request later. lookupEnv
// may be nil when no block needs a secret.
//
// auth selects the scheme: "none" (or empty), "hmac" (header and
// secret_env required, prefix optional) or "bearer" (token_env
// required). A key that belongs to another scheme is an error.
func (c Config) WebhookMappings(lookupEnv func(string) (string, bool)) ([]WebhookMapping, error) {
	var out []WebhookMapping
	for _, pattern := range sortedPatterns(c) {
		w := c.Surfaces.Commands[pattern].Webhook
		if webhookBlockEmpty(w) {
			continue
		}
		path, err := blockPath(pattern, "webhook")
		if err != nil {
			return nil, err
		}
		if w.Name == "" {
			return nil, fmt.Errorf("cmdsurface: config %q: webhook: name is required", pattern)
		}
		auth, err := webhookAuth(w, lookupEnv)
		if err != nil {
			return nil, fmt.Errorf("cmdsurface: config %q: webhook: %w", pattern, err)
		}
		out = append(out, WebhookMapping{
			Name:         w.Name,
			Path:         path,
			FlagMap:      maps.Clone(w.Map),
			ArgsTemplate: w.ArgsTemplate,
			Auth:         auth,
		})
	}
	return out, nil
}

// BusBindings returns one BusBinding per bus: block. request_topic is
// required once any key of the block is set.
func (c Config) BusBindings() ([]BusBinding, error) {
	var out []BusBinding
	for _, pattern := range sortedPatterns(c) {
		bc := c.Surfaces.Commands[pattern].Bus
		if bc == (BusConfig{}) {
			continue
		}
		path, err := blockPath(pattern, "bus")
		if err != nil {
			return nil, err
		}
		if bc.RequestTopic == "" {
			return nil, fmt.Errorf("cmdsurface: config %q: bus: request_topic is required", pattern)
		}
		out = append(out, BusBinding{
			Path:          path,
			RequestTopic:  bc.RequestTopic,
			ResponseTopic: bc.ResponseTopic,
			GroupID:       bc.GroupID,
		})
	}
	return out, nil
}

// CronSchedules returns one CronSchedule per cron: block. expr is
// required once any key of the block is set; the expression and the
// timezone are validated by MountCron's engine.
func (c Config) CronSchedules() ([]CronSchedule, error) {
	var out []CronSchedule
	for _, pattern := range sortedPatterns(c) {
		cc := c.Surfaces.Commands[pattern].Cron
		if cc.Expr == "" && cc.Timezone == "" && len(cc.Args) == 0 && len(cc.Flags) == 0 {
			continue
		}
		path, err := blockPath(pattern, "cron")
		if err != nil {
			return nil, err
		}
		if cc.Expr == "" {
			return nil, fmt.Errorf("cmdsurface: config %q: cron: expr is required", pattern)
		}
		out = append(out, CronSchedule{
			Path:     path,
			Expr:     cc.Expr,
			Timezone: cc.Timezone,
			Args:     slices.Clone(cc.Args),
			Flags:    maps.Clone(cc.Flags),
		})
	}
	return out, nil
}

// sortedPatterns returns the command patterns in c in sorted order.
func sortedPatterns(c Config) []string {
	return slices.Sorted(maps.Keys(c.Surfaces.Commands))
}

// blockPath returns the command path an exact pattern names, and
// refuses a wildcard: webhook, bus and cron blocks bind one command.
func blockPath(pattern, block string) ([]string, error) {
	path := strings.Fields(pattern)
	if len(path) == 0 || slices.Contains(path, "*") {
		return nil, fmt.Errorf(
			"cmdsurface: config %q: %s: a %s block binds exactly one command; put it under that command's full path",
			pattern, block, block,
		)
	}
	return path, nil
}

func webhookBlockEmpty(w WebhookConfig) bool {
	return w.Name == "" && len(w.Map) == 0 && w.ArgsTemplate == "" && w.Auth == "" &&
		w.Header == "" && w.Prefix == "" && w.SecretEnv == "" && w.TokenEnv == ""
}

// webhookAuth builds the WebhookAuth a block's auth keys select.
func webhookAuth(w WebhookConfig, lookupEnv func(string) (string, bool)) (WebhookAuth, error) {
	scheme := strings.ToLower(strings.TrimSpace(w.Auth))
	if scheme != "hmac" {
		for _, kv := range [][2]string{{"header", w.Header}, {"prefix", w.Prefix}, {"secret_env", w.SecretEnv}} {
			if kv[1] != "" {
				return nil, fmt.Errorf("%s applies to hmac, and auth is %q", kv[0], w.Auth)
			}
		}
	}
	if scheme != "bearer" && w.TokenEnv != "" {
		return nil, fmt.Errorf("token_env applies to bearer, and auth is %q", w.Auth)
	}
	switch scheme {
	case "", "none":
		return AuthNone{}, nil
	case "hmac":
		if w.Header == "" {
			return nil, errors.New("hmac needs header, the request header carrying the signature")
		}
		if w.SecretEnv == "" {
			return nil, errors.New("hmac needs secret_env, the variable holding the shared secret")
		}
		secret, err := secretFrom(lookupEnv, w.SecretEnv)
		if err != nil {
			return nil, err
		}
		return AuthHMAC{Header: w.Header, Prefix: w.Prefix, Secret: []byte(secret)}, nil
	case "bearer":
		if w.TokenEnv == "" {
			return nil, errors.New("bearer needs token_env, the variable holding the token")
		}
		token, err := secretFrom(lookupEnv, w.TokenEnv)
		if err != nil {
			return nil, err
		}
		return AuthBearer{Token: token}, nil
	default:
		return nil, fmt.Errorf("unknown auth %q; use none, hmac or bearer", w.Auth)
	}
}

// secretFrom resolves one secret variable, refusing an unset or empty
// value.
func secretFrom(lookupEnv func(string) (string, bool), name string) (string, error) {
	if lookupEnv == nil {
		return "", fmt.Errorf("%s: no lookupEnv to resolve it with; pass os.LookupEnv", name)
	}
	v, ok := lookupEnv(name)
	if !ok || v == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	return v, nil
}
