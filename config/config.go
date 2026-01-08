package config

import (
	"github.com/caarlos0/env/v6"
)

type Config struct {
	Proxy Proxy `envPrefix:"PROXY_"`
}

type Proxy struct {
	// Restricted webhook access.
	// means that proxy will listen port only on Nodes running webhook pod.
	Restricted bool `env:"RESTRICTED"`
	// AllowedSrcCIDRs tells controller to create network policy
	// with CIDRs allowed. Will be handled only if Restricted set to true.
	AllowedSrcCIDRs []string `env:"ALLOWED_CIDRS"`
}

// New creates a new Config.
func New() (*Config, error) {
	cfg := &Config{}

	if err := env.Parse(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
