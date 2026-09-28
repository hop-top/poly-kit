package uxp

import "testing"

func TestDeriveKey(t *testing.T) {
	const cwd = "/Users/alice/src/uhp"

	tests := []struct {
		name     string
		strategy ProjectKeyStrategy
		want     string
	}{
		{
			name:     "SlashToDash",
			strategy: SlashToDash,
			want:     "Users-alice-src-uhp",
		},
		{
			name:     "SHA1",
			strategy: SHA1,
			want:     "84e4bbeecedfc5721695045d99448b9ed90fa89c",
		},
		{
			name:     "SHA256",
			strategy: SHA256,
			want:     "8f9a157a38322a312d26f0b08f3ba69cdf82f673d1ad96e54fd88a0cb06de6f3",
		},
		{
			name:     "MD5",
			strategy: MD5,
			want:     "6dea3d72ee66901f03a84cf93c065544",
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
		{"trailing slash", "/Users/alice/", "Users-alice"},
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
