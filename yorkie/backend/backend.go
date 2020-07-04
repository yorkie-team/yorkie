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
	defaultSync "sync"

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/pkg/sync"
	"github.com/yorkie-team/yorkie/yorkie/backend/mongo"
	"github.com/yorkie-team/yorkie/yorkie/pubsub"
)

type Config struct {
	// SnapshotThreshold is the threshold that determines if changes should be
	// sent with snapshot when the number of changes is greater than this value.
	SnapshotThreshold uint64 `json:"SnapshotThreshold"`

	// SnapshotInterval is the interval of changes to create a snapshot.
	SnapshotInterval uint64 `json:"SnapshotInterval"`
}

// Backend manages Yorkie's remote states such as data store, distributed lock
// and etc. And it has the server status like the configuration.
type Backend struct {
	Config *Config

	Mongo  *mongo.Client
	MutexMap *sync.MutexMap
	PubSub   *pubsub.PubSub

	// closing is closed by backend close.
	closing chan struct{}

	// wgMu blocks concurrent WaitGroup mutation while backend closing
	wgMu defaultSync.RWMutex

	// wg is used to wait for the goroutines that depends on the backend state
	// to exit when closing the backend.
	wg defaultSync.WaitGroup
}

// New creates a new instance of Backend.
func New(conf *Config, mongoConf *mongo.Config) (*Backend, error) {
	client, err := mongo.NewClient(mongoConf)
	if err != nil {
		return nil, err
	}

	return &Backend{
		Config:   conf,
		Mongo:    client,
		MutexMap: sync.NewMutexMap(),
		PubSub:   pubsub.New(),
		closing:  make(chan struct{}),
	}, nil
}

// Close closes all resources of this instance.
func (b *Backend) Close() error {
	b.wgMu.Lock()
	close(b.closing)
	b.wgMu.Unlock()

	// wait for goroutines before closing backend
	b.wg.Wait()

	if err := b.Mongo.Close(); err != nil {
		return err
	}

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
