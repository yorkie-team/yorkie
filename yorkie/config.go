/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package yorkie

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/mongo"
	"github.com/yorkie-team/yorkie/yorkie/rpc"
)

const (
	DefaultRPCPort = 9090

	DefaultMongoConnectionURI        = "mongodb://localhost:27017"
	DefaultMongoConnectionTimeoutSec = 5
	DefaultMongoPingTimeoutSec       = 5
	DefaultMongoYorkieDatabase       = "yorkie-meta"

	DefaultSnapshotThreshold = 500
)

// Config is the configuration for creating a Yorkie instance.
type Config struct {
	RPC     *rpc.Config     `json:"RPC"`
	Mongo   *mongo.Config   `json:"Mongo"`
	Backend *backend.Config `json:"Backend"`
}

// RPCAddr returns the RPC address.
func (c *Config) RPCAddr() string {
	return fmt.Sprintf("localhost:%d", c.RPC.Port)
}

// NewConfig returns a Config struct that contains reasonable defaults
// for most of the configurations.
func NewConfig() *Config {
	return newConfig(DefaultRPCPort, DefaultMongoYorkieDatabase)
}

// NewConfigWithPortAndDBName returns a new instance of Config.
func NewConfigWithPortAndDBName(port int, dbName string) *Config {
	return newConfig(port, dbName)
}

// NewConfigFromFile returns a Config struct for the given conf file.
func NewConfigFromFile(path string) (*Config, error) {
	conf := &Config{}
	file, err := os.Open(path)
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	bytes, err := ioutil.ReadAll(file)
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	if err := json.Unmarshal(bytes, conf); err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	return conf, nil
}

func newConfig(port int, dbName string) *Config {
	return &Config{
		RPC: &rpc.Config{
			Port: port,
		},
		Backend: &backend.Config{
			SnapshotThreshold: DefaultSnapshotThreshold,
		},
		Mongo: &mongo.Config{
			ConnectionURI:        DefaultMongoConnectionURI,
			ConnectionTimeoutSec: DefaultMongoConnectionTimeoutSec,
			PingTimeoutSec:       DefaultMongoPingTimeoutSec,
			YorkieDatabase:       dbName,
		},
	}
}
