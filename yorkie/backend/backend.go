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

package backend

import (
	"os"
	"time"

	"github.com/rs/xid"

	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/yorkie/backend/background"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
	memdb "github.com/yorkie-team/yorkie/yorkie/backend/db/memory"
	"github.com/yorkie-team/yorkie/yorkie/backend/db/mongo"
	"github.com/yorkie-team/yorkie/yorkie/backend/housekeeping"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync/etcd"
	memsync "github.com/yorkie-team/yorkie/yorkie/backend/sync/memory"
	"github.com/yorkie-team/yorkie/yorkie/log"
	"github.com/yorkie-team/yorkie/yorkie/profiling/prometheus"
)

// Backend manages Yorkie's backend such as Database and Coordinator. And it
// has the server status such as the information of this Agent.
type Backend struct {
	Config    *Config
	agentInfo *sync.AgentInfo

	Background       *background.Background
	DB               db.DB
	Coordinator      sync.Coordinator
	Metrics          *prometheus.Metrics
	Housekeeping     *housekeeping.Housekeeping
	AuthWebhookCache *cache.LRUExpireCache
}

// New creates a new instance of Backend.
func New(
	conf *Config,
	mongoConf *mongo.Config,
	etcdConf *etcd.Config,
	housekeepingConf *housekeeping.Config,
	rpcAddr string,
	metrics *prometheus.Metrics,
) (*Backend, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	agentInfo := &sync.AgentInfo{
		ID:        xid.New().String(),
		Hostname:  hostname,
		RPCAddr:   rpcAddr,
		UpdatedAt: time.Now(),
	}

	bg := background.New()

	var database db.DB
	if mongoConf != nil {
		database, err = mongo.Dial(mongoConf)
		if err != nil {
			return nil, err
		}
	} else {
		database, err = memdb.New()
		if err != nil {
			return nil, err
		}
	}

	var coordinator sync.Coordinator
	if etcdConf != nil {
		etcdClient, err := etcd.Dial(etcdConf, agentInfo)
		if err != nil {
			return nil, err
		}
		if err := etcdClient.Initialize(); err != nil {
			return nil, err
		}

		coordinator = etcdClient
	} else {
		coordinator = memsync.NewCoordinator(agentInfo)
	}

	authWebhookCache, err := cache.NewLRUExpireCache(conf.AuthWebhookCacheSize)
	if err != nil {
		return nil, err
	}

	keeping, err := housekeeping.Start(
		housekeepingConf,
		database,
		coordinator,
	)
	if err != nil {
		return nil, err
	}

	dbInfo := "memory"
	if mongoConf != nil {
		dbInfo = mongoConf.ConnectionURI
	}

	log.Logger().Infof(
		"backend created: id: %s, rpc: %s: db: %s",
		agentInfo.ID,
		agentInfo.RPCAddr,
		dbInfo,
	)

	return &Backend{
		Config:    conf,
		agentInfo: agentInfo,

		Background:       bg,
		Metrics:          metrics,
		DB:               database,
		Coordinator:      coordinator,
		Housekeeping:     keeping,
		AuthWebhookCache: authWebhookCache,
	}, nil
}

// Shutdown closes all resources of this instance.
func (b *Backend) Shutdown() error {
	// this will wait for all goroutines to exit
	b.Background.Close()

	if err := b.Housekeeping.Stop(); err != nil {
		return err
	}

	if err := b.Coordinator.Close(); err != nil {
		log.Logger().Error(err)
	}

	if err := b.DB.Close(); err != nil {
		log.Logger().Error(err)
	}

	log.Logger().Infof(
		"backend stoped: id: %s, rpc: %s",
		b.agentInfo.ID,
		b.agentInfo.RPCAddr,
	)

	return nil
}

// Members returns the members of this cluster.
func (b *Backend) Members() map[string]*sync.AgentInfo {
	return b.Coordinator.Members()
}
