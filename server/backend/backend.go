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
	Config     *Config
	serverInfo *sync.ServerInfo

	DB           database.Database
	Coordinator  sync.Coordinator
	Metrics      *prometheus.Metrics
	Background   *background.Background
	Housekeeping *housekeeping.Housekeeping

	AuthWebhookCache *cache.LRUExpireCache[string, *types.AuthWebhookResponse]
}

// New creates a new instance of Backend.
func New(
	conf *Config,
	mongoConf *mongo.Config,
	housekeepingConf *housekeeping.Config,
	metrics *prometheus.Metrics,
) (*Backend, error) {
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

	bg := background.New()

	var db database.Database
	var err error
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

	// TODO(hackerwins): Implement the coordinator for a shard. For now, we
	//  distribute workloads to all shards per document. In the future, we
	//  will need to distribute workloads of a document.
	coordinator := memsync.NewCoordinator(serverInfo)

	authWebhookCache, err := cache.NewLRUExpireCache[string, *types.AuthWebhookResponse](conf.AuthWebhookCacheSize)
	if err != nil {
		return nil, err
	}

	keeping, err := housekeeping.Start(
		housekeepingConf,
		db,
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
		"backend created: id: %s, rpc: %s",
		serverInfo.ID,
		dbInfo,
	)

	_, _, err = db.EnsureDefaultUserAndProject(
		context.Background(),
		conf.AdminUser,
		conf.AdminPassword,
		conf.ClientDeactivateThreshold,
	)
	if err != nil {
		return nil, err
	}

	return &Backend{
		Config:     conf,
		serverInfo: serverInfo,

		Background:   bg,
		Metrics:      metrics,
		DB:           db,
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

	logging.DefaultLogger().Infof("backend stopped: id: %s", b.serverInfo.ID)
	return nil
}

// Members returns the members of this cluster.
func (b *Backend) Members() map[string]*sync.ServerInfo {
	return b.Coordinator.Members()
}
