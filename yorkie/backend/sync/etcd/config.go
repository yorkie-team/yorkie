package etcd

import (
	"errors"
	"fmt"
	"time"
)

var (
	//ErrEmptyEndPoints occurs when the endpoints in the config is empty.
	ErrEmptyEndPoints = errors.New("length of etcd endpoints must be greater than 0")
	// ErrEmptyUserName occurs when the username is empty.
	ErrEmptyUserName = errors.New("etcd username cannot be nil or empty")
	// ErrEmptyPassword occurs when the password is empty.
	ErrEmptyPassword = errors.New("etcd password cannot be nil or empty")
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
		return fmt.Errorf("%w", ErrEmptyEndPoints)
	}

	if len(c.Username) == 0 {
		return fmt.Errorf("%w", ErrEmptyUserName)
	}

	if len(c.Password) == 0 {
		return fmt.Errorf("%w", ErrEmptyPassword)
	}

	return nil
}
