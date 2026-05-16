package uxp

import "testing"

func TestDeriveKey(t *testing.T) {
	const cwd = "/Users/jadb/.w/ideacrafterslabs/uhp"

	tests := []struct {
		name     string
		strategy ProjectKeyStrategy
		want     string
	}{
		{
			name:     "SlashToDash",
			strategy: SlashToDash,
			want:     "Users-jadb-.w-ideacrafterslabs-uhp",
		},
		{
			name:     "SHA1",
			strategy: SHA1,
			want:     "dc46c5c89af35341f834cfb93af103343ac31158",
		},
		{
			name:     "SHA256",
			strategy: SHA256,
			want:     "b2e9434903c3d2059d2c5c0d60467de55a64165e331ac29fb235ee4a0d7b64b9",
		},
		{
			name:     "MD5",
			strategy: MD5,
			want:     "04c0ec06e2a2388290e4bffa13f97131",
		},
		{
			name:     "BasenameAlias",
			strategy: BasenameAlias,
			want:     "uhp",
		},
		{
			name:     "Embedded",
			strategy: Embedded,
			want:     cwd,
		},
		{
			name:     "None",
			strategy: None,
			want:     cwd,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveKey(cwd, tt.strategy)
			if got != tt.want {
				t.Errorf("DeriveKey(%q, %s) = %q, want %q", cwd, tt.strategy, got, tt.want)
			}
		})
	}
}

// Regression: SlashToDash must not panic on empty or root-only input.
func TestDeriveKey_SlashToDashEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{"empty string", "", ""},
		{"root only", "/", ""},
		{"single segment", "foo", "foo"},
		{"trailing slash", "/Users/jadb/", "Users-jadb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("DeriveKey(%q, SlashToDash) panicked: %v", tt.cwd, r)
				}
			}()
			got := DeriveKey(tt.cwd, SlashToDash)
			if got != tt.want {
				t.Errorf("DeriveKey(%q, SlashToDash) = %q, want %q", tt.cwd, got, tt.want)
			}
		})
	}
}

func TestProjectKeyStrategyString(t *testing.T) {
	tests := []struct {
		strategy ProjectKeyStrategy
		want     string
	}{
		{SlashToDash, "slash-to-dash"},
		{SHA1, "sha1"},
		{SHA256, "sha256"},
		{MD5, "md5"},
		{BasenameAlias, "basename-alias"},
		{Embedded, "embedded"},
		{None, "none"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.strategy.String()
			if got != tt.want {
				t.Errorf("ProjectKeyStrategy.String() = %q, want %q", got, tt.want)
			}
		})
	}
}
