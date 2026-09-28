package svcconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvKey(t *testing.T) {
	services := []string{"api", "socket", "mcp", "rpc", "my", "my-svc"} // "my" first: the longest match wins, not the first
	cases := map[string]string{
		"TOOL_SERVICES_API_ADDR":                         "services.api.addr",
		"TOOL_SERVICES_API_INSECURE_REMOTE":              "services.api.insecure_remote",
		"TOOL_SERVICES_API_READY_TIMEOUT":                "services.api.ready_timeout",
		"TOOL_SERVICES_SOCKET_ENABLED":                   "services.socket.enabled",
		"TOOL_SERVICES_API_BODY_LIMIT_MAX_BYTES":         "services.api.body_limit.max_bytes",
		"TOOL_SERVICES_API_BODY_LIMIT_MAXBYTES":          "services.api.body_limit.maxbytes",
		"TOOL_SERVICES_ALL_HEALTH_PATH_PREFIX":           "services.all.health.path_prefix",
		"TOOL_SERVICES_ALL_AUDIT_REDACT_SECRET_FLAGS":    "services.all.audit.redact.secret_flags",
		"TOOL_SERVICES_API_AUDIT_REDACT_MAX_FIELD_BYTES": "services.api.audit.redact.max_field_bytes",
		"TOOL_SERVICES_API_AUDIT_SINKS":                  "services.api.audit.sinks",
		"TOOL_SERVICES_API_METRICS_ENDPOINT":             "services.api.metrics.endpoint",
		"TOOL_SERVICES_API_METRICS_SCRAPE_ENABLED":       "services.api.metrics.scrape.enabled",
		"TOOL_SERVICES_ALL_METRICS_SCRAPE_ALLOW_REMOTE":  "services.all.metrics.scrape.allow_remote",
		"TOOL_SERVICES_API_ORIGIN_CHECK":                 "services.api.origin_check",
		"TOOL_SERVICES_ALL_ADDR":                         "services.all.addr",
		"TOOL_SERVICES_API":                              "services.api",
		"TOOL_SERVICES_MY_SVC_METRICS_INTERVAL":          "services.my-svc.metrics.interval",
		"TOOL_SERVICES_MY_READY_TIMEOUT":                 "services.my.ready_timeout",
		"TOOL_SERVICES_MCP_TRANSPORT":                    "services.mcp.transport",
		"TOOL_SERVICES_MCP_INSECURE_NO_POLICY":           "services.mcp.insecure_no_policy",
		"TOOL_SERVICES_RPC_ADDR":                         "services.rpc.addr",
		"TOOL_SERVICES_RPC_BODY_LIMIT_MAX_BYTES":         "services.rpc.body_limit.max_bytes",
		"TOOL_SERVICES_FAILURE_POLICY":                   "services.failure_policy",
		"TOOL_SERVICES_SHUTDOWN_TIMEOUT":                 "services.shutdown_timeout",
	}
	for name, want := range cases {
		got, ok := EnvKey(name, "tool", services)
		if assert.True(t, ok, name) {
			assert.Equal(t, want, got, name)
		}
	}
	for _, name := range []string{"TOOL_ADDR", "OTHER_SERVICES_API_ADDR", "TOOL_SERVICES_", "TOOLS_SERVICES_API_ADDR"} {
		_, ok := EnvKey(name, "tool", services)
		assert.False(t, ok, name)
	}
	got, ok := EnvKey("MY_TOOL_SERVICES_API_ADDR", "my-tool", services)
	assert.True(t, ok)
	assert.Equal(t, "services.api.addr", got, "a dash in the tool name is an underscore")
}
