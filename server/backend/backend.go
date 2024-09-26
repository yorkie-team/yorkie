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

// Package backend provides the backend implementation of the Yorkie.
// This package is responsible for managing the database and other
// resources required to run Yorkie.
package backend

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rs/xid"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/server/backend/background"
	"github.com/yorkie-team/yorkie/server/backend/database"
	memdb "github.com/yorkie-team/yorkie/server/backend/database/memory"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	memsync "github.com/yorkie-team/yorkie/server/backend/sync/memory"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
)

// Backend manages Yorkie's backend such as Database and Coordinator. And it
// has the server status such as the information of this Server.
type Backend struct {
	Config           *Config
	serverInfo       *sync.ServerInfo
	AuthWebhookCache *cache.LRUExpireCache[string, *types.AuthWebhookResponse]

	Metrics      *prometheus.Metrics
	DB           database.Database
	Coordinator  sync.Coordinator
	Background   *background.Background
	Housekeeping *housekeeping.Housekeeping
}

// New creates a new instance of Backend.
func New(
	conf *Config,
	mongoConf *mongo.Config,
	housekeepingConf *housekeeping.Config,
	metrics *prometheus.Metrics,
) (*Backend, error) {
	// 01. Build the server info with the given hostname or the hostname of the
	// current machine.
	hostname := conf.Hostname
	if hostname == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("os.Hostname: %w", err)
		}
		conf.Hostname = hostname
	}

	serverInfo := &sync.ServerInfo{
		ID:        xid.New().String(),
		Hostname:  hostname,
		UpdatedAt: time.Now(),
	}

	// 02. Create the auth webhook cache. The auth webhook cache is used to
	// cache the response of the auth webhook.
	// TODO(hackerwins): Consider to extend the cache for general purpose.
	webhookCache, err := cache.NewLRUExpireCache[string, *types.AuthWebhookResponse](conf.AuthWebhookCacheSize)
	if err != nil {
		return nil, err
	}

	// 03. Create the background instance. The background instance is used to
	// manage background tasks.
	bg := background.New(metrics)

	// 04. Create the database instance. If the MongoDB configuration is given,
	// create a MongoDB instance. Otherwise, create a memory database instance.
	var db database.Database
	if mongoConf != nil {
		db, err = mongo.Dial(mongoConf)
		if err != nil {
			return nil, err
		}
	} else {
		db, err = memdb.New()
		if err != nil {
			return nil, err
		}
	}

	// 05. Create the coordinator instance. The coordinator is used to manage
	// the synchronization between the Yorkie servers.
	// TODO(hackerwins): Implement the coordinator for a shard. For now, we
	//  distribute workloads to all shards per document. In the future, we
	//  will need to distribute workloads of a document.
	coordinator := memsync.NewCoordinator(serverInfo)

	// 06. Create the housekeeping instance. The housekeeping is used
	// to manage keeping tasks such as deactivating inactive clients.
	keeping, err := housekeeping.New(housekeepingConf)
	if err != nil {
		return nil, err
	}

	// 07. Ensure the default user and project. If the default user and project
	// do not exist, create them.
	if conf.UseDefaultProject {
		_, _, err = db.EnsureDefaultUserAndProject(
			context.Background(),
			conf.AdminUser,
			conf.AdminPassword,
			conf.ClientDeactivateThreshold,
		)
		if err != nil {
			return nil, err
		}
	}

	dbInfo := "memory"
	if mongoConf != nil {
		dbInfo = mongoConf.ConnectionURI
	}

	logging.DefaultLogger().Infof(
		"backend created: id: %s, db: %s",
		serverInfo.ID,
		dbInfo,
	)

	return &Backend{
		Config:           conf,
		serverInfo:       serverInfo,
		AuthWebhookCache: webhookCache,

		Metrics:      metrics,
		DB:           db,
		Coordinator:  coordinator,
		Background:   bg,
		Housekeeping: keeping,
	}, nil
}

// Start starts the backend.
func (b *Backend) Start() error {
	if err := b.Housekeeping.Start(); err != nil {
		return err
	}

	logging.DefaultLogger().Infof("backend started: id: %s", b.serverInfo.ID)
	return nil
}

// Shutdown closes all resources of this instance.
func (b *Backend) Shutdown() error {
	if err := b.Housekeeping.Stop(); err != nil {
		return err
	}

	b.Background.Close()

	if err := b.Coordinator.Close(); err != nil {
		logging.DefaultLogger().Error(err)
	}

	if err := b.DB.Close(); err != nil {
		logging.DefaultLogger().Error(err)
	}

	logging.DefaultLogger().Infof("backend stopped: id: %s", b.serverInfo.ID)
	return nil
}

// Members returns the members of this cluster.
func (b *Backend) Members() map[string]*sync.ServerInfo {
	return b.Coordinator.Members()
}
