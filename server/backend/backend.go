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
	"errors"
	"fmt"
	"os"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/cluster"
	pkgwebhook "github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server/backend/background"
	"github.com/yorkie-team/yorkie/server/backend/cache"
	"github.com/yorkie-team/yorkie/server/backend/channel"
	"github.com/yorkie-team/yorkie/server/backend/database"
	memdb "github.com/yorkie-team/yorkie/server/backend/database/memory"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/backend/membership"
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
	// Presence is used to manage real-time channels.
	Presence *channel.Manager

	// Background is used to manage background tasks.
	Background *background.Background
	// Membership is used to manage leader election and lease renewal.
	Membership *membership.Manager
	// Housekeeping is used to manage background batch tasks.
	Housekeeping *housekeeping.Housekeeping

	// AuthWebhookClient is used to send auth webhook.
	AuthWebhookClient *pkgwebhook.Client[types.AuthWebhookRequest, types.AuthWebhookResponse]
	// ClusterClientPool is used to manage connections to cluster nodes.
	ClusterClientPool *cluster.ClientPool
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
	membershipConf *membership.Config,
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
		SnapshotCacheSize:    conf.SnapshotCacheSize,
	})
	if err != nil {
		return nil, err
	}
	lockers := sync.New()
	pubsub := pubsub.New()

	// 03. Create the presence manager for real-time user tracking and background
	// task manager.
	presenceManager := channel.NewManager(
		pubsub,
		conf.ParsePresenceTTL(),
		conf.ParsePresenceCleanupInterval(),
	)
	bg := background.New(metrics)

	// 04. Create webhook clients and cluster client pool.
	authWebhookClient := pkgwebhook.NewClient[types.AuthWebhookRequest, types.AuthWebhookResponse]()
	eventWebhookManger := webhook.NewManager(pkgwebhook.NewClient[types.EventWebhookRequest, int]())
	clusterClientPool := cluster.NewClientPool()

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

	// 06. Create the membership manager and the housekeeping instance.
	membership := membership.New(db, conf.RPCAddr, membershipConf)
	housekeeper, err := housekeeping.New(housekeepingConf, membership)
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

		Cache:    cacheManager,
		Lockers:  lockers,
		PubSub:   pubsub,
		Presence: presenceManager,

		Background:   bg,
		Membership:   membership,
		Housekeeping: housekeeper,

		AuthWebhookClient:   authWebhookClient,
		EventWebhookManager: eventWebhookManger,
		ClusterClientPool:   clusterClientPool,

		Metrics:   metrics,
		DB:        db,
		MsgBroker: broker,
		Warehouse: warehouse,
	}, nil
}

// Start starts the backend.
func (b *Backend) Start(ctx context.Context) error {
	if err := b.Membership.Start(ctx); err != nil {
		return err
	}

	if err := b.Housekeeping.Start(ctx); err != nil {
		return err
	}

	b.Presence.Start()

	logging.DefaultLogger().Infof("backend started")
	return nil
}

// Shutdown closes all resources of this instance.
func (b *Backend) Shutdown() error {
	var errs []error

	b.Presence.Stop()

	if err := b.Housekeeping.Stop(); err != nil {
		errs = append(errs, err)
	}
	if err := b.Membership.Stop(); err != nil {
		errs = append(errs, err)
	}

	b.Background.Close()

	b.AuthWebhookClient.Close()
	b.EventWebhookManager.Close()
	b.ClusterClientPool.Close()

	if err := b.MsgBroker.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := b.Warehouse.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := b.DB.Close(); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	logging.DefaultLogger().Infof("backend stopped")
	return nil
}

// ClusterNodes returns the active cluster nodes for testing purposes.
func (b *Backend) ClusterNodes(
	ctx context.Context,
) ([]*database.ClusterNodeInfo, error) {
	return b.Membership.ClusterNodes(ctx)
}

// RemoveClusterNodes removes all cluster nodes for testing purposes.
func (b *Backend) ClearClusterNodes(ctx context.Context) error {
	return b.DB.RemoveClusterNodes(ctx)
}

// IsLeader returns whether this server is leader for testing purposes.
func (b *Backend) IsLeader() bool {
	return b.Membership.IsLeader()
}

// SetMembershipDB sets the database for membership for testing purposes.
func (h *Backend) SetMembershipDB(db database.Database) {
	h.Membership.SetDB(db)
}

// ClusterClient returns the cluster client for the gateway address.
// This client is used for unicast requests with consistent hashing.
func (b *Backend) ClusterClient() (*cluster.Client, error) {
	return b.ClusterClientPool.Get(b.Config.GatewayAddr)
}

// BroadcastCacheInvalidation broadcasts cache invalidation to all cluster nodes.
func (b *Backend) BroadcastCacheInvalidation(
	ctx context.Context,
	cacheType types.CacheType,
	key string,
) error {
	nodes, err := b.Membership.ClusterNodes(ctx)
	if err != nil {
		return fmt.Errorf("broadcast cache invalidation: %w", err)
	}

	// Prune inactive nodes from the pool (protect gateway address)
	addrs := []string{b.Config.GatewayAddr}
	for _, node := range nodes {
		addrs = append(addrs, node.RPCAddr)
	}
	b.ClusterClientPool.Prune(addrs)

	var errs []error
	for _, node := range nodes {
		nodeAddr := node.RPCAddr

		cli, err := b.ClusterClientPool.Get(nodeAddr)
		if err != nil {
			errs = append(errs, fmt.Errorf("get client for %s: %w", nodeAddr, err))
			continue
		}

		if err := cli.InvalidateCache(
			ctx,
			cacheType,
			key,
		); err != nil {
			errs = append(errs, fmt.Errorf("invalidate on %s: %w", nodeAddr, err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
