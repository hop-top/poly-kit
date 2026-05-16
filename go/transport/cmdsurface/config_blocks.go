package cmdsurface

// This file defines the typed YAML shapes for the per-command
// surface blocks documented in the cmdsurface spec
// (.tlc/tracks/cmdsurf/spec.md). They are loaded by the same
// Load / LoadFile entry points used for the rest of Config and
// surface here so adopters can express webhook mappings, bus
// bindings, cron schedules, and outbound sink fan-out in YAML
// without resorting to free-form `any` maps.
//
// The runtime surfaces (surface_webhook.go, surface_bus.go,
// surface_cron.go, sink_*.go) take their bindings as Go structs
// via Mount* / SinkSet at call time. These YAML structs are the
// declarative counterpart: a caller building Mount* inputs from
// YAML reads CommandConfig.Webhook / Bus / Cron / Sinks and
// translates each entry to the corresponding runtime struct.

// WebhookConfig is the per-command "webhook:" block. It maps an
// inbound HTTP webhook onto the leaf:
//
//	webhook:
//	  name: widget-create
//	  map:
//	    name: "{{ .body.title }}"
//	  args: "{{ .body.tags }}"
//	  auth: hmac
//	  header: X-Hub-Signature-256
//	  prefix: "sha256="
//	  secret_env: WIDGET_HOOK_SECRET
//
// Name is the URL slug under the bridge's webhook prefix; Map and
// ArgsTemplate are text/template sources executed against the
// {body, headers, query, path} root the surface assembles per
// request (see WebhookMapping); Auth selects the verification
// scheme, with Header/Prefix/SecretEnv/TokenEnv carrying the
// scheme-specific knobs.
type WebhookConfig struct {
	// Name is the URL slug ("/hooks/{Name}"). Required.
	Name string `yaml:"name"`
	// Map maps flag name → text/template source. Renders to "" omit
	// the flag from the invocation.
	Map map[string]string `yaml:"map,omitempty"`
	// ArgsTemplate is a text/template whose rendered value is split
	// on whitespace and used as positional args. Optional.
	ArgsTemplate string `yaml:"args,omitempty"`
	// Auth names the WebhookAuth scheme: "none", "hmac", or "bearer".
	// Empty defaults to "none" (the surface refuses AuthNone on
	// auth-required leaves at mount time).
	Auth string `yaml:"auth,omitempty"`
	// Header is the request header carrying the credential. Used by
	// "hmac" (e.g. "X-Hub-Signature-256"); ignored by "bearer".
	Header string `yaml:"header,omitempty"`
	// Prefix is stripped from the header value before decoding (e.g.
	// "sha256="). HMAC only.
	Prefix string `yaml:"prefix,omitempty"`
	// SecretEnv is the environment variable holding the HMAC shared
	// secret. The surface adapter loads this at mount time.
	SecretEnv string `yaml:"secret_env,omitempty"`
	// TokenEnv is the environment variable holding the bearer token.
	TokenEnv string `yaml:"token_env,omitempty"`
}

// BusConfig is the per-command "bus:" block:
//
//	bus:
//	  request_topic:  widgets.create.req
//	  response_topic: widgets.create.resp
//	  group_id:       cmdsurface-widgets
//
// RequestTopic is the topic the surface subscribes to on behalf
// of the leaf; ResponseTopic is where Results are published (an
// empty value means fire-and-forget); GroupID is forwarded to the
// adopter's Subscriber as an opaque consumer-group label.
type BusConfig struct {
	// RequestTopic is the topic the surface subscribes to. Required
	// for the block to take effect; an empty value disables the
	// binding.
	RequestTopic string `yaml:"request_topic,omitempty"`
	// ResponseTopic is the topic Results are published to. Empty →
	// fire-and-forget.
	ResponseTopic string `yaml:"response_topic,omitempty"`
	// GroupID is the consumer-group label adopters forward to their
	// broker at Subscribe time.
	GroupID string `yaml:"group_id,omitempty"`
}

// CronConfig is the per-command "cron:" block:
//
//	cron:
//	  expr:     "0 9 * * *"
//	  timezone: America/New_York
//	  args:     [ "--dry-run" ]
//	  flags:
//	    limit: 100
//
// Expr is a 5-field cron expression (no seconds); Timezone is an
// IANA zone name (empty or "UTC" → UTC); Args and Flags bake
// positional / flag values into the invocation issued on every
// tick.
type CronConfig struct {
	// Expr is the 5-field cron expression. Required for the block
	// to take effect.
	Expr string `yaml:"expr,omitempty"`
	// Timezone is the IANA zone name. Empty defaults to UTC.
	Timezone string `yaml:"timezone,omitempty"`
	// Args are positional arguments baked into the invocation.
	Args []string `yaml:"args,omitempty"`
	// Flags are flag values baked into the invocation.
	Flags map[string]any `yaml:"flags,omitempty"`
}

// SinkConfig is one entry in the per-command "sinks:" list:
//
//	sinks:
//	  - { type: webhook, url: https://audit/x, on: [success, error] }
//	  - { type: bus, topic: invocations.audit }
//	  - { type: log, level: info }
//	  - { type: file, path: /var/log/kit.jsonl }
//
// Type selects the implementation; On is the OnOK / OnError
// filter expressed as a list of "success" / "error" tokens (an
// empty list defaults to ["success", "error"]); the remaining
// fields are read by the relevant sink type only. Path patterns
// and surface filters use the same dotted / space-separated forms
// SinkSpec.Paths accepts.
type SinkConfig struct {
	// Type is one of "webhook", "bus", "log", "file". Required.
	Type string `yaml:"type"`
	// On is the outcome filter: "success", "error", or both. Empty
	// defaults to both.
	On []string `yaml:"on,omitempty"`
	// Surfaces is the surface allow-set. Empty matches every
	// surface.
	Surfaces []Surface `yaml:"surfaces,omitempty"`
	// Paths is the path pattern allow-set ("widget *",
	// "report.purge", "*"). Empty matches every path.
	Paths []string `yaml:"paths,omitempty"`

	// WebhookSink fields.
	URL     string            `yaml:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`

	// BusSink fields.
	Topic  string `yaml:"topic,omitempty"`
	Source string `yaml:"source,omitempty"`

	// LogSink fields.
	Level string `yaml:"level,omitempty"`

	// FileSink fields.
	Path string `yaml:"path,omitempty"`
}
