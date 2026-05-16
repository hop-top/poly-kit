package temporal

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
				"server_address": "localhost:7233",
				"namespace":      "default",
			},
		},
		{
			name: "missing server_address",
			cfg: map[string]string{
				"namespace": "default",
			},
			wantErr: true,
		},
		{
			name: "server_address without port",
			cfg: map[string]string{
				"server_address": "localhost",
				"namespace":      "default",
			},
			wantErr: true,
		},
		{
			name: "missing namespace",
			cfg: map[string]string{
				"server_address": "localhost:7233",
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
