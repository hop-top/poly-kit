package uri

import (
	"hop.top/uri/handle/generate"
	"hop.top/uri/scheme"
)

const (
	formatText  = "text"
	formatJSON  = "json"
	formatYAML  = "yaml"
	formatTable = "table"
	formatLines = "lines"
)

// Config configures the shared URI command tree.
type Config struct {
	// CommandName is the mounted command name. Empty defaults to "uri".
	CommandName string
	// Policy controls namespace segments, vanity aliases, and action routes.
	Policy scheme.Policy
	// Types adds parser/completer registrations used by `uri complete`.
	Types []scheme.TypeRegistration
	// Handler supplies defaults for handler id/generate flags.
	Handler HandlerConfig
	// DisabledCommands disables leaves by key: parse, resolve, complete,
	// handler.id, handler.generate, completion.
	DisabledCommands []string
}

// HandlerConfig supplies handler artifact defaults for an app.
type HandlerConfig struct {
	Vendor      string
	App         string
	Instance    string
	Language    generate.Language
	Scheme      string
	Version     string
	Channel     string
	AppPath     string
	DisplayName string
}

type parseFlags struct {
	PolicyFile    string
	Strict        bool
	JSONAmbiguity bool
	Format        string
}

type completeFlags struct {
	Type       string
	Prefix     string
	Input      string
	PolicyFile string
	Format     string
}

type handlerFlags struct {
	Vendor      string
	App         string
	Instance    string
	Language    string
	Scheme      string
	Version     string
	Channel     string
	AppPath     string
	DisplayName string
	Platform    string
	Output      string
	Format      string
}

type uriRow struct {
	Scheme    string `table:"SCHEME" json:"scheme" yaml:"scheme"`
	Namespace string `table:"NAMESPACE" json:"namespace" yaml:"namespace"`
	ID        string `table:"ID" json:"id" yaml:"id"`
	Query     string `table:"QUERY" json:"query,omitempty" yaml:"query,omitempty"`
	Fragment  string `table:"FRAGMENT" json:"fragment,omitempty" yaml:"fragment,omitempty"`
	Original  string `table:"ORIGINAL" json:"original,omitempty" yaml:"original,omitempty"`
	Action    string `table:"ACTION" json:"action,omitempty" yaml:"action,omitempty"`
}

type actionRow struct {
	Action  string   `table:"ACTION" json:"action" yaml:"action"`
	Command string   `table:"COMMAND" json:"command" yaml:"command"`
	Args    []string `table:"ARGS" json:"args" yaml:"args"`
}

type completionRow struct {
	Type  string `table:"TYPE" json:"type" yaml:"type"`
	Value string `table:"VALUE" json:"value" yaml:"value"`
}

type vanityRow struct {
	From     string `table:"FROM" json:"from" yaml:"from"`
	To       string `table:"TO" json:"to" yaml:"to"`
	Distance int    `table:"DISTANCE" json:"distance" yaml:"distance"`
}

type handlerIDRow struct {
	HandlerID string `table:"HANDLER ID" json:"handler_id" yaml:"handler_id"`
}

type handlerGenerateRow struct {
	Platform  string `table:"PLATFORM" json:"platform" yaml:"platform"`
	HandlerID string `table:"HANDLER ID" json:"handler_id" yaml:"handler_id"`
	Output    string `table:"OUTPUT" json:"output,omitempty" yaml:"output,omitempty"`
	Snippet   string `table:"-" json:"snippet" yaml:"snippet"`
}
