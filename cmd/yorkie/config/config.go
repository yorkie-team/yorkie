/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

// Package config provides the configuration for Admin server.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// ensureYorkieDir ensures that the directory of Yorkie exists.
func ensureYorkieDir() (string, error) {
	yorkieDir := path.Join(os.Getenv("HOME"), ".yorkie")
	if err := os.MkdirAll(yorkieDir, 0700); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	return yorkieDir, nil
}

// configPath returns the path of CLI.
func configPath() (string, error) {
	yorkieDir, err := ensureYorkieDir()
	if err != nil {
		return "", fmt.Errorf("ensure yorkie dir: %w", err)
	}

	return path.Join(yorkieDir, "config.json"), nil
}

// Auth is the authentication information.
type Auth struct {
	Token    string `json:"token"`
	Insecure bool   `json:"insecure"`
}

// Config is the configuration of CLI.
type Config struct {
	// Auths is the map of the address and the token.
	Auths map[string]Auth `json:"auths"`

	// RPCAddr is the address of the rpc server
	RPCAddr string `json:"rpcAddr"`
}

// New creates a new configuration.
func New() *Config {
	return &Config{
		Auths: make(map[string]Auth),
	}
}

// LoadAuth loads the authentication information for the given address.
func LoadAuth(addr string) (Auth, error) {
	config, err := Load()
	if err != nil {
		return Auth{}, fmt.Errorf("load token: %w", err)
	}

	auth, ok := config.Auths[addr]
	if !ok {
		return Auth{}, fmt.Errorf("auth for '%s' does not exist", addr)
	}

	return auth, nil
}

// Load loads the configuration from the given path.
func Load() (*Config, error) {
	configPathValue, err := configPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "get config path: %w", err)
		os.Exit(1)
	}

	file, err := os.Open(filepath.Clean(configPathValue))
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}

		return nil, fmt.Errorf("open config file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	var config *Config
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		return nil, fmt.Errorf("decode config file: %w", err)
	}

	return config, nil
}

// Save saves the configuration to the given path.
func Save(config *Config) error {
	configPathValue, err := configPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "get config path: %w", err)
		os.Exit(1)
	}

	file, err := os.Create(filepath.Clean(configPathValue))
	if err != nil {
		return fmt.Errorf("create config file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	if err := json.NewEncoder(file).Encode(config); err != nil {
		return fmt.Errorf("encode config file: %w", err)
	}

	return nil
}

// Delete deletes the configuration file.
func Delete() error {
	configPathValue, err := configPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "get config path: %w", err)
		os.Exit(1)
	}

	if err := os.Remove(filepath.Clean(configPathValue)); err != nil {
		return fmt.Errorf("remove config file: %w", err)
	}

	return nil
}

// Preload read configuration file for viper before running command
func Preload(_ *cobra.Command, _ []string) error {
	configPathValue, err := configPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "get config path: %w", err)
		os.Exit(1)
	}

	if _, err := os.Stat(filepath.Clean(configPathValue)); err != nil {
		if os.IsNotExist(err) {
			if saveErr := Save(New()); saveErr != nil {
				return fmt.Errorf("save default config: %w", saveErr)
			}
		} else {
			return fmt.Errorf("check config file: %w", err)
		}
	}

	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read in config: %w", err)
	}
	return nil
}
