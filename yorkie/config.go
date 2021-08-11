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
	"os"
	"path/filepath"
	"time"

	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/db/mongo"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync/etcd"
	"github.com/yorkie-team/yorkie/yorkie/metrics/prometheus"
	"github.com/yorkie-team/yorkie/yorkie/rpc"
)

// Below are the values of the default values of Yorkie config.
const (
	DefaultRPCPort     = 11101
	DefaultMetricsPort = 11102

	DefaultMongoConnectionURI     = "mongodb://localhost:27017"
	DefaultMongoConnectionTimeout = 5 * time.Second
	DefaultMongoPingTimeout       = 5 * time.Second
	DefaultMongoYorkieDatabase    = "yorkie-meta"

	DefaultSnapshotThreshold = 500
	DefaultSnapshotInterval  = 100

	DefaultAuthWebhookMaxRetries                       = 10
	DefaultAuthWebhookWaitInterval                     = 3000 * time.Millisecond
	DefaultAuthorizationWebhookCacheAuthorizedTTLSec   = 10
	DefaultAuthorizationWebhookCacheUnauthorizedTTLSec = 10
)

// Config is the configuration for creating a Yorkie instance.
type Config struct {
	RPC     *rpc.Config        `json:"RPC"`
	Metrics *prometheus.Config `json:"Metrics"`
	Mongo   *mongo.Config      `json:"Mongo"`
	ETCD    *etcd.Config       `json:"ETCD"`
	Backend *backend.Config    `json:"Backend"`
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

	if err := json.NewDecoder(file).Decode(conf); err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	return conf, nil
}

// RPCAddr returns the RPC address.
func (c *Config) RPCAddr() string {
	return fmt.Sprintf("localhost:%d", c.RPC.Port)
}

// Validate returns an error if the provided Config is invalidated.
func (c *Config) Validate() error {
	if err := c.RPC.Validate(); err != nil {
		return err
	}

	// TODO(umi0410): Other validations will be here later.

	if err := c.Backend.Validate(); err != nil {
		return err
	}

	if err := c.Mongo.Validate(); err != nil {
		return err
	}

	return nil
}

func newConfig(port int, metricsPort int, dbName string) *Config {
	return &Config{
		RPC: &rpc.Config{
			Port: port,
		},
		Metrics: &prometheus.Config{
			Port: metricsPort,
		},
		Backend: &backend.Config{
			SnapshotThreshold: DefaultSnapshotThreshold,
			SnapshotInterval:  DefaultSnapshotInterval,
		},
		Mongo: &mongo.Config{
			ConnectionURI:     DefaultMongoConnectionURI,
			ConnectionTimeout: DefaultMongoConnectionTimeout.String(),
			PingTimeout:       DefaultMongoPingTimeout.String(),
			YorkieDatabase:    dbName,
		},
	}
}
