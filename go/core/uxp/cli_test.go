package uxp

import (
	"encoding/json"
	"testing"
)

func TestCLINameConstants(t *testing.T) {
	tests := []struct {
		name CLIName
		want string
	}{
		{CLIClaude, "claude"},
		{CLIGemini, "gemini"},
		{CLICodex, "codex"},
		{CLIOpenCode, "opencode"},
		{CLICopilot, "copilot"},
		{CLICursorAgent, "cursor-agent"},
		{CLIAmp, "amp"},
		{CLIKimi, "kimi"},
		{CLIQwen, "qwen"},
		{CLIVibe, "vibe"},
		{CLIAntigravity, "antigravity"},
		{CLITabnine, "tabnine"},
		{CLIWindsurf, "windsurf"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if tt.name != tt.want {
				t.Errorf("CLIName = %q, want %q", tt.name, tt.want)
			}
		})
	}
}

func TestCLINameCount(t *testing.T) {
	all := []CLIName{
		CLIClaude, CLIGemini, CLICodex, CLIOpenCode, CLICopilot,
		CLICursorAgent, CLIAmp, CLIKimi, CLIQwen, CLIVibe,
		CLIAntigravity, CLITabnine, CLIWindsurf,
	}
	if got := len(all); got != 13 {
		t.Errorf("expected 13 CLI names, got %d", got)
	}
}

func TestCLIInfo_JSONRoundTrip(t *testing.T) {
	info := CLIInfo{
		Name:        CLIClaude,
		BinaryNames: []string{"claude"},
		StoreRootPaths: StorePaths{
			Config: "~/.config/claude",
			Data:   "~/.local/share/claude",
			Cache:  "~/.cache/claude",
			State:  "~/.local/state/claude",
		},
		ConfigFilePatterns: []string{"*.json", "*.toml"},
		ProjectKeyStrategy: SlashToDash,
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got CLIInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Name != info.Name {
		t.Errorf("Name = %q, want %q", got.Name, info.Name)
	}
	if len(got.BinaryNames) != 1 || got.BinaryNames[0] != "claude" {
		t.Errorf("BinaryNames = %v, want [claude]", got.BinaryNames)
	}
	if got.StoreRootPaths.Config != info.StoreRootPaths.Config {
		t.Errorf("StoreRootPaths.Config = %q, want %q",
			got.StoreRootPaths.Config, info.StoreRootPaths.Config)
	}
	if got.StoreRootPaths.Data != info.StoreRootPaths.Data {
		t.Errorf("StoreRootPaths.Data = %q, want %q",
			got.StoreRootPaths.Data, info.StoreRootPaths.Data)
	}
	if got.StoreRootPaths.Cache != info.StoreRootPaths.Cache {
		t.Errorf("StoreRootPaths.Cache = %q, want %q",
			got.StoreRootPaths.Cache, info.StoreRootPaths.Cache)
	}
	if got.StoreRootPaths.State != info.StoreRootPaths.State {
		t.Errorf("StoreRootPaths.State = %q, want %q",
			got.StoreRootPaths.State, info.StoreRootPaths.State)
	}
	if len(got.ConfigFilePatterns) != 2 {
		t.Fatalf("ConfigFilePatterns len = %d, want 2",
			len(got.ConfigFilePatterns))
	}
	if got.ProjectKeyStrategy != info.ProjectKeyStrategy {
		t.Errorf("ProjectKeyStrategy = %q, want %q",
			got.ProjectKeyStrategy, info.ProjectKeyStrategy)
	}
}

func TestStorePaths_JSONRoundTrip(t *testing.T) {
	sp := StorePaths{
		Config: "/a",
		Data:   "/b",
		Cache:  "/c",
		State:  "/d",
	}

	data, err := json.Marshal(sp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got StorePaths
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got != sp {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, sp)
	}
}
