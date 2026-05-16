package uxp

import (
	"slices"
	"sync"
)

// CLIRegistry is a read-only map of CLIName to CLIInfo.
type CLIRegistry struct {
	m map[CLIName]CLIInfo
}

var (
	defaultOnce sync.Once
	defaultReg  *CLIRegistry
)

// DefaultRegistry returns the singleton registry pre-populated with all
// known AI coding CLIs.
func DefaultRegistry() *CLIRegistry {
	defaultOnce.Do(func() {
		defaultReg = &CLIRegistry{m: knownCLIs()}
	})
	return defaultReg
}

// Get returns the CLIInfo for name, or false if not found.
func (r *CLIRegistry) Get(name CLIName) (CLIInfo, bool) {
	info, ok := r.m[name]
	return info, ok
}

// All returns every registered CLIInfo in sorted order.
func (r *CLIRegistry) All() []CLIInfo {
	names := r.Names()
	out := make([]CLIInfo, len(names))
	for i, n := range names {
		out[i] = r.m[n]
	}
	return out
}

// Names returns all registered CLI names, sorted.
func (r *CLIRegistry) Names() []CLIName {
	names := make([]CLIName, 0, len(r.m))
	for n := range r.m {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func knownCLIs() map[CLIName]CLIInfo {
	return map[CLIName]CLIInfo{
		CLIClaude: {
			Name: CLIClaude, BinaryNames: []string{"claude"},
			StoreRootPaths: StorePaths{Data: "~/.claude/projects/"},
		},
		CLIGemini: {
			Name: CLIGemini, BinaryNames: []string{"gemini"},
			StoreRootPaths: StorePaths{Data: "~/.gemini/history/"},
		},
		CLICodex: {
			Name: CLICodex, BinaryNames: []string{"codex"},
			StoreRootPaths: StorePaths{Data: "~/.codex/sessions/"},
		},
		CLIOpenCode: {
			Name: CLIOpenCode, BinaryNames: []string{"opencode"},
			StoreRootPaths: StorePaths{Data: "~/.local/share/opencode/"},
		},
		CLICopilot: {
			Name: CLICopilot, BinaryNames: []string{"copilot"},
			StoreRootPaths: StorePaths{Data: "~/.copilot/state/"},
		},
		CLICursorAgent: {
			Name: CLICursorAgent, BinaryNames: []string{"cursor-agent"},
			StoreRootPaths: StorePaths{Data: "~/.cursor/chats/"},
		},
		CLIAmp: {
			Name: CLIAmp, BinaryNames: []string{"amp"},
			StoreRootPaths: StorePaths{Data: "~/.amp/"},
		},
		CLIKimi: {
			Name: CLIKimi, BinaryNames: []string{"kimi"},
			StoreRootPaths: StorePaths{Data: "~/.kimi/sessions/"},
		},
		CLIQwen: {
			Name: CLIQwen, BinaryNames: []string{"qwen"},
			StoreRootPaths: StorePaths{Data: "~/.qwen/tmp/"},
		},
		CLIVibe: {
			Name: CLIVibe, BinaryNames: []string{"vibe"},
			StoreRootPaths: StorePaths{Data: "~/.vibe/logs/session/"},
		},
		CLIGoose: {
			Name: CLIGoose, BinaryNames: []string{"goose"},
			StoreRootPaths: StorePaths{Data: "~/.local/share/goose/sessions/"},
		},
		CLICrush: {
			Name: CLICrush, BinaryNames: []string{"crush"},
			StoreRootPaths: StorePaths{Data: "~/.local/share/crush/"},
		},
		CLIAntigravity: {
			Name: CLIAntigravity, BinaryNames: []string{"agy"},
			StoreRootPaths: StorePaths{
				Data: "~/Library/Application Support/Antigravity/",
			},
		},
		CLITabnine: {
			Name: CLITabnine, BinaryNames: []string{"tabnine"},
			StoreRootPaths: StorePaths{Data: "~/.tabnine/"},
		},
		CLIWindsurf: {
			Name: CLIWindsurf, BinaryNames: []string{"windsurf"},
			StoreRootPaths: StorePaths{Data: "~/.windsurf/"},
		},
	}
}
