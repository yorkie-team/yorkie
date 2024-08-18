/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

// Package background provides the background service. This service is used to
// manage the background goroutines in the backend.
package background

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
)

type routineID int32

func (c *routineID) next() string {
	next := atomic.AddInt32((*int32)(c), 1)
	return "b" + strconv.Itoa(int(next))
}

// Background is the background service. It is responsible for managing
// background routines.
type Background struct {
	// closing is closed by backend close.
	closing chan struct{}

	// wgMu blocks concurrent WaitGroup mutation while backend closing
	wgMu sync.RWMutex

	// wg is used to wait for the goroutines that depends on the backend state
	// to exit when closing the backend.
	wg sync.WaitGroup

	// routineID is used to generate routine ID.
	routineID routineID

	// metrics is used to collect metrics with prometheus.
	metrics *prometheus.Metrics
}

// New creates a new background service.
func New(metrics *prometheus.Metrics) *Background {
	return &Background{
		closing: make(chan struct{}),
		metrics: metrics,
	}
}

// AttachGoroutine creates a goroutine on a given function and tracks it using
// the background's WaitGroup.
func (b *Background) AttachGoroutine(
	f func(ctx context.Context),
	taskType string,
) {
	b.wgMu.RLock() // this blocks with ongoing close(b.closing)
	defer b.wgMu.RUnlock()
	select {
	case <-b.closing:
		logging.DefaultLogger().Warn("backend has closed; skipping AttachGoroutine")
		return
	default:
	}

	// now safe to add since WaitGroup wait has not started yet
	b.wg.Add(1)
	routineLogger := logging.New(b.routineID.next())
	b.metrics.AddBackgroundGoroutines(taskType)
	go func() {
		defer func() {
			b.wg.Done()
			b.metrics.RemoveBackgroundGoroutines(taskType)
		}()
		f(logging.With(context.Background(), routineLogger))
	}()
}

// Close closes the background service. This will wait for all goroutines to
// exit.
func (b *Background) Close() {
	b.wgMu.Lock()
	close(b.closing)
	b.wgMu.Unlock()

	// wait for goroutines before closing backend
	b.wg.Wait()
}
