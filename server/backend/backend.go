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
	"github.com/yorkie-team/yorkie/pkg/cache"
	pkgtypes "github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/server/backend/background"
	"github.com/yorkie-team/yorkie/server/backend/database"
	memdb "github.com/yorkie-team/yorkie/server/backend/database/memory"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/backend/messagebroker"
	"github.com/yorkie-team/yorkie/server/backend/pubsub"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
)

// Backend manages Yorkie's backend such as Database and Coordinator. It also
// provides in-memory cache, pubsub, and locker.
type Backend struct {
	Config *Config

	// AuthWebhookCache is used to cache the response of the auth webhook.
	WebhookCache *cache.LRUExpireCache[string, pkgtypes.Pair[
		int,
		*types.AuthWebhookResponse,
	]]
	// PubSub is used to publish/subscribe events to/from clients.
	PubSub *pubsub.PubSub
	// Locker is used to lock/unlock resources.
	Locker *sync.LockerManager

	// Metrics is used to expose metrics.
	Metrics *prometheus.Metrics
	// DB is the database instance.
	DB database.Database

	// Background is used to manage background tasks.
	Background *background.Background
	// Housekeeping is used to manage background batch tasks.
	Housekeeping *housekeeping.Housekeeping
	// MessageBroker is used to send the message to the message broker.
	MessageBroker *messagebroker.KafkaProducer
}

// New creates a new instance of Backend.
func New(
	conf *Config,
	mongoConf *mongo.Config,
	housekeepingConf *housekeeping.Config,
	metrics *prometheus.Metrics,
	messagebrokerConf *messagebroker.Config,
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

	// 02. Create in-memory cache, pubsub, and locker.
	cache := cache.NewLRUExpireCache[string, pkgtypes.Pair[int, *types.AuthWebhookResponse]](
		conf.AuthWebhookCacheSize,
	)
	locker := sync.New()
	pubsub := pubsub.New()

	// 03. Create the background instance. The background instance is used to
	// manage background tasks.
	bg := background.New(metrics)

	// 04. Create the database instance. If the MongoDB configuration is given,
	// create a MongoDB instance. Otherwise, create a memory database instance.
	var err error
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

	// 08. Create the message broker producer instance. The message broker producer is used to
	// send the message to the message broker.
	var kafkaProducer *messagebroker.KafkaProducer
	if messagebrokerConf != nil && messagebrokerConf.ConnectionURL != "" && messagebrokerConf.Topic != "" {
		kafkaProducer = messagebroker.NewKafkaProducer(messagebrokerConf.ConnectionURL, messagebrokerConf.Topic)
		logging.DefaultLogger().Infof("message broker producer: URL: %s, Topic: %s",
			messagebrokerConf.ConnectionURL, messagebrokerConf.Topic)
	} else {
		kafkaProducer = nil
		logging.DefaultLogger().Infof("message broker producer is not created")
	}

	// 09. Return the backend instance.
	dbInfo := "memory"
	if mongoConf != nil {
		dbInfo = mongoConf.ConnectionURI
	}

	logging.DefaultLogger().Infof(
		"backend created: db: %s",
		dbInfo,
	)

	return &Backend{
		Config:       conf,
		WebhookCache: cache,
		Locker:       locker,
		PubSub:       pubsub,

		Metrics:       metrics,
		DB:            db,
		Background:    bg,
		Housekeeping:  keeping,
		MessageBroker: kafkaProducer,
	}, nil
}

// Start starts the backend.
func (b *Backend) Start() error {
	if err := b.Housekeeping.Start(); err != nil {
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

	if err := b.DB.Close(); err != nil {
		logging.DefaultLogger().Error(err)
	}

	logging.DefaultLogger().Infof("backend stopped")
	return nil
}
