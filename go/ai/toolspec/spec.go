package toolspec

// SafetyLevel classifies how risky a command invocation is.
type SafetyLevel string

const (
	SafetyLevelSafe      SafetyLevel = "safe"
	SafetyLevelCaution   SafetyLevel = "caution"
	SafetyLevelDangerous SafetyLevel = "dangerous"
)

// Contract describes behavioral guarantees of a command.
type Contract struct {
	Idempotent    bool     `json:"idempotent,omitempty"`
	SideEffects   []string `json:"side_effects,omitempty"`
	Retryable     bool     `json:"retryable,omitempty"`
	PreConditions []string `json:"pre_conditions,omitempty"`
}

// Safety captures risk metadata for a command.
type Safety struct {
	Level                SafetyLevel `json:"level"`
	RequiresConfirmation bool        `json:"requires_confirmation,omitempty"`
	Permissions          []string    `json:"permissions,omitempty"`
}

// OutputSchema describes the expected output of a command.
type OutputSchema struct {
	Format  string   `json:"format,omitempty"`
	Fields  []string `json:"fields,omitempty"`
	Example string   `json:"example,omitempty"`
}

// StateIntrospection lists commands/vars for discovering tool state.
type StateIntrospection struct {
	ConfigCommands []string `json:"config_commands,omitempty"`
	EnvVars        []string `json:"env_vars,omitempty"`
	AuthCommands   []string `json:"auth_commands,omitempty"`
}

// Provenance records where a piece of spec data came from.
type Provenance struct {
	Source      string  `json:"source,omitempty"`
	RetrievedAt string  `json:"retrieved_at,omitempty"`
	Confidence  float32 `json:"confidence,omitempty"`
}

// Intent classifies a command's purpose.
type Intent struct {
	Domain   string   `json:"domain,omitempty"`
	Category string   `json:"category,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

// ToolSpec captures everything known about a single CLI tool.
type ToolSpec struct {
	Name               string              `json:"name"`
	SchemaVersion      string              `json:"schema_version,omitempty"`
	Commands           []Command           `json:"commands,omitempty"`
	Flags              []Flag              `json:"flags,omitempty"`
	ErrorPatterns      []ErrorPattern      `json:"error_patterns,omitempty"`
	Workflows          []Workflow          `json:"workflows,omitempty"`
	StateIntrospection *StateIntrospection `json:"state_introspection,omitempty"`
}

// Command is a (sub)command in a CLI tool's command tree.
type Command struct {
	Name            string        `json:"name"`
	Aliases         []string      `json:"aliases,omitempty"`
	Flags           []Flag        `json:"flags,omitempty"`
	Children        []Command     `json:"children,omitempty"`
	Contract        *Contract     `json:"contract,omitempty"`
	Safety          *Safety       `json:"safety,omitempty"`
	PreviewModes    []string      `json:"preview_modes,omitempty"`
	OutputSchema    *OutputSchema `json:"output_schema,omitempty"`
	Deprecated      bool          `json:"deprecated,omitempty"`
	DeprecatedSince string        `json:"deprecated_since,omitempty"`
	ReplacedBy      string        `json:"replaced_by,omitempty"`
	Intent          *Intent       `json:"intent,omitempty"`
	SuggestedNext   []string      `json:"suggested_next,omitempty"`
}

// Flag describes a single CLI flag.
type Flag struct {
	Name        string `json:"name"`
	Short       string `json:"short,omitempty"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	Deprecated  bool   `json:"deprecated,omitempty"`
	ReplacedBy  string `json:"replaced_by,omitempty"`
}

// ErrorPattern maps a known error output to a fix.
type ErrorPattern struct {
	Pattern    string      `json:"pattern"`
	Fix        string      `json:"fix"`
	Source     string      `json:"source,omitempty"`
	Cause      string      `json:"cause,omitempty"`
	Fixes      []string    `json:"fixes,omitempty"`
	Confidence float32     `json:"confidence,omitempty"`
	Provenance *Provenance `json:"provenance,omitempty"`
}

// Workflow describes a common multi-step sequence.
type Workflow struct {
	Name       string              `json:"name"`
	Steps      []string            `json:"steps"`
	After      map[string][]string `json:"after,omitempty"`
	Provenance *Provenance         `json:"provenance,omitempty"`
}

// Manifest is the machine-readable capability manifest emitted by the
// `<tool> spec` subcommand (see ~/.ops/docs/cli-conventions-with-kit.md
// §13). Distinct from ToolSpec, which is the rich data model populated
// by source plugins (LLM, tldr, completion, …); Manifest is the
// authoritative, tool-internal description rendered for agents.
type Manifest struct {
	// Tool is the binary name (cli.Config.Name).
	Tool string `json:"tool"`
	// Version is the tool's semver string (cli.Config.Version).
	Version string `json:"version"`
	// SchemaVersion is the CLI schema version (MAJOR.MINOR) the tool
	// claims, supplied by the adopter via RegisterSpecCommand.
	SchemaVersion string `json:"schema_version"`
	// Commands is the flat list of leaf commands in the tool's tree,
	// each with its full discoverable metadata.
	Commands []ManifestCommand `json:"commands"`
}

// ManifestCommand is one leaf in the live cobra tree projected into the
// manifest shape. Distinct from Command (the source-plugin data shape):
// every field comes from cobra annotations or built-ins kit owns.
//
// Schema 1.0 covers the first 11 fields below. Schema 1.1 adds the
// 13 trailing fields; all additions are
// `omitempty` unless the explicit false carries information
// (Retryable, DryRunSupported). Renames or removals require a major
// bump per factor 12.
type ManifestCommand struct {
	// Path is the cobra command path split into segments
	// (e.g. ["mytool", "alias", "add"]).
	Path []string `json:"path"`
	// Short is the one-line description (cobra.Command.Short).
	Short string `json:"short"`
	// SideEffect is the kit/side-effect class (read|write|destructive|interactive).
	SideEffect string `json:"side_effect"`
	// Idempotent is the kit/idempotent class (yes|no|conditional).
	Idempotent string `json:"idempotent"`
	// Args lists declared positional arguments. Optional — cobra does
	// not introspect arg names by default; this is populated from the
	// kit/args annotation (comma-separated names) when present.
	Args []ManifestArg `json:"args,omitempty"`
	// Flags lists declared flags on this leaf (excluding inherited
	// persistent globals).
	Flags []ManifestFlag `json:"flags,omitempty"`
	// ExitCodes is the set of exit-code symbols the command may
	// produce (kit/exit-codes annotation, comma-separated).
	ExitCodes []string `json:"exit_codes,omitempty"`
	// Deprecated reports whether cobra.Command.Deprecated is set.
	Deprecated bool `json:"deprecated,omitempty"`
	// DeprecatedSince is the kit/deprecated-since annotation.
	DeprecatedSince string `json:"deprecated_since,omitempty"`
	// RemovalTarget is the kit/removal-target annotation.
	RemovalTarget string `json:"removal_target,omitempty"`
	// SinceVersion is the kit/since annotation — the schema version in
	// which this command first appeared (used for --api-version
	// compatibility-mode filtering).
	SinceVersion string `json:"since_version,omitempty"`

	// === Schema 1.1 additions ===

	// Long is the cobra.Command.Long description (multi-line).
	Long string `json:"long,omitempty"`
	// Retryable reports whether the command is safely re-runnable
	// (kit/retryable annotation). Explicit false carries information
	// so agents don't have to special-case missing-field semantics.
	Retryable bool `json:"retryable"`
	// OutputSchema carries the adopter-declared JSON Schema for the
	// command's structured output (kit/output-schema). Embedded as
	// raw bytes so the manifest can be re-serialized without losing
	// canonical formatting.
	OutputSchema RawJSON `json:"output_schema,omitempty"`
	// OutputSchemaVersion is the adopter-declared MAJOR.MINOR
	// version paired with OutputSchema.
	OutputSchemaVersion string `json:"output_schema_version,omitempty"`
	// Examples lists agent-facing examples declared via SetExamples.
	Examples []ManifestExample `json:"examples,omitempty"`
	// NextSteps lists post-invocation chaining suggestions declared
	// via SetNextSteps.
	NextSteps []ManifestNextStep `json:"next_steps,omitempty"`
	// TopLevelVerb reports kit/top-level-verb (depth-1 leaf opt-in).
	TopLevelVerb bool `json:"top_level_verb,omitempty"`
	// Hierarchical reports kit/hierarchical (intermediate node
	// annotation supporting depth >= 3 trees).
	Hierarchical bool `json:"hierarchical,omitempty"`
	// Passthrough reports kit/passthrough (forwarded `-- argv`).
	Passthrough bool `json:"passthrough,omitempty"`
	// DestructiveTokenRequired reports kit/destructive-token=required.
	DestructiveTokenRequired bool `json:"destructive_token_required,omitempty"`
	// DryRunSupported is derived: side-effect ∈ write*|destructive*
	// AND not annotated kit/dry-run=opted-out. Explicit field so
	// agents don't have to mirror the derivation rule.
	DryRunSupported bool `json:"dry_run_supported"`
	// DryRunRationale carries the kit/dry-run-rationale string when
	// the adopter has explicitly opted out.
	DryRunRationale string `json:"dry_run_rationale,omitempty"`
	// Reserved reports whether the leaf's depth-1 ancestor is a
	// kit-reserved subcommand (set populated by Root.reservedSnapshot).
	Reserved bool `json:"reserved,omitempty"`
	// Hidden reports cobra.Command.Hidden (the leaf isn't surfaced
	// in --help by default).
	Hidden bool `json:"hidden,omitempty"`
}

// RawJSON is a json.RawMessage alias that keeps the toolspec package
// dependency-free of encoding/json at the type level. Implementations
// project into it via json.RawMessage(...) cast.
type RawJSON []byte

// MarshalJSON makes RawJSON forward to its underlying bytes verbatim
// (mirroring json.RawMessage semantics) so the embedded schema
// re-serializes losslessly.
func (r RawJSON) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return []byte(r), nil
}

// UnmarshalJSON stores the input bytes verbatim, also mirroring
// json.RawMessage.
func (r *RawJSON) UnmarshalJSON(b []byte) error {
	if r == nil {
		return nil
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	*r = cp
	return nil
}

// ManifestExample is one entry in the kit/examples annotation
// projection. Mirrors cli.Example (kept here to avoid pulling the
// cli package into toolspec's import graph).
type ManifestExample struct {
	Title   string `json:"title"`
	Command string `json:"command"`
	Output  string `json:"output,omitempty"`
}

// ManifestNextStep is one entry in the kit/next-steps annotation
// projection. Mirrors cli.NextStep.
type ManifestNextStep struct {
	When    string `json:"when,omitempty"`
	Suggest string `json:"suggest"`
	Reason  string `json:"reason,omitempty"`
}

// ManifestArg is a positional argument declaration.
type ManifestArg struct {
	Name     string `json:"name"`
	Required bool   `json:"required,omitempty"`
}

// ManifestFlag is a flag declaration in the manifest.
type ManifestFlag struct {
	Name         string `json:"name"`
	Short        string `json:"short,omitempty"`
	Type         string `json:"type,omitempty"`
	Description  string `json:"description,omitempty"`
	Default      string `json:"default,omitempty"`
	SinceVersion string `json:"since_version,omitempty"`
}

// FindCommand walks the command tree breadth-first and returns the
// shallowest Command whose Name matches name, or nil if not found.
func (ts *ToolSpec) FindCommand(name string) *Command {
	queue := make([]*Command, 0, len(ts.Commands))
	for i := range ts.Commands {
		queue = append(queue, &ts.Commands[i])
	}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c.Name == name {
			return c
		}
		for i := range c.Children {
			queue = append(queue, &c.Children[i])
		}
	}
	return nil
}
