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
	"github.com/yorkie-team/yorkie/yorkie/backend/mongo"
)

const (
	DefaultMongoDBURI     = "mongodb://localhost:27017"
	DefaultYorkieDatabase = "yorkie-meta"
)

type Config struct {
	RPCPort int
	Mongo   *mongo.Config
}

func (c *Config) RPCAddr() string {
	return fmt.Sprintf("localhost:%d", c.RPCPort)
}

func newConfig(port int, dbname string) *Config {
	return &Config{
		RPCPort: port,
		Mongo: &mongo.Config{
			ConnectionURI:        DefaultMongoDBURI,
			ConnectionTimeoutSec: 5,
			PingTimeoutSec:       5,
			YorkieDatabase:       dbname,
		},
	}
}

func NewConfig() *Config {
	const port = 9090
	return newConfig(port, DefaultYorkieDatabase)
}

func NewConfigForTest(port int, dbname string) *Config {
	return newConfig(port, dbname)
}

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
