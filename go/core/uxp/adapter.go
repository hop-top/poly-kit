package uxp

// Adapter abstracts interaction with a specific coding-assistant CLI.
type Adapter interface {
	// CLI returns the canonical name of the CLI this adapter handles.
	CLI() CLIName

	// Detect probes the local environment for the CLI's presence.
	Detect() (*DetectResult, error)

	// Capabilities returns the feature-support map for this CLI.
	Capabilities() CapabilityMap
}

// DetectResult captures what Detect found about a CLI installation.
type DetectResult struct {
	Installed   bool     `json:"installed"`
	Version     string   `json:"version,omitempty"`
	BinaryPath  string   `json:"binary_path,omitempty"`
	ConfigPaths []string `json:"config_paths,omitempty"`
	Errors      []string `json:"errors,omitempty"`
}

// CapabilityMap reports which feature dimensions a CLI supports.
type CapabilityMap interface {
	// Supports returns true if the CLI has native or workaround
	// support for the named dimension.
	Supports(dimension string) bool

	// Coverage returns all known dimensions and their support level.
	Coverage() map[string]Support
}

// Support describes the level of feature support.
type Support int

const (
	// Native means the CLI supports the feature out of the box.
	Native Support = iota
	// Workaround means the feature works via a non-standard path.
	Workaround
	// Missing means the CLI lacks the feature entirely.
	Missing
)

// String returns a human-readable label for the support level.
func (s Support) String() string {
	switch s {
	case Native:
		return "native"
	case Workaround:
		return "workaround"
	case Missing:
		return "missing"
	default:
		return "unknown"
	}
}
