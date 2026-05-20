package telemetry

import (
	"testing"
)

// useFreshConfig forces LoadConfig to bypass its sync.Once cache for the
// duration of a single test. Restores the production-path behavior on cleanup.
func useFreshConfig(t *testing.T) {
	t.Helper()
	prev := resetForTest
	resetForTest = true
	t.Cleanup(func() { resetForTest = prev })
}

// withLdflagDefaults swaps the package-level Default* vars and restores them.
func withLdflagDefaults(t *testing.T, endpoint, token string) {
	t.Helper()
	prevE, prevT := DefaultEndpoint, DefaultToken
	DefaultEndpoint, DefaultToken = endpoint, token
	t.Cleanup(func() { DefaultEndpoint, DefaultToken = prevE, prevT })
}

// setEnvOr sets the env var to the given value; the empty string here is
// equivalent to "unset" for os.Getenv-based reads.
func setEnvOr(t *testing.T, key, val string) {
	t.Helper()
	t.Setenv(key, val)
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	cases := []struct {
		name        string
		envOn       string
		envEndpoint string
		envToken    string
		ldEndpoint  string
		ldToken     string
		wantEnabled bool
		wantEnd     string
		wantTok     string
	}{
		{
			name:        "unset env, empty ldflag defaults",
			wantEnabled: true,
		},
		{
			name:        "SKILLS_OCI_TELEMETRY=off disables emission",
			envOn:       "off",
			wantEnabled: false,
		},
		{
			name:        "SKILLS_OCI_TELEMETRY=on leaves enabled true",
			envOn:       "on",
			wantEnabled: true,
		},
		{
			name:        "arbitrary value stays on (only 'off' is off)",
			envOn:       "yes",
			wantEnabled: true,
		},
		{
			name:        "empty SKILLS_OCI_TELEMETRY is treated as on",
			envOn:       "",
			wantEnabled: true,
		},
		{
			name:        "env endpoint and token override ldflag defaults",
			envEndpoint: "https://env",
			envToken:    "env-tok",
			ldEndpoint:  "https://built-in",
			ldToken:     "builtin-tok",
			wantEnabled: true,
			wantEnd:     "https://env",
			wantTok:     "env-tok",
		},
		{
			name:        "empty env endpoint falls back to ldflag default",
			envEndpoint: "",
			ldEndpoint:  "https://built-in",
			wantEnabled: true,
			wantEnd:     "https://built-in",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useFreshConfig(t)
			withLdflagDefaults(t, tc.ldEndpoint, tc.ldToken)
			setEnvOr(t, envOn, tc.envOn)
			setEnvOr(t, envEndpoint, tc.envEndpoint)
			setEnvOr(t, envToken, tc.envToken)

			cfg := LoadConfig()
			if cfg.Enabled != tc.wantEnabled {
				t.Errorf("Enabled = %v, want %v", cfg.Enabled, tc.wantEnabled)
			}
			if cfg.Endpoint != tc.wantEnd {
				t.Errorf("Endpoint = %q, want %q", cfg.Endpoint, tc.wantEnd)
			}
			if cfg.Token != tc.wantTok {
				t.Errorf("Token = %q, want %q", cfg.Token, tc.wantTok)
			}
		})
	}
}

func TestConfig_LdflagFallback(t *testing.T) {
	useFreshConfig(t)
	withLdflagDefaults(t, "http://built-in", "builtin")
	setEnvOr(t, envEndpoint, "")
	setEnvOr(t, envToken, "")

	cfg := LoadConfig()
	if cfg.Endpoint != "http://built-in" {
		t.Errorf("Endpoint = %q, want http://built-in", cfg.Endpoint)
	}
	if cfg.Token != "builtin" {
		t.Errorf("Token = %q, want builtin", cfg.Token)
	}
}
