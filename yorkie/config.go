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
	"path/filepath"

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/db/mongo"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync/etcd"
	"github.com/yorkie-team/yorkie/yorkie/metrics"
	"github.com/yorkie-team/yorkie/yorkie/rpc"
)

// Below are the values of the default values of Yorkie config.
const (
	DefaultRPCPort     = 11101
	DefaultMetricsPort = 11102

	DefaultMongoConnectionURI        = "mongodb://localhost:27017"
	DefaultMongoConnectionTimeoutSec = 5
	DefaultMongoPingTimeoutSec       = 5
	DefaultMongoYorkieDatabase       = "yorkie-meta"

	DefaultSnapshotThreshold = 500
	DefaultSnapshotInterval  = 100
)

// Config is the configuration for creating a Yorkie instance.
type Config struct {
	RPC     *rpc.Config     `json:"RPC"`
	Metrics *metrics.Config `json:"Metrics"`
	Mongo   *mongo.Config   `json:"Mongo"`
	ETCD    *etcd.Config    `json:"ETCD"`
	Backend *backend.Config `json:"Backend"`
}

// RPCAddr returns the RPC address.
func (c *Config) RPCAddr() string {
	return fmt.Sprintf("localhost:%d", c.RPC.Port)
}

// NewConfig returns a Config struct that contains reasonable defaults
// for most of the configurations.
func NewConfig() *Config {
	return newConfig(DefaultRPCPort, DefaultMetricsPort, DefaultMongoYorkieDatabase)
}

// NewConfigFromFile returns a Config struct for the given conf file.
func NewConfigFromFile(path string) (*Config, error) {
	conf := &Config{}
	file, err := os.Open(filepath.Clean(path))
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

func newConfig(port int, metricsPort int, dbName string) *Config {
	return &Config{
		RPC: &rpc.Config{
			Port: port,
		},
		Metrics: &metrics.Config{
			Port: metricsPort,
		},
		Backend: &backend.Config{
			SnapshotThreshold: DefaultSnapshotThreshold,
			SnapshotInterval:  DefaultSnapshotInterval,
		},
		Mongo: &mongo.Config{
			ConnectionURI:        DefaultMongoConnectionURI,
			ConnectionTimeoutSec: DefaultMongoConnectionTimeoutSec,
			PingTimeoutSec:       DefaultMongoPingTimeoutSec,
			YorkieDatabase:       dbName,
		},
	}
}
