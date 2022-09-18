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

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
)

// AdminAddr is the address of the admin server.
var AdminAddr string

// ensureYorkieDir ensures that the directory of Yorkie exists.
func ensureYorkieDir() string {
	yorkieDir := path.Join(os.Getenv("HOME"), ".yorkie")
	if err := os.MkdirAll(yorkieDir, 0700); err != nil {
		panic(err)
	}
	return yorkieDir
}

// configPath returns the path of CLI.
func configPath() string {
	return path.Join(ensureYorkieDir(), "config.json")
}

// Config is the configuration of CLI.
type Config struct {
	// Auths is the map of the address and the token.
	Auths map[string]string `json:"auths"`
}

// New creates a new configuration.
func New() *Config {
	return &Config{
		Auths: make(map[string]string),
	}
}

// LoadToken loads the token from the given address.
func LoadToken(addr string) (string, error) {
	config, err := Load()
	if err != nil {
		return "", err
	}

	return config.Auths[addr], nil
}

// Load loads the configuration from the given path.
func Load() (*Config, error) {
	confPath := configPath()
	file, err := os.Open(confPath)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}

		return nil, fmt.Errorf("failed to open config file %s: %w", confPath, err)
	}
	defer func() {
		_ = file.Close()
	}()

	var config *Config
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to decode config file: %w", err)
	}

	return config, nil
}

// Save saves the configuration to the given path.
func Save(config *Config) error {
	confPath := configPath()
	file, err := os.Create(confPath)
	if err != nil {
		return fmt.Errorf("failed to create config file %s: %w", confPath, err)
	}
	defer func() {
		_ = file.Close()
	}()

	if err := json.NewEncoder(file).Encode(config); err != nil {
		return fmt.Errorf("failed to encode config file: %w", err)
	}

	return nil
}
