package uxp

// CLIName identifies a supported coding-assistant CLI.
type CLIName = string

// Well-known CLIName constants for the 15 supported coding-assistant CLIs.
const (
	CLIClaude      CLIName = "claude"
	CLIGemini      CLIName = "gemini"
	CLICodex       CLIName = "codex"
	CLIOpenCode    CLIName = "opencode"
	CLICopilot     CLIName = "copilot"
	CLICursorAgent CLIName = "cursor-agent"
	CLIAmp         CLIName = "amp"
	CLIKimi        CLIName = "kimi"
	CLIQwen        CLIName = "qwen"
	CLIVibe        CLIName = "vibe"
	CLIGoose       CLIName = "goose"
	CLICrush       CLIName = "crush"
	CLIAntigravity CLIName = "antigravity"
	CLITabnine     CLIName = "tabnine"
	CLIWindsurf    CLIName = "windsurf"
)

// StorePaths holds XDG-style root paths for a CLI's persistent stores.
type StorePaths struct {
	Config string `json:"config,omitempty"`
	Data   string `json:"data,omitempty"`
	Cache  string `json:"cache,omitempty"`
	State  string `json:"state,omitempty"`
}

// CLIInfo describes a coding-assistant CLI tool.
type CLIInfo struct {
	Name               CLIName            `json:"name"`
	BinaryNames        []string           `json:"binary_names,omitempty"`
	StoreRootPaths     StorePaths         `json:"store_root_paths"`
	ConfigFilePatterns []string           `json:"config_file_patterns,omitempty"`
	ProjectKeyStrategy ProjectKeyStrategy `json:"project_key_strategy,omitempty"`
}
