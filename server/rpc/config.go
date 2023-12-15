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
	"errors"
	"fmt"
	"os"
)

var (
	// ErrInvalidRPCPort occurs when the port in the config is invalid.
	ErrInvalidRPCPort = errors.New("invalid port number for RPC server")
	// ErrInvalidCertFile occurs when the certificate file is invalid.
	ErrInvalidCertFile = errors.New("invalid cert file for RPC server")
	// ErrInvalidKeyFile occurs when the key file is invalid.
	ErrInvalidKeyFile = errors.New("invalid key file for RPC server")
)

// Config is the configuration for creating a Server instance.
type Config struct {
	// Port is the port number for the RPC server.
	Port int `yaml:"Port"`

	// CertFile is the path to the certificate file.
	CertFile string `yaml:"CertFile"`

	// KeyFile is the path to the key file.
	KeyFile string `yaml:"KeyFile"`
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

	return nil
}
