package cmdsurface_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"hop.top/kit/go/transport/cmdsurface"
)

// translateEnv is a lookupEnv over a fixed map, so no test reads the
// process environment.
func translateEnv(vars map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
}

func loadTranslateConfig(t *testing.T, yaml string) cmdsurface.Config {
	t.Helper()
	cfg, err := cmdsurface.Load(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestConfigWebhookMappingsTranslatesEveryBlock(t *testing.T) {
	cfg := loadTranslateConfig(t, `
surfaces:
  commands:
    "widget secure":
      webhook:
        name: secure-hook
        auth: bearer
        token_env: SECURE_TOKEN
    "widget add":
      enabled: [cli, webhook]
      webhook:
        name: widget-create
        map:
          name: "{{ .body.title }}"
        args: "{{ .body.tags }}"
        auth: hmac
        header: X-Hub-Signature-256
        prefix: "sha256="
        secret_env: WIDGET_SECRET
    "ping":
      webhook:
        name: ping-hook
    "report *":
      enabled: [cli]
`)
	got, err := cfg.WebhookMappings(translateEnv(map[string]string{
		"WIDGET_SECRET": "s3cret",
		"SECURE_TOKEN":  "tok",
	}))
	if err != nil {
		t.Fatalf("WebhookMappings: %v", err)
	}
	want := []cmdsurface.WebhookMapping{
		{Name: "ping-hook", Path: []string{"ping"}, Auth: cmdsurface.AuthNone{}},
		{
			Name:         "widget-create",
			Path:         []string{"widget", "add"},
			FlagMap:      map[string]string{"name": "{{ .body.title }}"},
			ArgsTemplate: "{{ .body.tags }}",
			Auth: cmdsurface.AuthHMAC{
				Header: "X-Hub-Signature-256",
				Prefix: "sha256=",
				Secret: []byte("s3cret"),
			},
		},
		{Name: "secure-hook", Path: []string{"widget", "secure"}, Auth: cmdsurface.AuthBearer{Token: "tok"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mappings:\n got %#v\nwant %#v", got, want)
	}
}

func TestConfigWebhookMappingsRefusesWhatCannotMount(t *testing.T) {
	env := translateEnv(map[string]string{"SET": "v", "EMPTY": ""})
	cases := []struct {
		name, block, pattern, want string
	}{
		{"wildcard pattern", `{name: w, auth: none}`, `"widget *"`, "exactly one command"},
		{"catch-all pattern", `{name: w}`, `"*"`, "exactly one command"},
		{"missing name", `{auth: none, map: {a: b}}`, `"ping"`, "name is required"},
		{"unknown auth", `{name: w, auth: basic}`, `"ping"`, `unknown auth "basic"`},
		{"hmac without header", `{name: w, auth: hmac, secret_env: SET}`, `"ping"`, "hmac needs header"},
		{"hmac without secret_env", `{name: w, auth: hmac, header: X-Sig}`, `"ping"`, "hmac needs secret_env"},
		{"hmac secret unset", `{name: w, auth: hmac, header: X-Sig, secret_env: UNSET}`, `"ping"`, "UNSET is not set"},
		{"hmac secret empty", `{name: w, auth: hmac, header: X-Sig, secret_env: EMPTY}`, `"ping"`, "EMPTY is not set"},
		{"bearer without token_env", `{name: w, auth: bearer}`, `"ping"`, "bearer needs token_env"},
		{"bearer token unset", `{name: w, auth: bearer, token_env: UNSET}`, `"ping"`, "UNSET is not set"},
		{"hmac key on bearer", `{name: w, auth: bearer, token_env: SET, header: X-Sig}`, `"ping"`, "header applies to hmac"},
		{"bearer key on none", `{name: w, token_env: SET}`, `"ping"`, "token_env applies to bearer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadTranslateConfig(t, "surfaces:\n  commands:\n    "+tc.pattern+":\n      webhook: "+tc.block+"\n")
			_, err := cfg.WebhookMappings(env)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestConfigWebhookMappingsNeedsLookupForSecrets(t *testing.T) {
	cfg := loadTranslateConfig(t, `
surfaces:
  commands:
    "ping":
      webhook: {name: w, auth: bearer, token_env: TOKEN}
`)
	if _, err := cfg.WebhookMappings(nil); err == nil || !strings.Contains(err.Error(), "lookupEnv") {
		t.Fatalf("err = %v, want a nil-lookupEnv refusal", err)
	}
}

// TestConfigWebhookBlockReachesTheMount is the point of the
// translation: a webhook declared in YAML is served, verified, and
// invokes its command with the templated flags.
func TestConfigWebhookBlockReachesTheMount(t *testing.T) {
	cfg := loadTranslateConfig(t, `
surfaces:
  commands:
    "widget add":
      enabled: [cli, webhook]
      webhook:
        name: widget-create
        map:
          name: "{{ .body.title }}"
        auth: hmac
        header: X-Hub-Signature-256
        prefix: "sha256="
        secret_env: WIDGET_SECRET
`)
	runner := &webhookFakeRunner{}
	b, err := cmdsurface.FromConfig(webhookTestTree(), cfg, cmdsurface.WithRunner(runner))
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	mappings, err := cfg.WebhookMappings(translateEnv(map[string]string{"WIDGET_SECRET": "s3cret"}))
	if err != nil {
		t.Fatalf("WebhookMappings: %v", err)
	}
	url, stop := webhookNewServer(t, b, mappings)
	defer stop()

	body := []byte(`{"title":"gizmo"}`)
	status, raw := webhookPost(t, url+"/hooks/widget-create", body, "application/json", map[string]string{
		"X-Hub-Signature-256": "sha256=" + webhookTestHMAC([]byte("s3cret"), body),
	})
	if status != 202 {
		t.Fatalf("status = %d, body = %s", status, raw)
	}
	got := runner.captured()
	if len(got) != 1 || strings.Join(got[0].Path, " ") != "widget add" || got[0].Flags["name"] != "gizmo" {
		t.Fatalf("invocations = %#v, want one widget add with name=gizmo", got)
	}

	status, _ = webhookPost(t, url+"/hooks/widget-create", body, "application/json", map[string]string{
		"X-Hub-Signature-256": "sha256=" + webhookTestHMAC([]byte("wrong"), body),
	})
	if status != 401 {
		t.Fatalf("badly signed status = %d, want 401", status)
	}
}

func TestConfigBusBindingsTranslatesEveryBlock(t *testing.T) {
	cfg := loadTranslateConfig(t, `
surfaces:
  commands:
    "widget add":
      bus:
        request_topic: widgets.create.req
        response_topic: widgets.create.resp
        group_id: widgets
    "echo":
      bus:
        request_topic: echo.req
    "report *":
      enabled: [cli]
`)
	got, err := cfg.BusBindings()
	if err != nil {
		t.Fatalf("BusBindings: %v", err)
	}
	want := []cmdsurface.BusBinding{
		{Path: []string{"echo"}, RequestTopic: "echo.req"},
		{
			Path:          []string{"widget", "add"},
			RequestTopic:  "widgets.create.req",
			ResponseTopic: "widgets.create.resp",
			GroupID:       "widgets",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bindings:\n got %#v\nwant %#v", got, want)
	}
}

func TestConfigBusBindingsRefusesWhatCannotMount(t *testing.T) {
	cases := []struct {
		name, pattern, block, want string
	}{
		{"wildcard pattern", `"widget *"`, `{request_topic: t}`, "exactly one command"},
		{"response without request", `"echo"`, `{response_topic: r}`, "request_topic is required"},
		{"group without request", `"echo"`, `{group_id: g}`, "request_topic is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadTranslateConfig(t, "surfaces:\n  commands:\n    "+tc.pattern+":\n      bus: "+tc.block+"\n")
			_, err := cfg.BusBindings()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestConfigCronSchedulesTranslatesEveryBlock(t *testing.T) {
	cfg := loadTranslateConfig(t, `
surfaces:
  commands:
    "report daily":
      cron:
        expr: "0 9 * * *"
        timezone: America/New_York
        args: ["--dry-run"]
        flags:
          limit: 100
    "jobs cleanup":
      cron:
        expr: "*/5 * * * *"
`)
	got, err := cfg.CronSchedules()
	if err != nil {
		t.Fatalf("CronSchedules: %v", err)
	}
	want := []cmdsurface.CronSchedule{
		{Path: []string{"jobs", "cleanup"}, Expr: "*/5 * * * *"},
		{
			Path:     []string{"report", "daily"},
			Expr:     "0 9 * * *",
			Timezone: "America/New_York",
			Args:     []string{"--dry-run"},
			Flags:    map[string]any{"limit": 100},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schedules:\n got %#v\nwant %#v", got, want)
	}
}

func TestConfigCronSchedulesRefusesWhatCannotMount(t *testing.T) {
	cases := []struct {
		name, pattern, block, want string
	}{
		{"wildcard pattern", `"report *"`, `{expr: "* * * * *"}`, "exactly one command"},
		{"timezone without expr", `"ping"`, `{timezone: UTC}`, "expr is required"},
		{"args without expr", `"ping"`, `{args: [a]}`, "expr is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadTranslateConfig(t, "surfaces:\n  commands:\n    "+tc.pattern+":\n      cron: "+tc.block+"\n")
			_, err := cfg.CronSchedules()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestConfigCronBlockReachesTheMount: a schedule declared in YAML is
// registered with the engine and fires its command with the baked-in
// args and flags.
func TestConfigCronBlockReachesTheMount(t *testing.T) {
	cfg := loadTranslateConfig(t, `
surfaces:
  commands:
    "ping":
      enabled: [cli, cron]
      cron:
        expr: "*/5 * * * *"
        flags:
          count: 3
`)
	runner := &webhookFakeRunner{}
	b, err := cmdsurface.FromConfig(webhookTestTree(), cfg, cmdsurface.WithRunner(runner))
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	schedules, err := cfg.CronSchedules()
	if err != nil {
		t.Fatalf("CronSchedules: %v", err)
	}
	engine := newCronTestEngine()
	cleanup, err := cmdsurface.MountCron(b, engine, schedules, cmdsurface.WithCronContext(context.Background()))
	if err != nil {
		t.Fatalf("MountCron: %v", err)
	}
	defer cleanup()
	if engine.jobCount() != 1 {
		t.Fatalf("jobs = %d, want 1", engine.jobCount())
	}
	engine.triggerAll()
	got := runner.captured()
	if len(got) != 1 || strings.Join(got[0].Path, " ") != "ping" || got[0].Flags["count"] != 3 {
		t.Fatalf("invocations = %#v, want one ping with count=3", got)
	}
}

// TestConfigTranslatorsIgnoreCommandsWithoutBlocks: an enabled-only
// entry (the common case) produces nothing and no error.
func TestConfigTranslatorsIgnoreCommandsWithoutBlocks(t *testing.T) {
	cfg := loadTranslateConfig(t, fixtureTranslateYAML)
	if m, err := cfg.WebhookMappings(nil); err != nil || len(m) != 0 {
		t.Fatalf("WebhookMappings = %v, %v", m, err)
	}
	if bb, err := cfg.BusBindings(); err != nil || len(bb) != 0 {
		t.Fatalf("BusBindings = %v, %v", bb, err)
	}
	if cs, err := cfg.CronSchedules(); err != nil || len(cs) != 0 {
		t.Fatalf("CronSchedules = %v, %v", cs, err)
	}
}

const fixtureTranslateYAML = `
surfaces:
  defaults: [cli, lib]
  commands:
    "widget *":
      enabled: [cli, rest]
    "*":
      enabled: [cli]
`
