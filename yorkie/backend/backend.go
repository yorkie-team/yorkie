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
	"fmt"
	"os"
	gosync "sync"
	"time"

	"github.com/rs/xid"

	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/types"
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
	AuthorizationWebhookURL string `json:"AuthorizationWebhookURL"`

	// AuthorizationWebhookMethods is the methods that run the authorization webhook.
	AuthorizationWebhookMethods []string `json:"AuthorizationWebhookMethods"`

	// AuthorizationWebhookMaxRetries is the max count
	// that retries the authorization webhook.
	AuthorizationWebhookMaxRetries uint64

	// AuthorizationWebhookMaxWaitInterval is the max interval
	// that waits before retrying the authorization webhook.
	AuthorizationWebhookMaxWaitInterval uint64
}

// RequireAuth returns whether the given method require authorization.
func (c *Config) RequireAuth(method types.Method) bool {
	if len(c.AuthorizationWebhookURL) == 0 {
		return false
	}

	if len(c.AuthorizationWebhookMethods) == 0 {
		return true
	}

	for _, m := range c.AuthorizationWebhookMethods {
		if types.Method(m) == method {
			return true
		}
	}

	return false
}

// Validate validates this config.
func (c *Config) Validate() error {
	for _, method := range c.AuthorizationWebhookMethods {
		if !types.IsAuthMethod(method) {
			return fmt.Errorf("not supported method for authorization webhook: %s", method)
		}
	}

	return nil
}

// Backend manages Yorkie's remote states such as data store, distributed lock
// and etc. And it has the server status like the configuration.
type Backend struct {
	Config    *Config
	agentInfo *sync.AgentInfo

	DB          db.DB
	Coordinator sync.Coordinator
	Metrics     metrics.Metrics

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
		coordinator = memory.NewCoordinator(agentInfo)
	}

	log.Logger.Infof(
		"backend created: id: %s, rpc: %s",
		agentInfo.ID,
		agentInfo.RPCAddr,
	)

	return &Backend{
		Config:      conf,
		agentInfo:   agentInfo,
		DB:          mongoClient,
		Coordinator: coordinator,
		Metrics:     met,
		closing:     make(chan struct{}),
	}, nil
}

// Close closes all resources of this instance.
func (b *Backend) Close() error {
	b.wgMu.Lock()
	close(b.closing)
	b.wgMu.Unlock()

	// wait for goroutines before closing backend
	b.wg.Wait()

	if err := b.Coordinator.Close(); err != nil {
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
	return b.Coordinator.Members()
}
