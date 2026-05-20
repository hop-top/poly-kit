package cmdsurface

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const fixtureYAML = `
surfaces:
  defaults: [cli, lib]
  commands:
    "widget add":
      enabled: [cli, rest, lib, mcp]
    "widget delete":
      enabled: [cli, lib]
    "report *":
      enabled: [cli, lib, mcp]
policy:
  destructive_default: deny_remote
`

func TestLoad_ParsesFixture(t *testing.T) {
	cfg, err := Load(strings.NewReader(fixtureYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Surfaces.Defaults) != 2 {
		t.Errorf("defaults len=%d want=2", len(cfg.Surfaces.Defaults))
	}
	if cfg.Surfaces.Commands["widget add"].Enabled[1] != SurfaceREST {
		t.Errorf("widget add[1]=%v want=rest", cfg.Surfaces.Commands["widget add"].Enabled[1])
	}
	if cfg.Policy.DestructiveDefault != "deny_remote" {
		t.Errorf("destructive_default=%q", cfg.Policy.DestructiveDefault)
	}
}

func TestLoad_Empty(t *testing.T) {
	cfg, err := Load(strings.NewReader(""))
	if err != nil {
		t.Fatalf("empty Load: %v", err)
	}
	if len(cfg.Surfaces.Defaults) != 0 {
		t.Errorf("empty YAML should yield empty defaults, got %v", cfg.Surfaces.Defaults)
	}
}

func TestLoad_UnknownSurfaceCaughtAtFromConfig(t *testing.T) {
	cfg, err := Load(strings.NewReader(`
surfaces:
  commands:
    "widget add":
      enabled: [cli, graphql]
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := FromConfig(newBridgeTree(), cfg); err == nil {
		t.Fatal("FromConfig should reject unknown surface")
	}
}

func TestFromConfig_PerLeafEnablement(t *testing.T) {
	cfg, err := Load(strings.NewReader(fixtureYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b, err := FromConfig(newBridgeTree(), cfg)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	expect := map[string]map[Surface]bool{
		"widget add":    {SurfaceCLI: true, SurfaceREST: true, SurfaceLib: true, SurfaceMCP: true},
		"widget delete": {SurfaceCLI: true, SurfaceLib: true},
		"report daily":  {SurfaceCLI: true, SurfaceLib: true, SurfaceMCP: true},
		// Not listed: takes the surfaces.defaults set (cli, lib).
		"ping": {SurfaceCLI: true, SurfaceLib: true},
	}
	for _, l := range b.Leaves() {
		want, ok := expect[l.PathKey()]
		if !ok {
			t.Fatalf("unexpected leaf %q", l.PathKey())
		}
		// Each expected surface must be enabled.
		for s := range want {
			if !l.Enabled[s] {
				t.Errorf("leaf %q missing surface %s", l.PathKey(), s)
			}
		}
		// And only those surfaces.
		for s, on := range l.Enabled {
			if on && !want[s] {
				t.Errorf("leaf %q unexpectedly has surface %s enabled", l.PathKey(), s)
			}
		}
	}
}

func TestFromConfig_DestructiveAllow(t *testing.T) {
	cfg, err := Load(strings.NewReader(`
surfaces:
  defaults: [cli, lib]
  commands:
    "widget delete":
      enabled: [cli, lib, rest]
policy:
  destructive_default: allow
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b, err := FromConfig(newBridgeTree(), cfg)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	// destructive_default=allow lifts the policy ceiling, so the
	// destructive leaf is reachable on REST.
	res, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"widget", "delete"},
		Meta: Meta{Surface: SurfaceREST},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode=%d want=0", res.ExitCode)
	}
}

func TestFromConfig_DestructiveDenyRemote_Default(t *testing.T) {
	cfg, err := Load(strings.NewReader(`
surfaces:
  defaults: [cli, lib]
  commands:
    "widget delete":
      enabled: [cli, lib, rest]
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b, err := FromConfig(newBridgeTree(), cfg)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	// Surface IS enabled but the policy still blocks.
	_, err = b.Invoke(context.Background(), Invocation{
		Path: []string{"widget", "delete"},
		Meta: Meta{Surface: SurfaceREST},
	})
	if !errors.Is(err, ErrDestructiveBlocked) {
		t.Fatalf("err=%v want ErrDestructiveBlocked", err)
	}
}

func TestFromConfig_NilRoot(t *testing.T) {
	if _, err := FromConfig(nil, Config{}); err == nil {
		t.Fatal("FromConfig(nil) should error")
	}
}

const fixtureBlocksYAML = `
surfaces:
  defaults: [cli, lib]
  commands:
    "widget add":
      enabled: [cli, rest, webhook, bus]
      webhook:
        name: widget-create
        map:
          name: "{{ .body.title }}"
        args: "{{ .body.tags }}"
        auth: hmac
        header: X-Hub-Signature-256
        prefix: "sha256="
        secret_env: WIDGET_HOOK_SECRET
      bus:
        request_topic:  widgets.create.req
        response_topic: widgets.create.resp
        group_id:       cmdsurface-widgets
      sinks:
        - { type: webhook, url: "https://audit/x", on: [success, error] }
        - { type: bus, topic: invocations.audit, source: kit }
        - { type: log, level: info, paths: ["widget *"] }
        - { type: file, path: /var/log/kit.jsonl }
    "report daily":
      enabled: [cli, cron]
      cron:
        expr: "0 9 * * *"
        timezone: America/New_York
        args: [--dry-run]
        flags:
          limit: 100
policy:
  destructive_default: deny_remote
`

func TestLoad_WebhookBlock(t *testing.T) {
	cfg, err := Load(strings.NewReader(fixtureBlocksYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	w := cfg.Surfaces.Commands["widget add"].Webhook
	if w.Name != "widget-create" {
		t.Errorf("Webhook.Name=%q want widget-create", w.Name)
	}
	if w.Auth != "hmac" {
		t.Errorf("Webhook.Auth=%q want hmac", w.Auth)
	}
	if w.SecretEnv != "WIDGET_HOOK_SECRET" {
		t.Errorf("Webhook.SecretEnv=%q", w.SecretEnv)
	}
	if w.Header != "X-Hub-Signature-256" {
		t.Errorf("Webhook.Header=%q", w.Header)
	}
	if w.Prefix != "sha256=" {
		t.Errorf("Webhook.Prefix=%q", w.Prefix)
	}
	if w.Map["name"] != "{{ .body.title }}" {
		t.Errorf("Webhook.Map[name]=%q", w.Map["name"])
	}
	if w.ArgsTemplate != "{{ .body.tags }}" {
		t.Errorf("Webhook.ArgsTemplate=%q", w.ArgsTemplate)
	}
}

func TestLoad_BusBlock(t *testing.T) {
	cfg, err := Load(strings.NewReader(fixtureBlocksYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b := cfg.Surfaces.Commands["widget add"].Bus
	if b.RequestTopic != "widgets.create.req" {
		t.Errorf("Bus.RequestTopic=%q", b.RequestTopic)
	}
	if b.ResponseTopic != "widgets.create.resp" {
		t.Errorf("Bus.ResponseTopic=%q", b.ResponseTopic)
	}
	if b.GroupID != "cmdsurface-widgets" {
		t.Errorf("Bus.GroupID=%q", b.GroupID)
	}
}

func TestLoad_CronBlock(t *testing.T) {
	cfg, err := Load(strings.NewReader(fixtureBlocksYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c := cfg.Surfaces.Commands["report daily"].Cron
	if c.Expr != "0 9 * * *" {
		t.Errorf("Cron.Expr=%q", c.Expr)
	}
	if c.Timezone != "America/New_York" {
		t.Errorf("Cron.Timezone=%q", c.Timezone)
	}
	if len(c.Args) != 1 || c.Args[0] != "--dry-run" {
		t.Errorf("Cron.Args=%v", c.Args)
	}
	if c.Flags["limit"] != 100 {
		t.Errorf("Cron.Flags[limit]=%v (%T)", c.Flags["limit"], c.Flags["limit"])
	}
}

func TestLoad_SinksBlock(t *testing.T) {
	cfg, err := Load(strings.NewReader(fixtureBlocksYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sinks := cfg.Surfaces.Commands["widget add"].Sinks
	if len(sinks) != 4 {
		t.Fatalf("sinks len=%d want 4", len(sinks))
	}
	if sinks[0].Type != "webhook" || sinks[0].URL != "https://audit/x" {
		t.Errorf("sinks[0]=%+v", sinks[0])
	}
	if len(sinks[0].On) != 2 {
		t.Errorf("sinks[0].On=%v", sinks[0].On)
	}
	if sinks[1].Type != "bus" || sinks[1].Topic != "invocations.audit" || sinks[1].Source != "kit" {
		t.Errorf("sinks[1]=%+v", sinks[1])
	}
	if sinks[2].Type != "log" || sinks[2].Level != "info" {
		t.Errorf("sinks[2]=%+v", sinks[2])
	}
	if len(sinks[2].Paths) != 1 || sinks[2].Paths[0] != "widget *" {
		t.Errorf("sinks[2].Paths=%v", sinks[2].Paths)
	}
	if sinks[3].Type != "file" || sinks[3].Path != "/var/log/kit.jsonl" {
		t.Errorf("sinks[3]=%+v", sinks[3])
	}
}

func TestLoad_BlocksOmittedByDefault(t *testing.T) {
	cfg, err := Load(strings.NewReader(fixtureYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cc := cfg.Surfaces.Commands["widget add"]
	if cc.Webhook.Name != "" {
		t.Errorf("expected zero Webhook, got %+v", cc.Webhook)
	}
	if cc.Bus.RequestTopic != "" {
		t.Errorf("expected zero Bus, got %+v", cc.Bus)
	}
	if cc.Cron.Expr != "" {
		t.Errorf("expected zero Cron, got %+v", cc.Cron)
	}
	if len(cc.Sinks) != 0 {
		t.Errorf("expected zero Sinks, got %+v", cc.Sinks)
	}
}

const fixtureTelemetryYAML = `
surfaces:
  defaults: [cli, lib]
telemetry:
  enabled:     true
  mode:        full
  channel_cap: 1024
  max_bytes:   16384
  kit_version: "1.2.3"
`

func TestConfig_TelemetryBlock_UnmarshalsAllFields(t *testing.T) {
	cfg, err := Load(strings.NewReader(fixtureTelemetryYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Telemetry == nil {
		t.Fatal("Telemetry block nil after explicit YAML")
	}
	tc := cfg.Telemetry
	if !tc.Enabled {
		t.Errorf("Enabled=%v want true", tc.Enabled)
	}
	if tc.Mode != "full" {
		t.Errorf("Mode=%q want full", tc.Mode)
	}
	if tc.ChannelCap != 1024 {
		t.Errorf("ChannelCap=%d want 1024", tc.ChannelCap)
	}
	if tc.MaxBytes != 16384 {
		t.Errorf("MaxBytes=%d want 16384", tc.MaxBytes)
	}
	if tc.KitVersion != "1.2.3" {
		t.Errorf("KitVersion=%q want 1.2.3", tc.KitVersion)
	}
}

func TestConfig_TelemetryBlock_AbsentLeavesNil(t *testing.T) {
	cfg, err := Load(strings.NewReader(fixtureYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Telemetry != nil {
		t.Errorf("Telemetry=%+v, want nil when YAML omits the block", cfg.Telemetry)
	}
}

func TestConfig_TelemetryBlock_DisabledExplicit(t *testing.T) {
	cfg, err := Load(strings.NewReader(`
telemetry:
  enabled: false
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Telemetry == nil {
		t.Fatal("Telemetry nil but YAML explicitly set the block")
	}
	if cfg.Telemetry.Enabled {
		t.Errorf("Enabled=true want false")
	}
}

func TestTelemetryConfig_ApplyDefaults_FillsZeroValues(t *testing.T) {
	tc := &TelemetryConfig{Enabled: true}
	tc.ApplyDefaults()
	if tc.Mode != "anon" {
		t.Errorf("Mode=%q want anon", tc.Mode)
	}
	if tc.ChannelCap != 256 {
		t.Errorf("ChannelCap=%d want 256", tc.ChannelCap)
	}
	if tc.MaxBytes != 8192 {
		t.Errorf("MaxBytes=%d want 8192", tc.MaxBytes)
	}
	// Idempotency: a second call must not change anything.
	tc.ApplyDefaults()
	if tc.Mode != "anon" || tc.ChannelCap != 256 || tc.MaxBytes != 8192 {
		t.Errorf("ApplyDefaults not idempotent: %+v", tc)
	}
}

func TestTelemetryConfig_ApplyDefaults_PreservesExplicit(t *testing.T) {
	tc := &TelemetryConfig{
		Enabled:    true,
		Mode:       "full",
		ChannelCap: 1024,
		MaxBytes:   16384,
	}
	tc.ApplyDefaults()
	if tc.Mode != "full" {
		t.Errorf("Mode=%q want full (preserved)", tc.Mode)
	}
	if tc.ChannelCap != 1024 {
		t.Errorf("ChannelCap=%d want 1024 (preserved)", tc.ChannelCap)
	}
	if tc.MaxBytes != 16384 {
		t.Errorf("MaxBytes=%d want 16384 (preserved)", tc.MaxBytes)
	}
}

func TestTelemetryConfig_ApplyDefaults_NoOpWhenDisabled(t *testing.T) {
	tc := &TelemetryConfig{Enabled: false}
	tc.ApplyDefaults()
	if tc.Mode != "" {
		t.Errorf("Mode=%q want \"\" (disabled block)", tc.Mode)
	}
	if tc.ChannelCap != 0 {
		t.Errorf("ChannelCap=%d want 0 (disabled block)", tc.ChannelCap)
	}
	if tc.MaxBytes != 0 {
		t.Errorf("MaxBytes=%d want 0 (disabled block)", tc.MaxBytes)
	}
}

func TestFromConfig_BlocksDoNotBreakEnablement(t *testing.T) {
	// Loading a fixture with webhook/bus/cron/sinks blocks must
	// not regress the foundation behavior: per-command Enabled is
	// still applied verbatim.
	cfg, err := Load(strings.NewReader(fixtureBlocksYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b, err := FromConfig(newBridgeTree(), cfg)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	for _, l := range b.Leaves() {
		if l.PathKey() == "widget add" {
			for _, s := range []Surface{SurfaceCLI, SurfaceREST, SurfaceWebhook, SurfaceBus} {
				if !l.Enabled[s] {
					t.Errorf("widget add missing surface %s", s)
				}
			}
		}
	}
}
