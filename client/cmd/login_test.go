package cmd

import (
	"testing"
)

func TestLoginTokenFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{"empty token", "", true},
		{"valid token", "xapi-abc123", false},
		{"valid jwt-like token", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLoginToken(tt.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateLoginToken(%q) error = %v, wantErr %v", tt.token, err, tt.wantErr)
			}
		})
	}
}
