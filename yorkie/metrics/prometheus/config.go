package prometheus

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidMetricPort occurs when the port in the config is invalid.
	ErrInvalidMetricPort = errors.New("invalid port number for metric server")
)

// Config is the configuration for creating a Server instance.
type Config struct {
	Port int
}

// Validate validates the port number.
func (c *Config) Validate() error {
	if c.Port < 1 || 65535 < c.Port {
		return fmt.Errorf("must be between 1 and 65535, given %d: %w", c.Port, ErrInvalidMetricPort)
	}

	return nil
}
