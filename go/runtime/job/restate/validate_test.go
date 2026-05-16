package restate

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
				"endpoint": "http://localhost:8080",
			},
		},
		{
			name:    "missing endpoint",
			cfg:     map[string]string{},
			wantErr: true,
		},
		{
			name: "invalid endpoint",
			cfg: map[string]string{
				"endpoint": "not a url",
			},
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
