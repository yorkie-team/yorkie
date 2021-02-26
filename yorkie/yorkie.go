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

package yorkie

import (
	"sync"

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/metrics"
	"github.com/yorkie-team/yorkie/yorkie/rpc"
)

// Yorkie is an agent of Yorkie framework.
// The agent receives changes from the client, stores them in the repository,
// and propagates the changes to clients who subscribe to the document.
type Yorkie struct {
	lock sync.Mutex

	conf          *Config
	backend       *backend.Backend
	rpcServer     *rpc.Server
	metricsServer *metrics.Server

	shutdown   bool
	shutdownCh chan struct{}
}

// New creates a new instance of Yorkie.
func New(conf *Config) (*Yorkie, error) {
	be, err := backend.New(conf.Backend, conf.Mongo, conf.ETCD)
	if err != nil {
		return nil, err
	}

	rpcServer, err := rpc.NewServer(conf.RPC, be)
	if err != nil {
		return nil, err
	}

	metricsServer, err := metrics.NewServer(conf.Metrics)
	if err != nil {
		return nil, err
	}

	return &Yorkie{
		conf:          conf,
		backend:       be,
		rpcServer:     rpcServer,
		metricsServer: metricsServer,

		shutdownCh: make(chan struct{}),
	}, nil
}

// Start starts the agent by opening the rpc port.
func (r *Yorkie) Start() error {
	r.lock.Lock()
	defer r.lock.Unlock()

	if r.metricsServer != nil {
		err := r.metricsServer.Start()
		if err != nil {
			log.Logger.Error(err)
			return err
		}
	}
	return r.rpcServer.Start()
}

// Shutdown shuts down this Yorkie agent.
func (r *Yorkie) Shutdown(graceful bool) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.shutdown {
		return nil
	}

	r.rpcServer.Shutdown(graceful)
	if r.metricsServer != nil {
		r.metricsServer.Shutdown(graceful)
	}

	if err := r.backend.Close(); err != nil {
		return err
	}

	close(r.shutdownCh)
	r.shutdown = true
	return nil
}

// ShutdownCh returns the shutdown channel.
func (r *Yorkie) ShutdownCh() <-chan struct{} {
	return r.shutdownCh
}

// RPCAddr returns the address of the RPC.
func (r *Yorkie) RPCAddr() string {
	return r.conf.RPCAddr()
}
