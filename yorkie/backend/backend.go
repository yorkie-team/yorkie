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
	gosync "sync"
	"time"

	"github.com/rs/xid"

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
	"github.com/yorkie-team/yorkie/yorkie/backend/db/mongo"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync/etcd"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync/memory"
	"github.com/yorkie-team/yorkie/yorkie/metrics"
)

// Config is the configuration for creating a Backend instance.
type Config struct {
	// SnapshotThreshold is the threshold that determines if changes should be
	// sent with snapshot when the number of changes is greater than this value.
	SnapshotThreshold uint64 `json:"SnapshotThreshold"`

	// SnapshotInterval is the interval of changes to create a snapshot.
	SnapshotInterval uint64 `json:"SnapshotInterval"`

	// AuthorizationWebhookURL is the url of the authorization webhook.
	AuthorizationWebhookURL string
}

// Backend manages Yorkie's remote states such as data store, distributed lock
// and etc. And it has the server status like the configuration.
type Backend struct {
	Config    *Config
	agentInfo *sync.AgentInfo

	DB        db.DB
	LockerMap sync.LockerMap
	PubSub    sync.PubSub
	Metrics   metrics.Metrics

	// closing is closed by backend close.
	closing chan struct{}

	// wgMu blocks concurrent WaitGroup mutation while backend closing
	wgMu gosync.RWMutex

	// wg is used to wait for the goroutines that depends on the backend state
	// to exit when closing the backend.
	wg gosync.WaitGroup
}

// New creates a new instance of Backend.
func New(
	conf *Config,
	mongoConf *mongo.Config,
	etcdConf *etcd.Config,
	rpcAddr string,
	met metrics.Metrics,
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

	mongoClient, err := mongo.Dial(mongoConf)
	if err != nil {
		return nil, err
	}

	// TODO(hackerwins): Merge these instances into Coordinator.
	var pubSub sync.PubSub
	var lockerMap sync.LockerMap
	if etcdConf != nil {
		etcdClient, err := etcd.Dial(etcdConf, agentInfo)
		if err != nil {
			return nil, err
		}
		if err := etcdClient.Initialize(); err != nil {
			return nil, err
		}

		lockerMap = etcdClient
		pubSub = etcdClient
	} else {
		lockerMap = memory.NewLockerMap()
		pubSub = memory.NewPubSub(agentInfo)
	}

	log.Logger.Infof(
		"backend created: id: %s, rpc: %s",
		agentInfo.ID,
		agentInfo.RPCAddr,
	)

	return &Backend{
		Config:    conf,
		agentInfo: agentInfo,
		DB:        mongoClient,
		LockerMap: lockerMap,
		PubSub:    pubSub,
		Metrics:   met,
		closing:   make(chan struct{}),
	}, nil
}

// Close closes all resources of this instance.
func (b *Backend) Close() error {
	b.wgMu.Lock()
	close(b.closing)
	b.wgMu.Unlock()

	// wait for goroutines before closing backend
	b.wg.Wait()

	if err := b.LockerMap.Close(); err != nil {
		log.Logger.Error(err)
	}

	if err := b.DB.Close(); err != nil {
		log.Logger.Error(err)
	}

	log.Logger.Infof(
		"backend stoped: id: %s, rpc: %s",
		b.agentInfo.ID,
		b.agentInfo.RPCAddr,
	)

	return nil
}

// AttachGoroutine creates a goroutine on a given function and tracks it using
// the backend's WaitGroup.
func (b *Backend) AttachGoroutine(f func()) {
	b.wgMu.RLock() // this blocks with ongoing close(b.closing)
	defer b.wgMu.RUnlock()
	select {
	case <-b.closing:
		log.Logger.Warn("backend has closed; skipping AttachGoroutine")
		return
	default:
	}

	// now safe to add since WaitGroup wait has not started yet
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		f()
	}()
}

// Members returns the members of this cluster.
func (b *Backend) Members() map[string]*sync.AgentInfo {
	return b.PubSub.Members()
}
