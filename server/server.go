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

// Package server provides the Yorkie server which is the main entry point of the
// Yorkie system. The server is responsible for starting the RPC server and
// admin server.
package server

import (
	gosync "sync"

	"github.com/yorkie-team/yorkie/server/admin"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/profiling"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/server/rpc"
)

// Yorkie is a server of Yorkie.
// The server receives changes from the client, stores them in the repository,
// and propagates the changes to clients who subscribe to the document.
type Yorkie struct {
	lock gosync.Mutex

	conf            *Config
	backend         *backend.Backend
	rpcServer       *rpc.Server
	adminServer     *admin.Server
	profilingServer *profiling.Server

	shutdown   bool
	shutdownCh chan struct{}
}

// New creates a new instance of Yorkie.
func New(conf *Config) (*Yorkie, error) {
	if err := conf.Validate(); err != nil {
		return nil, err
	}

	metrics, err := prometheus.NewMetrics()
	if err != nil {
		return nil, err
	}

	be, err := backend.New(
		conf.Backend,
		conf.Mongo,
		conf.ETCD,
		conf.Housekeeping,
		conf.AdminAddr(),
		metrics,
	)
	if err != nil {
		return nil, err
	}

	rpcServer, err := rpc.NewServer(conf.RPC, be)
	if err != nil {
		return nil, err
	}

	var profilingServer *profiling.Server
	if conf.Profiling != nil {
		profilingServer = profiling.NewServer(conf.Profiling, metrics)
	}

	adminServer := admin.NewServer(conf.Admin, be)

	return &Yorkie{
		conf:            conf,
		backend:         be,
		rpcServer:       rpcServer,
		profilingServer: profilingServer,
		adminServer:     adminServer,
		shutdownCh:      make(chan struct{}),
	}, nil
}

// Start starts the server by opening the rpc port.
func (r *Yorkie) Start() error {
	r.lock.Lock()
	defer r.lock.Unlock()

	if err := r.adminServer.Start(); err != nil {
		return err
	}

	if r.profilingServer != nil {
		err := r.profilingServer.Start()
		if err != nil {
			logging.DefaultLogger().Error(err)
			return err
		}
	}
	return r.rpcServer.Start()
}

// Shutdown shuts down this Yorkie server.
func (r *Yorkie) Shutdown(graceful bool) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.shutdown {
		return nil
	}

	r.rpcServer.Shutdown(graceful)
	if r.profilingServer != nil {
		r.profilingServer.Shutdown(graceful)
	}

	r.adminServer.Shutdown(graceful)

	if err := r.backend.Shutdown(); err != nil {
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

// AdminAddr returns the address of the admin server.
func (r *Yorkie) AdminAddr() string {
	return r.conf.AdminAddr()
}

// Members returns the members of this cluster.
func (r *Yorkie) Members() map[string]*sync.ServerInfo {
	return r.backend.Members()
}
