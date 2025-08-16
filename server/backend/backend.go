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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/cluster"
	pkgwebhook "github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server/backend/background"
	"github.com/yorkie-team/yorkie/server/backend/cache"
	"github.com/yorkie-team/yorkie/server/backend/database"
	memdb "github.com/yorkie-team/yorkie/server/backend/database/memory"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/backend/messagebroker"
	"github.com/yorkie-team/yorkie/server/backend/pubsub"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/backend/warehouse"
	"github.com/yorkie-team/yorkie/server/backend/webhook"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
)

// Backend manages Yorkie's backend such as Database and Coordinator. It also
// provides in-memory cache, pubsub, and locker.
type Backend struct {
	Config *Config

	// Cache is the central cache manager for all caches.
	Cache *cache.Manager
	// PubSub is used to publish/subscribe events to/from clients.
	PubSub *pubsub.PubSub
	// Lockers is used to lock/unlock resources.
	Lockers *sync.LockerManager

	// Background is used to manage background tasks.
	Background *background.Background
	// Housekeeping is used to manage background batch tasks.
	Housekeeping *housekeeping.Housekeeping

	// AuthWebhookClient is used to send auth webhook.
	AuthWebhookClient *pkgwebhook.Client[types.AuthWebhookRequest, types.AuthWebhookResponse]
	// ClusterClient is used to send requests to nodes in the cluster.
	ClusterClient *cluster.Client
	// EventWebhookManager is used to send event webhook
	EventWebhookManager *webhook.Manager

	// Metrics is used to expose metrics.
	Metrics *prometheus.Metrics
	// DB is the database instance.
	DB database.Database
	// MsgBroker is the message producer instance.
	MsgBroker messagebroker.Broker
	// Warehouse is the warehouse instance.
	Warehouse warehouse.Warehouse
}

// New creates a new instance of Backend.
func New(
	conf *Config,
	mongoConf *mongo.Config,
	housekeepingConf *housekeeping.Config,
	metrics *prometheus.Metrics,
	kafkaConf *messagebroker.Config,
	rocksConf *warehouse.Config,
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

	// 02. Create the cache manager, pubsub, and lockers.
	cacheManager, err := cache.New(cache.Options{
		AuthWebhookCacheSize: conf.AuthWebhookCacheSize,
		AuthWebhookCacheTTL:  conf.ParseAuthWebhookCacheTTL(),
		ProjectCacheSize:     conf.ProjectCacheSize,
		ProjectCacheTTL:      conf.ParseProjectCacheTTL(),
		SnapshotCacheSize:    conf.SnapshotCacheSize,
	})
	if err != nil {
		return nil, err
	}
	lockers := sync.New()
	pubsub := pubsub.New()

	// 03. Create the background instance. The background instance is used to
	// manage background tasks.
	bg := background.New(metrics)

	// 04. Create webhook clients and cluster client.
	authWebhookClient := pkgwebhook.NewClient[types.AuthWebhookRequest, types.AuthWebhookResponse](
		pkgwebhook.Options{
			MaxRetries:      conf.AuthWebhookMaxRetries,
			MinWaitInterval: conf.ParseAuthWebhookMinWaitInterval(),
			MaxWaitInterval: conf.ParseAuthWebhookMaxWaitInterval(),
			RequestTimeout:  conf.ParseAuthWebhookRequestTimeout(),
		},
	)
	eventWebhookManger := webhook.NewManager(pkgwebhook.NewClient[types.EventWebhookRequest, int](
		pkgwebhook.Options{
			MaxRetries:      conf.EventWebhookMaxRetries,
			MinWaitInterval: conf.ParseEventWebhookMinWaitInterval(),
			MaxWaitInterval: conf.ParseEventWebhookMaxWaitInterval(),
			RequestTimeout:  conf.ParseEventWebhookRequestTimeout(),
		},
	))
	clusterClient, err := cluster.Dial(conf.GatewayAddr)
	if err != nil {
		return nil, err
	}

	// 05. Create the database instance. If the MongoDB configuration is given,
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

	// 06. Create the housekeeping instance. The housekeeping is used
	// to manage housekeeper tasks such as deactivating inactive clients.
	housekeeper, err := housekeeping.New(housekeepingConf, db, conf.Hostname)
	if err != nil {
		return nil, err
	}

	// 07. Create the message broker instance.
	broker := messagebroker.Ensure(kafkaConf)

	// 08. Ensure the warehouse instance.
	warehouse, err := warehouse.Ensure(rocksConf)
	if err != nil {
		return nil, err
	}

	// 09. Ensure the default user and project. If the default user and project
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
	logging.DefaultLogger().Infof("backend created: db: %s", dbInfo)

	return &Backend{
		Config: conf,

		Cache:        cacheManager,
		Lockers:      lockers,
		PubSub:       pubsub,
		Background:   bg,
		Housekeeping: housekeeper,

		AuthWebhookClient:   authWebhookClient,
		EventWebhookManager: eventWebhookManger,
		ClusterClient:       clusterClient,

		Metrics:   metrics,
		DB:        db,
		MsgBroker: broker,
		Warehouse: warehouse,
	}, nil
}

// Start starts the backend.
func (b *Backend) Start(ctx context.Context) error {
	if err := b.Housekeeping.Start(ctx); err != nil {
		return err
	}

	logging.DefaultLogger().Infof("backend started")
	return nil
}

// Shutdown closes all resources of this instance.
func (b *Backend) Shutdown() error {
	if err := b.Housekeeping.Stop(); err != nil {
		return err
	}
	b.Background.Close()

	b.AuthWebhookClient.Close()
	b.EventWebhookManager.Close()
	b.ClusterClient.Close()

	if err := b.MsgBroker.Close(); err != nil {
		logging.DefaultLogger().Error(err)
	}
	if err := b.Warehouse.Close(); err != nil {
		logging.DefaultLogger().Error(err)
	}
	if err := b.DB.Close(); err != nil {
		logging.DefaultLogger().Error(err)
	}

	logging.DefaultLogger().Infof("backend stopped")
	return nil
}
