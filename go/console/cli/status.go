package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/output"
)

// Status section status enums. Surface in StatusOutput.Sections[i].Status
// so adopters can sort/filter by lookup outcome without inspecting
// section-specific shapes.
const (
	StatusOK          = "ok"
	StatusEmpty       = "empty"
	StatusUnavailable = "unavailable"
	StatusError       = "error"
)

// Default timeouts for the status pipeline. Promote to StatusConfig
// fields only when adopter demand surfaces.
const (
	defaultStatusProviderTimeout = 2 * time.Second
	defaultStatusOverallTimeout  = 10 * time.Second
)

// Default key-pattern redaction set. Keys containing any of these
// substrings (case-insensitive) render as "[redacted]" inside
// sensitive sections unless --show-sensitive is passed.
var defaultRedactPatterns = []string{"TOKEN", "SECRET", "KEY", "PASSWORD"}

// ShowSensitiveFlag values control how --show-sensitive is registered
// on the status subcommand. Empty = default ("hidden" — registered
// but kept out of --help). "always" exposes the flag in help;
// "disabled" omits registration entirely so sensitive values never
// surface.
const (
	StatusShowSensitiveDefault  = ""
	StatusShowSensitiveAlways   = "always"
	StatusShowSensitiveHidden   = "hidden"
	StatusShowSensitiveDisabled = "disabled"
)

// StatusSection is one row in the status output. Adopters return one
// per registered provider; Data carries an arbitrary JSON-serializable
// payload (struct/map/slice with json tags).
type StatusSection struct {
	// Title is the human-friendly section name (e.g. "profile",
	// "workspace"). Lower-case is conventional.
	Title string `json:"title" yaml:"title"`
	// Data is the section payload. Required when Status=="ok".
	Data any `json:"data,omitempty" yaml:"data,omitempty"`
	// Status is one of "ok" / "empty" / "unavailable" / "error".
	// Empty value is normalized to "ok".
	Status string `json:"status" yaml:"status"`
	// ErrorMessage is non-empty only when Status=="error". Sanitized
	// before set — adopters are responsible for stripping secrets.
	ErrorMessage string `json:"error_message,omitempty" yaml:"error_message,omitempty"`
	// Sensitive marks the section as containing sensitive data.
	// When true, Data values render as "[redacted]" inside the
	// section unless --show-sensitive was passed. Key-pattern
	// matching applies field-by-field on map/struct payloads.
	Sensitive bool `json:"sensitive,omitempty" yaml:"sensitive,omitempty"`
	// Priority controls render order; lower runs first. Kit-shipped
	// providers occupy 100-600; adopter providers should start at
	// 1000+ to keep kit's surface stable.
	Priority int `json:"priority,omitempty" yaml:"priority,omitempty"`
}

// StatusProvider is the function shape every status section feeder
// implements. Adopters register one per provider name; kit ships six
// defaults (see DefaultStatusProviders).
type StatusProvider func(ctx context.Context) (StatusSection, error)

// StatusOutput is the wire shape of the status subcommand response.
// Tagged json + yaml so output.Render picks it up directly.
type StatusOutput struct {
	Sections []StatusSection `json:"sections" yaml:"sections"`
}

// StatusConfig tunes the kit-shipped status subcommand. Pass to
// WithStatus to mount it.
type StatusConfig struct {
	// ExtraEnvKeys widens the env-filter beyond KIT_*. Pass each
	// prefix or exact name (e.g. "SPACED_*" or "FOO_BAR").
	ExtraEnvKeys []string
	// RedactPatterns appends to the kit default
	// (*TOKEN*/*SECRET*/*KEY*/*PASSWORD*). Match is case-insensitive
	// substring; callers wanting exact match prepend "^" / append
	// "$" — but the matcher today is plain substring, not regex.
	RedactPatterns []string
	// DisableDefaultProviders names kit-shipped providers to
	// suppress (e.g. ["workspace"] when adopter has no workspace
	// concept). Adopter providers always run.
	DisableDefaultProviders []string
	// ShowSensitiveFlag selects how --show-sensitive is registered.
	// "" or "hidden" (default): flag registered, hidden from --help.
	// "always": exposed in --help. "disabled": flag not registered,
	// sensitive values always redact.
	ShowSensitiveFlag string
}

// WithStatus returns a cli.New option that mounts the kit-shipped
// `<tool> status` subcommand and seeds the reservedSnapshot. The
// subcommand walks every registered StatusProvider (defaults +
// adopter-registered) and renders a StatusOutput in the active
// --format.
//
// Adopters extend via RegisterStatusProvider before Execute; kit's
// own status is self-annotated to pass the validator under
// EnforceValidate=true.
func WithStatus(cfg StatusConfig) func(*Root) {
	return func(r *Root) {
		if r == nil || r.Cmd == nil {
			return
		}
		r.statusConfig = cfg
		r.ensureStatusProviders()
		cmd := buildStatusCmd(r, cfg)
		r.Cmd.AddCommand(cmd)
	}
}

// RegisterStatusProvider records fn under name on r. Last write wins.
// Adopters call this before Execute(); registrations after the
// validator runs are no-ops. Kit-shipped names live in 100-600
// priority band; adopter names should start at 1000+.
func (r *Root) RegisterStatusProvider(name string, fn StatusProvider) {
	if r == nil || name == "" || fn == nil {
		return
	}
	r.statusProvidersMu.Lock()
	defer r.statusProvidersMu.Unlock()
	if r.statusProviders == nil {
		r.statusProviders = make(map[string]StatusProvider)
	}
	r.statusProviders[name] = fn
}

// ensureStatusProviders seeds the default-provider set if not already
// populated. Idempotent.
func (r *Root) ensureStatusProviders() {
	r.statusProvidersMu.Lock()
	defer r.statusProvidersMu.Unlock()
	if r.statusProviders == nil {
		r.statusProviders = make(map[string]StatusProvider)
	}
	disabled := map[string]struct{}{}
	for _, name := range r.statusConfig.DisableDefaultProviders {
		disabled[name] = struct{}{}
	}
	for name, fn := range defaultStatusProviders(r) {
		if _, off := disabled[name]; off {
			continue
		}
		if _, exists := r.statusProviders[name]; exists {
			continue
		}
		r.statusProviders[name] = fn
	}
}

// defaultStatusProviders returns the six kit-shipped providers.
// Closures over r so each provider can read the Root state at the
// time the section is rendered (not at WithStatus time).
func defaultStatusProviders(r *Root) map[string]StatusProvider {
	return map[string]StatusProvider{
		"profile":          profileStatusProvider(r),
		"env":              envStatusProvider(r),
		"workspace":        workspaceStatusProvider(r),
		"auth":             authStatusProvider(r),
		"effective-config": configStatusProvider(r),
		"kit-annotations":  annotationsStatusProvider(r),
	}
}

func profileStatusProvider(_ *Root) StatusProvider {
	return func(_ context.Context) (StatusSection, error) {
		sec := StatusSection{Title: "profile", Priority: 100}
		if u, err := user.Current(); err == nil && u.Username != "" {
			sec.Status = StatusOK
			sec.Data = map[string]string{"user": u.Username}
			return sec, nil
		}
		if v := os.Getenv("USER"); v != "" {
			sec.Status = StatusOK
			sec.Data = map[string]string{"user": v}
			return sec, nil
		}
		sec.Status = StatusUnavailable
		return sec, nil
	}
}

func envStatusProvider(r *Root) StatusProvider {
	return func(_ context.Context) (StatusSection, error) {
		sec := StatusSection{Title: "env", Priority: 200, Sensitive: true}
		filtered := filterEnv(os.Environ(), r.statusConfig.ExtraEnvKeys)
		if len(filtered) == 0 {
			sec.Status = StatusEmpty
			return sec, nil
		}
		sec.Status = StatusOK
		sec.Data = filtered
		return sec, nil
	}
}

func workspaceStatusProvider(_ *Root) StatusProvider {
	// Kit does not bundle a workspace manager; adopters that wire
	// `wsm` re-register this provider to surface the active
	// workspace. Default returns "unavailable" to keep the surface
	// shape consistent.
	return func(_ context.Context) (StatusSection, error) {
		return StatusSection{
			Title:    "workspace",
			Priority: 300,
			Status:   StatusUnavailable,
		}, nil
	}
}

func authStatusProvider(r *Root) StatusProvider {
	return func(ctx context.Context) (StatusSection, error) {
		sec := StatusSection{Title: "auth", Priority: 400, Sensitive: true}
		if r.Auth == nil {
			sec.Status = StatusUnavailable
			return sec, nil
		}
		cred, err := r.Auth.Inspect(ctx)
		if err != nil {
			sec.Status = StatusError
			sec.ErrorMessage = err.Error()
			return sec, sec.toError()
		}
		if cred == nil {
			sec.Status = StatusEmpty
			return sec, nil
		}
		sec.Status = StatusOK
		// Cred holds Source + Identity + Scopes — Scopes can leak
		// privilege information, so the redactor will key-pattern
		// match when the section renders.
		data := map[string]any{
			"source":   cred.Source,
			"identity": cred.Identity,
		}
		if len(cred.Scopes) > 0 {
			data["scopes"] = cred.Scopes
		}
		if cred.ExpiresAt != nil {
			data["expires_at"] = cred.ExpiresAt.UTC().Format(time.RFC3339)
		}
		data["renewable"] = cred.Renewable
		sec.Data = data
		return sec, nil
	}
}

func configStatusProvider(r *Root) StatusProvider {
	return func(_ context.Context) (StatusSection, error) {
		sec := StatusSection{Title: "effective-config", Priority: 500, Sensitive: true}
		if r.Viper == nil {
			sec.Status = StatusUnavailable
			return sec, nil
		}
		settings := r.Viper.AllSettings()
		if len(settings) == 0 {
			sec.Status = StatusEmpty
			return sec, nil
		}
		sec.Status = StatusOK
		sec.Data = settings
		return sec, nil
	}
}

func annotationsStatusProvider(r *Root) StatusProvider {
	return func(_ context.Context) (StatusSection, error) {
		sec := StatusSection{Title: "kit-annotations", Priority: 600}
		data := map[string]any{}
		if r.Cmd != nil && r.Cmd.Annotations != nil {
			if v := r.Cmd.Annotations["kit/min-api-version"]; v != "" {
				data["min_api_version"] = v
			}
			if v := r.Cmd.Annotations["kit/since"]; v != "" {
				data["since"] = v
			}
		}
		data["reserved_subcommands"] = r.ReservedSubcommands()
		data["enforce_validate"] = r.Config.EnforceValidate
		sec.Status = StatusOK
		sec.Data = data
		return sec, nil
	}
}

// filterEnv keeps env vars starting with KIT_ plus any prefix in
// extras. Each entry comes back as "KEY=VALUE" string preserved so
// the redactor key-pattern matches on the unsplit key.
func filterEnv(env []string, extras []string) map[string]string {
	out := map[string]string{}
	for _, e := range env {
		eq := strings.IndexByte(e, '=')
		if eq < 0 {
			continue
		}
		k := e[:eq]
		v := e[eq+1:]
		if strings.HasPrefix(k, "KIT_") {
			out[k] = v
			continue
		}
		for _, p := range extras {
			if matchesEnvPrefix(k, p) {
				out[k] = v
				break
			}
		}
	}
	return out
}

// matchesEnvPrefix supports "PREFIX_*" suffix-wildcards and exact
// matches. No full glob — enough for the documented contract.
func matchesEnvPrefix(name, pattern string) bool {
	if pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	return name == pattern
}

func (s StatusSection) toError() error {
	if s.Status != StatusError {
		return nil
	}
	return fmt.Errorf("status provider %s: %s", s.Title, s.ErrorMessage)
}

// buildStatusCmd builds the `<tool> status` cobra command and
// self-annotates it for §4 conformance.
func buildStatusCmd(r *Root, cfg StatusConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show kit runtime status (profile, env, workspace, auth, config)",
		Long: "Inspect kit's runtime state across the six default sections — " +
			"profile, env, workspace, auth, effective-config, kit-annotations — " +
			"plus any adopter-registered providers. Read-only: no network calls, " +
			"no state mutation. Sensitive sections (env, auth, effective-config) " +
			"render values as [redacted] unless --show-sensitive is passed.",
		Args: cobra.NoArgs,
	}
	showSensitivePolicy := cfg.ShowSensitiveFlag
	if showSensitivePolicy == StatusShowSensitiveDefault {
		showSensitivePolicy = StatusShowSensitiveHidden
	}
	if showSensitivePolicy != StatusShowSensitiveDisabled {
		cmd.Flags().Bool("show-sensitive", false,
			"Show redacted values (env, auth, config). Logs an audit line via slog.")
		if showSensitivePolicy == StatusShowSensitiveHidden {
			_ = cmd.Flags().MarkHidden("show-sensitive")
		}
	}
	cmd.RunE = func(c *cobra.Command, _ []string) error {
		return runStatus(c, r)
	}

	SetSideEffect(cmd, SideEffectRead)
	SetIdempotency(cmd, IdempotencyYes)
	SetTopLevelVerb(cmd)
	_ = SetOutputSchema(cmd, OutputSchema{Type: &StatusOutput{}, Version: "1.0"})
	_ = SetExamples(cmd, []Example{
		{Title: "Default sections", Command: r.Config.Name + " status"},
		{Title: "Reveal redactions", Command: r.Config.Name + " status --show-sensitive"},
		{Title: "JSON for agents", Command: r.Config.Name + " status --format json"},
	})
	_ = SetNextSteps(cmd, []NextStep{
		{
			When:    "auth section reports unavailable",
			Suggest: r.Config.Name + " auth login",
			Reason:  "No credential introspector wired on the root",
		},
		{
			When:    "env section is empty",
			Suggest: "Set KIT_* env vars or pass StatusConfig.ExtraEnvKeys",
			Reason:  "Default filter only picks up KIT_* names",
		},
	})
	return cmd
}

// runStatus executes every registered provider with per-provider and
// overall deadlines, applies redaction, and renders via output.Render.
func runStatus(cmd *cobra.Command, r *Root) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), defaultStatusOverallTimeout)
	defer cancel()

	showSensitive := false
	if f := cmd.Flags().Lookup("show-sensitive"); f != nil {
		showSensitive, _ = cmd.Flags().GetBool("show-sensitive")
	}
	if showSensitive {
		emitShowSensitiveAudit(ctx)
	}

	r.statusProvidersMu.Lock()
	names := make([]string, 0, len(r.statusProviders))
	for k := range r.statusProviders {
		names = append(names, k)
	}
	providers := make(map[string]StatusProvider, len(r.statusProviders))
	for k, v := range r.statusProviders {
		providers[k] = v
	}
	r.statusProvidersMu.Unlock()
	sort.Strings(names)

	sections := make([]StatusSection, 0, len(names))
	for _, name := range names {
		sec := runProvider(ctx, name, providers[name])
		sections = append(sections, sec)
	}
	// Stable priority order then by title for ties.
	sort.SliceStable(sections, func(i, j int) bool {
		if sections[i].Priority != sections[j].Priority {
			return sections[i].Priority < sections[j].Priority
		}
		return sections[i].Title < sections[j].Title
	})

	redactPatterns := append([]string{}, defaultRedactPatterns...)
	redactPatterns = append(redactPatterns, r.statusConfig.RedactPatterns...)
	if !showSensitive {
		for i := range sections {
			sections[i].Data = redactSection(sections[i], redactPatterns)
		}
	}

	out := StatusOutput{Sections: sections}
	format := resolveStatusFormat(cmd)
	return output.Render(cmd.OutOrStdout(), format, out)
}

func runProvider(ctx context.Context, name string, fn StatusProvider) StatusSection {
	pctx, cancel := context.WithTimeout(ctx, defaultStatusProviderTimeout)
	defer cancel()
	resultCh := make(chan struct {
		sec StatusSection
		err error
	}, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				resultCh <- struct {
					sec StatusSection
					err error
				}{
					sec: StatusSection{
						Title:        name,
						Status:       StatusError,
						ErrorMessage: fmt.Sprintf("panic: %v", rec),
					},
				}
			}
		}()
		sec, err := fn(pctx)
		if sec.Title == "" {
			sec.Title = name
		}
		if sec.Status == "" && err == nil {
			sec.Status = StatusOK
		}
		resultCh <- struct {
			sec StatusSection
			err error
		}{sec, err}
	}()
	select {
	case <-pctx.Done():
		if pctx.Err() == context.DeadlineExceeded {
			return StatusSection{
				Title:        name,
				Status:       StatusUnavailable,
				ErrorMessage: fmt.Sprintf("timeout after %s", defaultStatusProviderTimeout),
			}
		}
		return StatusSection{Title: name, Status: StatusUnavailable, ErrorMessage: "canceled"}
	case res := <-resultCh:
		if res.err != nil && res.sec.Status != StatusError {
			res.sec.Status = StatusError
			res.sec.ErrorMessage = res.err.Error()
		}
		return res.sec
	}
}

// redactSection returns a copy of section.Data with sensitive values
// replaced by "[redacted]". Non-sensitive sections pass through.
// Within sensitive sections, key-pattern match decides per-field
// redaction; non-string scalars and unmatched keys pass through.
func redactSection(sec StatusSection, patterns []string) any {
	if !sec.Sensitive || sec.Data == nil {
		return sec.Data
	}
	switch v := sec.Data.(type) {
	case map[string]string:
		out := make(map[string]string, len(v))
		for k, vv := range v {
			if shouldRedact(k, patterns) {
				out[k] = "[redacted]"
			} else {
				out[k] = vv
			}
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, vv := range v {
			if shouldRedact(k, patterns) {
				out[k] = "[redacted]"
			} else {
				out[k] = vv
			}
		}
		return out
	default:
		// Unknown shape; redact whole-section to be safe.
		return "[redacted]"
	}
}

// shouldRedact reports whether key matches any redact pattern
// (case-insensitive substring).
func shouldRedact(key string, patterns []string) bool {
	upper := strings.ToUpper(key)
	for _, p := range patterns {
		if strings.Contains(upper, strings.ToUpper(p)) {
			return true
		}
	}
	return false
}

func emitShowSensitiveAudit(ctx context.Context) {
	name := "unknown"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	slog.InfoContext(ctx, "status invoked with --show-sensitive", "user", name)
}

func resolveStatusFormat(cmd *cobra.Command) output.Format {
	for c := cmd; c != nil; c = c.Parent() {
		if f := c.Flags().Lookup("format"); f != nil {
			if v := f.Value.String(); v != "" {
				return v
			}
		}
		if pf := c.PersistentFlags().Lookup("format"); pf != nil {
			if v := pf.Value.String(); v != "" {
				return v
			}
		}
	}
	return output.JSON
}
