package etcd

import (
	"errors"
	"time"
)

var (
	//ErrEmptyEndpoints occurs when the endpoints in the config is empty.
	ErrEmptyEndpoints = errors.New("length of etcd endpoints must be greater than 0")
)

// Config is the configuration for creating a Client instance.
type Config struct {
	Endpoints      []string      `json:"Endpoints"`
	DialTimeoutSec time.Duration `json:"DialTimeoutSec"`
	Username       string        `json:"Username"`
	Password       string        `json:"Password"`

	LockLeaseTimeSec int `json:"LockLeaseTimeSec"`
}

// Validate validates this config.
func (c *Config) Validate() error {
	if len(c.Endpoints) == 0 {
		return ErrEmptyEndpoints
	}

	return nil
}
