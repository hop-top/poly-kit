package svcconfig

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupSpecificityBeforeSource(t *testing.T) {
	t.Setenv("TOOL_SERVICES_ALL_BODY_LIMIT_MAX_BYTES", "2048")
	v := viper.New()
	require.NoError(t, v.BindEnv("services.all.body_limit.max_bytes", "TOOL_SERVICES_ALL_BODY_LIMIT_MAX_BYTES"))
	r := New(v)

	val, from, ok := r.Lookup("api", "body_limit", "max_bytes")
	require.True(t, ok, "services.all applies when the service sets nothing")
	assert.Equal(t, "2048", val)
	assert.Equal(t, "services.all.body_limit.max_bytes", from)

	// The service's key from the config file beats the shared key from
	// the environment, a higher-precedence source.
	require.NoError(t, v.MergeConfigMap(map[string]any{
		"services": map[string]any{"api": map[string]any{"body_limit": map[string]any{"max_bytes": 1024}}},
	}))
	val, from, ok = r.Lookup("api", "body_limit", "max_bytes")
	require.True(t, ok)
	assert.Equal(t, 1024, val)
	assert.Equal(t, "services.api.body_limit.max_bytes", from)

	// Keys resolve one by one: the service's max_bytes does not hide
	// the shared enabled.
	v.Set("services.all.body_limit.enabled", false)
	val, from, ok = r.Lookup("api", "body_limit", "enabled")
	require.True(t, ok)
	assert.Equal(t, false, val)
	assert.Equal(t, "services.all.body_limit.enabled", from)

	_, _, ok = r.Lookup("api", "compression", "enabled")
	assert.False(t, ok, "unset everywhere: the caller's default applies")
	_, _, ok = New(nil).Lookup("api", "body_limit", "enabled")
	assert.False(t, ok)
}

func TestLookupListsReplace(t *testing.T) {
	v := viper.New()
	v.Set("services.all.host_check.allow", []string{"a.example", "b.example"})
	v.Set("services.api.host_check.allow", []string{"c.example"})
	val, _, ok := New(v).Lookup("api", "host_check", "allow")
	require.True(t, ok)
	assert.Equal(t, []string{"c.example"}, val, "the service's list replaces the shared one")
}

func TestIsConfiguredSeesEnvironmentOnlyBlocks(t *testing.T) {
	t.Setenv("TOOL_SERVICES_SOCKET_ENABLED", "true")
	v := viper.New()
	r := New(v)
	assert.False(t, r.IsConfigured("socket"))
	require.NoError(t, v.BindEnv("services.socket.enabled", "TOOL_SERVICES_SOCKET_ENABLED"))
	assert.False(t, v.IsSet("services.socket"), "viper alone misses it")
	assert.True(t, r.IsConfigured("socket"))
	assert.False(t, r.IsConfigured("api"))
}

func TestValidateRefusesUnknownKeys(t *testing.T) {
	cases := map[string]struct {
		key  string
		val  any
		want string
	}{
		"unknown key in a service block": {"services.api.body_limit.max_byte", 1, `services.api.body_limit.max_byte: unknown key "max_byte"; body_limit accepts enabled, max_bytes`},
		"unknown key in services.all":    {"services.all.health.prefix", "/x", `services.all.health.prefix: unknown key "prefix"`},
		"adopter service block":          {"services.heartbeat.compression.level", 9, `services.heartbeat.compression.level: unknown key "level"`},
		"nested block":                   {"services.api.audit.redact.enabled", false, `unknown key "enabled"; audit.redact accepts secret_flags, patterns, max_field_bytes; secret-flag redaction cannot be switched off`},
		"scalar block":                   {"services.api.origin_check", true, "services.api.origin_check: must be a block with keys enabled, allow"},
		"nested scrape block":            {"services.all.metrics.scrape.allow_remot", true, `services.all.metrics.scrape.allow_remot: unknown key "allow_remot"; metrics.scrape accepts enabled, path, allow_remote`},
		"scalar scrape block":            {"services.api.metrics.scrape", true, "services.api.metrics.scrape: must be a block with keys enabled, path, allow_remote"},
		"lifecycle key under all":        {"services.all.enabled", true, "services.all.enabled: not a middleware key"},
		"service key under all":          {"services.all.addr", "0.0.0.0:1", "services.all.addr: not a middleware key"},
		"unknown block under all":        {"services.all.ratelimit.enabled", true, "services.all.ratelimit.enabled: not a middleware key"},
		"unknown rate_limit tier":        {"services.api.rate_limit.reads.burst", 1, `services.api.rate_limit.reads: unknown key "reads"; rate_limit accepts enabled; the per-tier limits are the read, write and destructive blocks`},
		"unknown key in a tier":          {"services.all.rate_limit.write.brust", 1, `services.all.rate_limit.write.brust: unknown key "brust"; rate_limit.write accepts per_minute, burst`},
		"scalar tier":                    {"services.api.rate_limit.read", 5, "services.api.rate_limit.read: must be a block with keys per_minute, burst"},
		"scalar services.all":            {"services.all", "x", "services.all: must be a block"},
		"unknown key in audit":           {"services.api.audit.sink", []string{"chain"}, `services.api.audit.sink: unknown key "sink"; audit accepts sinks`},
		"scalar audit block":             {"services.all.audit", "chain", "services.all.audit: must be a block with keys sinks"},
		"sinks as a map":                 {"services.api.audit.sinks", map[string]any{"type": "chain"}, "services.api.audit.sinks: must be a list of entries, each a type or a map with keys fsync, max_bytes, max_files, on, path, paths, surfaces, type"},
		"sinks as a number":              {"services.api.audit.sinks", 3, "services.api.audit.sinks: must be a list of entries"},
		"sink entry not a map":           {"services.all.audit.sinks", []any{"chain", 42}, "services.all.audit.sinks[1]: want a type or a map with keys fsync, "},
		"unknown key in a sink entry":    {"services.api.audit.sinks", []any{map[string]any{"type": "chain", "rotate": true, "fsync": "always"}}, `services.api.audit.sinks[0]: unknown key rotate; an entry accepts fsync, max_bytes`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			v := viper.New()
			v.Set(c.key, c.val)
			err := New(v).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

func TestValidateAcceptsKnownKeys(t *testing.T) {
	v := viper.New()
	for k, val := range map[string]any{
		"services.failure_policy":                        "isolate",
		"services.api.addr":                              "127.0.0.1:0",
		"services.api.enabled":                           true,
		"services.api.body_limit.max_bytes":              10,
		"services.all.rate_limit.enabled":                true,
		"services.api.rate_limit.read.burst":             5,
		"services.mcp.rate_limit.destructive.per_minute": 1,
		"services.api.audit.redact.patterns":             []string{"x"},
		"services.all.tracing.enabled":                   true,
		"services.api.metrics.scrape.enabled":            true,
		"services.all.metrics.endpoint":                  "http://127.0.0.1:4318",
		"services.all.host_check.allow":                  []string{"a"},
		"services.heartbeat.interval":                    "1s",
		"services.heartbeat.health.path_prefix":          "/h",
		"services.all.audit.redact.secret_flags":         []string{"dsn"},
		"services.all.audit.sinks":                       []string{"chain"},
		"services.api.audit.sinks": []any{"chain", map[string]any{
			"type": "chain", "path": "/x", "fsync": "1s", "max_bytes": 1, "max_files": 2,
			"on": []any{"error"}, "surfaces": []any{"rest"}, "paths": []any{"a *"},
		}},
		"services.socket.audit.sinks": "chain, chain",
	} {
		v.Set(k, val)
	}
	assert.NoError(t, New(v).Validate())
	assert.NoError(t, New(nil).Validate())
}

func TestValidateSeesEveryLayer(t *testing.T) {
	// viper returns one layer's map for a block, so a check that
	// reads the block as a map misses a key from another layer.
	t.Setenv("TOOL_SERVICES_API_BODY_LIMIT_BOGUS", "1")
	v := viper.New()
	v.Set("services.api.body_limit.max_bytes", 10)
	require.NoError(t, v.BindEnv("services.api.body_limit.bogus", "TOOL_SERVICES_API_BODY_LIMIT_BOGUS"))
	err := New(v).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "services.api.body_limit.bogus")

	require.NoError(t, v.MergeConfigMap(map[string]any{
		"services": map[string]any{"all": map[string]any{"compression": map[string]any{"level": 3}}},
	}))
	err = New(v).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "services.all.compression.level")
}

func TestValidateBlockScopes(t *testing.T) {
	v := viper.New()
	v.Set("services.socket.body_limit.bogus", 1)
	v.Set("services.api.health.bogus", 1)
	r := New(v)
	assert.NoError(t, r.ValidateBlock("body_limit", "api", Shared), "another service's block is not in scope")
	assert.Error(t, r.ValidateBlock("body_limit", "socket", Shared))
	assert.Error(t, r.ValidateBlock("body_limit"), "no scope named: every service")
	assert.NoError(t, r.ValidateBlock("compression"))
	assert.Error(t, r.ValidateBlock("no_such_block"))
}
