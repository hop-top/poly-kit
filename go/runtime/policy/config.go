package policy

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ContextAttrsKey is the context.Context key the host tool uses to
// attach per-request attributes (e.g. CLI flag values like --note).
// Engine reads it when building the 'context' CEL binding.
//
// Usage in host tool, BEFORE calling a kit mutator:
//
//	ctx = context.WithValue(ctx, policy.ContextAttrsKey, map[string]any{
//	    "note": cliFlags.Note,
//	    "request_attrs": map[string]any{...},
//	})
type ctxKey struct{ name string }

// ContextAttrsKey carries the host-supplied 'context' map for CEL.
var ContextAttrsKey = &ctxKey{name: "policy.context.attrs"}

// ContextPrincipalKey carries an explicit Principal for the request.
// Hosts that compute principal from their own auth layer set this;
// the default resolver picks it up.
var ContextPrincipalKey = &ctxKey{name: "policy.context.principal"}

// Allowed topics for the 'on:' field. Mirrors §3 of ADR 0008.
//
// kit/runtime/bus.Validate enforces 4-segment topic shape on
// publishers but doesn't enumerate which topics are veto-able. Until
// that surface lands, hard-code here. To extend: add the topic AND
// wire a corresponding subscriber call in Wire().
var allowedTopics = map[string]struct{}{
	"kit.runtime.state.pre_transitioned": {},
	"kit.runtime.entity.pre_validated":   {},
	"kit.runtime.entity.pre_persisted":   {},
}

// Config is the parsed YAML schema.
type Config struct {
	Policies []Policy
}

// rawConfig mirrors the YAML on disk. Internal — not exported.
type rawConfig struct {
	Policies []rawPolicy `yaml:"policies"`
}

type rawPolicy struct {
	Name      string `yaml:"name"`
	On        string `yaml:"on"`
	When      string `yaml:"when"`
	Effect    string `yaml:"effect"`
	Otherwise string `yaml:"otherwise"`
	Message   string `yaml:"message"`
	Async     bool   `yaml:"async"` // rejected at parse — see §7
}

// LoadConfig reads and validates a policy YAML file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("policy: read %s: %w", path, err)
	}
	return ParseConfig(b)
}

// ParseConfig parses raw YAML bytes.
func ParseConfig(data []byte) (*Config, error) {
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("policy: parse yaml: %w", err)
	}
	cfg := &Config{Policies: make([]Policy, 0, len(raw.Policies))}
	seen := map[string]struct{}{}
	for i, rp := range raw.Policies {
		if rp.Name == "" {
			return nil, fmt.Errorf("policy: index %d: name required", i)
		}
		if _, dup := seen[rp.Name]; dup {
			return nil, fmt.Errorf("policy %q: duplicate name", rp.Name)
		}
		seen[rp.Name] = struct{}{}
		if rp.Async {
			return nil, fmt.Errorf("policy %q: async not supported (sync veto only — ADR 0008 §7)", rp.Name)
		}
		if _, ok := allowedTopics[rp.On]; !ok {
			return nil, fmt.Errorf("policy %q: 'on' %q not a veto-able topic (allowed: kit.runtime.state.pre_transitioned, kit.runtime.entity.pre_validated, kit.runtime.entity.pre_persisted)", rp.Name, rp.On)
		}
		if rp.When == "" {
			return nil, fmt.Errorf("policy %q: 'when' required", rp.Name)
		}
		eff, err := parseEffect(rp.Effect, "effect", rp.Name)
		if err != nil {
			return nil, err
		}
		oth, err := parseEffect(rp.Otherwise, "otherwise", rp.Name)
		if err != nil {
			return nil, err
		}
		cfg.Policies = append(cfg.Policies, Policy{
			Name:      rp.Name,
			On:        rp.On,
			When:      rp.When,
			Effect:    eff,
			Otherwise: oth,
			Message:   rp.Message,
		})
	}
	return cfg, nil
}

func parseEffect(s, field, name string) (Effect, error) {
	switch s {
	case "allow":
		return EffectAllow, nil
	case "deny":
		return EffectDeny, nil
	case "":
		return "", fmt.Errorf("policy %q: %s required (allow|deny)", name, field)
	default:
		return "", fmt.Errorf("policy %q: %s %q invalid (want allow|deny)", name, field, s)
	}
}

// DefaultPrincipalResolver picks principal from ctx → KIT_POLICY_ROLE
// env → empty. ID falls back to $USER then empty. Source records the
// resolution path.
//
// Aps profile lookup is out of scope here — kit cannot import aps
// (layering). Hosts that integrate with aps install their own
// resolver via WithPrincipalResolver.
func DefaultPrincipalResolver(ctx context.Context) Principal {
	if v := ctx.Value(ContextPrincipalKey); v != nil {
		if p, ok := v.(Principal); ok {
			if p.Source == "" {
				p.Source = "ctx"
			}
			return p
		}
	}
	role := os.Getenv("KIT_POLICY_ROLE")
	id := os.Getenv("USER")
	source := "none"
	switch {
	case role != "":
		source = "env"
	case id != "":
		source = "env"
	}
	return Principal{ID: id, Role: role, Source: source}
}
