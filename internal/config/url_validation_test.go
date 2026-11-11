package config

import (
	"strings"
	"testing"
)

func TestValidateAPIBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid http URL",
			url:     "http://localhost:8080",
			wantErr: false,
		},
		{
			name:    "valid https URL",
			url:     "https://api.example.com",
			wantErr: false,
		},
		{
			name:    "valid https URL with path",
			url:     "https://api.example.com/v1",
			wantErr: false,
		},
		{
			name:    "missing scheme - localhost with port",
			url:     "localhost:8080",
			wantErr: true,
			errMsg:  "must start with http:// or https://",
		},
		{
			name:    "missing scheme - bare hostname",
			url:     "localhost",
			wantErr: true,
			errMsg:  "must start with http:// or https://",
		},
		{
			name:    "garbage text",
			url:     "not a url at all",
			wantErr: true,
			errMsg:  "must start with http:// or https://",
		},
		{
			name:    "ftp scheme",
			url:     "ftp://files.example.com",
			wantErr: true,
			errMsg:  "must start with http:// or https://",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAPIBaseURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateAPIBaseURL(%q) = nil, want error", tt.url)
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error message %q does not contain %q", err.Error(), tt.errMsg)
				}
				if !strings.Contains(err.Error(), tt.url) {
					t.Errorf("error message %q does not contain the invalid URL %q", err.Error(), tt.url)
				}
			} else {
				if err != nil {
					t.Fatalf("validateAPIBaseURL(%q) = %v, want nil", tt.url, err)
				}
			}
		})
	}
}
