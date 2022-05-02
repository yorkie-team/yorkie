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
	"context"
	"os"
	"time"

	"github.com/rs/xid"

	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/server/backend/background"
	"github.com/yorkie-team/yorkie/server/backend/db"
	memdb "github.com/yorkie-team/yorkie/server/backend/db/memory"
	"github.com/yorkie-team/yorkie/server/backend/db/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/backend/sync/etcd"
	memsync "github.com/yorkie-team/yorkie/server/backend/sync/memory"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
)

// Backend manages Yorkie's backend such as Database and Coordinator. And it
// has the server status such as the information of this Server.
type Backend struct {
	Config     *Config
	serverInfo *sync.ServerInfo

	DB           db.DB
	Coordinator  sync.Coordinator
	Metrics      *prometheus.Metrics
	Background   *background.Background
	Housekeeping *housekeeping.Housekeeping

	AuthWebhookCache *cache.LRUExpireCache
}

// New creates a new instance of Backend.
func New(
	conf *Config,
	mongoConf *mongo.Config,
	etcdConf *etcd.Config,
	housekeepingConf *housekeeping.Config,
	clusterAddr string,
	metrics *prometheus.Metrics,
) (*Backend, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	serverInfo := &sync.ServerInfo{
		ID:          xid.New().String(),
		Hostname:    hostname,
		ClusterAddr: clusterAddr,
		UpdatedAt:   time.Now(),
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
		etcdClient, err := etcd.Dial(etcdConf, serverInfo)
		if err != nil {
			return nil, err
		}
		if err := etcdClient.Initialize(); err != nil {
			return nil, err
		}

		coordinator = etcdClient
	} else {
		coordinator = memsync.NewCoordinator(serverInfo)
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

	logging.DefaultLogger().Infof(
		"backend created: id: %s, rpc: %s: db: %s",
		serverInfo.ID,
		serverInfo.ClusterAddr,
		dbInfo,
	)

	_, err = database.EnsureDefaultProjectInfo(context.Background())
	if err != nil {
		return nil, err
	}

	return &Backend{
		Config:     conf,
		serverInfo: serverInfo,

		Background:   bg,
		Metrics:      metrics,
		DB:           database,
		Coordinator:  coordinator,
		Housekeeping: keeping,

		AuthWebhookCache: authWebhookCache,
	}, nil
}

// Shutdown closes all resources of this instance.
func (b *Backend) Shutdown() error {
	b.Background.Close()

	if err := b.Housekeeping.Stop(); err != nil {
		return err
	}

	if err := b.Coordinator.Close(); err != nil {
		logging.DefaultLogger().Error(err)
	}

	if err := b.DB.Close(); err != nil {
		logging.DefaultLogger().Error(err)
	}

	logging.DefaultLogger().Infof(
		"backend stoped: id: %s, rpc: %s",
		b.serverInfo.ID,
		b.serverInfo.ClusterAddr,
	)

	return nil
}

// Members returns the members of this cluster.
func (b *Backend) Members() map[string]*sync.ServerInfo {
	return b.Coordinator.Members()
}
