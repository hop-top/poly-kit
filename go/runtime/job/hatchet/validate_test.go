package hatchet

import (
	"testing"
)

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]string
		wantErr bool
	}{
		{
			name: "valid",
			cfg: map[string]string{
				"server_url": "http://localhost:8080",
				"api_token":  "tok_abc123",
			},
		},
		{
			name: "missing server_url",
			cfg: map[string]string{
				"api_token": "tok_abc123",
			},
			wantErr: true,
		},
		{
			name: "invalid server_url",
			cfg: map[string]string{
				"server_url": "not a url",
				"api_token":  "tok_abc123",
			},
			wantErr: true,
		},
		{
			name: "missing api_token",
			cfg: map[string]string{
				"server_url": "http://localhost:8080",
			},
			wantErr: true,
		},
		{
			name:    "empty config",
			cfg:     map[string]string{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v",
					err, tt.wantErr)
			}
		})
	}
}
