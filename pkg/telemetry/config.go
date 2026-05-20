package telemetry

import (
	"os"
	"sync"
)

// Default endpoint and token are intended to be injected at build time via
// -ldflags. Stock builds leave them empty, which keeps emission effectively
// off (empty endpoint short-circuits in the transport) until the collector is
// stood up.
var (
	DefaultEndpoint string
	DefaultToken    string
)

// Config is the resolved telemetry configuration. Constructed once via
// LoadConfig() at startup.
type Config struct {
	Enabled  bool
	Endpoint string
	Token    string
}

const (
	envOn       = "SKILLS_OCI_TELEMETRY"
	envEndpoint = "SKILLS_OCI_TELEMETRY_ENDPOINT"
	envToken    = "SKILLS_OCI_TELEMETRY_TOKEN"

	offValue = "off"
)

var (
	cachedConfig Config
	cachedOnce   sync.Once
	// resetForTest is true only when tests have asked LoadConfig to bypass
	// the once-cache; never flipped in production code paths.
	resetForTest bool
)

// LoadConfig reads the three SKILLS_OCI_TELEMETRY* env vars once and returns
// the resolved configuration. Subsequent calls return the cached value.
func LoadConfig() Config {
	if resetForTest {
		return resolveConfig()
	}
	cachedOnce.Do(func() {
		cachedConfig = resolveConfig()
	})
	return cachedConfig
}

func resolveConfig() Config {
	endpoint := os.Getenv(envEndpoint)
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	token := os.Getenv(envToken)
	if token == "" {
		token = DefaultToken
	}
	enabled := os.Getenv(envOn) != offValue
	return Config{
		Enabled:  enabled,
		Endpoint: endpoint,
		Token:    token,
	}
}
