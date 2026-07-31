/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package rpc

import (
	"fmt"
	"os"
	"time"

	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
)

var (
	// ErrInvalidRPCPort occurs when the port in the config is invalid.
	ErrInvalidRPCPort = errors.InvalidArgument("invalid port number for RPC server")

	// ErrInvalidCertFile occurs when the certificate file is invalid.
	ErrInvalidCertFile = errors.InvalidArgument("invalid cert file for RPC server")
	// ErrInvalidKeyFile occurs when the key file is invalid.
	ErrInvalidKeyFile = errors.InvalidArgument("invalid key file for RPC server")
)

// Config is the configuration for creating a Server instance.
type Config struct {
	// Port is the port number for the RPC server.
	Port int `yaml:"Port"`

	// CertFile is the path to the certificate file.
	CertFile string `yaml:"CertFile"`

	// KeyFile is the path to the key file.
	KeyFile string `yaml:"KeyFile"`

	// ReadHeaderTimeout is the maximum duration for reading request headers
	// (Slowloris protection). Default is "5s".
	ReadHeaderTimeout string `yaml:"ReadHeaderTimeout"`

	// IdleTimeout is the maximum duration to wait for the next request on
	// idle keep-alive connections. Default is "2m".
	IdleTimeout string `yaml:"IdleTimeout"`

	// Auth is the configuration for authentication.
	Auth auth.Config `yaml:"Auth"`
}

// Validate validates the port number and the files for certification.
func (c *Config) Validate() error {
	if c.Port < 1 || 65535 < c.Port {
		return fmt.Errorf("must be between 1 and 65535, given %d: %w", c.Port, ErrInvalidRPCPort)
	}

	// when specific cert or key file are configured
	if c.CertFile != "" {
		if _, err := os.Stat(c.CertFile); err != nil {
			return fmt.Errorf("%s: %w", c.CertFile, ErrInvalidCertFile)
		}
	}

	if c.KeyFile != "" {
		if _, err := os.Stat(c.KeyFile); err != nil {
			return fmt.Errorf("%s: %w", c.KeyFile, ErrInvalidKeyFile)
		}
	}

	if c.ReadHeaderTimeout != "" {
		if _, err := time.ParseDuration(c.ReadHeaderTimeout); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--rpc-read-header-timeout" flag: %w`,
				c.ReadHeaderTimeout,
				err,
			)
		}
	}

	if c.IdleTimeout != "" {
		if _, err := time.ParseDuration(c.IdleTimeout); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--rpc-idle-timeout" flag: %w`,
				c.IdleTimeout,
				err,
			)
		}
	}

	return nil
}

// ParseReadHeaderTimeout returns the timeout for reading request headers.
func (c *Config) ParseReadHeaderTimeout() time.Duration {
	result, err := time.ParseDuration(c.ReadHeaderTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse read header timeout: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParseIdleTimeout returns the timeout for idle keep-alive connections.
func (c *Config) ParseIdleTimeout() time.Duration {
	result, err := time.ParseDuration(c.IdleTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse idle timeout: %v\n", err)
		os.Exit(1)
	}

	return result
}
