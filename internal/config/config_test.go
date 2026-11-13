package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setValidEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"PORT", "APP_ENV", "HTTPS_PORT", "TLS_CERT_FILE", "TLS_KEY_FILE"} {
		t.Setenv(k, "")
	}
	t.Setenv("DATABASE_URL", "postgres://app@localhost/crm")
	t.Setenv("MIGRATION_DATABASE_URL", "postgres://owner@localhost/crm")
	t.Setenv("JWT_SECRET", strings.Repeat("x", 32))
	t.Setenv("FRONTEND_ORIGIN", "http://localhost:3000")
}

func TestLoad_ValidUsesDefaults(t *testing.T) {
	setValidEnv(t)

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, "8080", cfg.Port)
	assert.Equal(t, EnvDevelopment, cfg.AppEnv)
}

func TestLoad_Invalid(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		wantErrs []string
	}{
		{
			name:     "missing several required vars reports all of them",
			env:      map[string]string{"DATABASE_URL": "", "JWT_SECRET": ""},
			wantErrs: []string{"DATABASE_URL is required", "JWT_SECRET is required"},
		},
		{
			name:     "short jwt secret",
			env:      map[string]string{"JWT_SECRET": "short"},
			wantErrs: []string{"JWT_SECRET must be at least 32 bytes"},
		},
		{
			name:     "tls cert without key",
			env:      map[string]string{"TLS_CERT_FILE": "certs/localhost.pem"},
			wantErrs: []string{"TLS_CERT_FILE and TLS_KEY_FILE must be set together"},
		},
		{
			name: "https port equals http port",
			env: map[string]string{"TLS_CERT_FILE": "c.pem", "TLS_KEY_FILE": "c.key",
				"PORT": "8080", "HTTPS_PORT": "8080"},
			wantErrs: []string{"HTTPS_PORT must differ from PORT"},
		},
		{
			name:     "unknown app env",
			env:      map[string]string{"APP_ENV": "staging"},
			wantErrs: []string{`APP_ENV must be one of`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			_, err := Load()

			require.Error(t, err)
			for _, want := range tt.wantErrs {
				assert.Contains(t, err.Error(), want)
			}
			assert.NotContains(t, err.Error(), "short", "secret value must not leak")
		})
	}
}
