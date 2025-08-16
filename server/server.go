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
	"context"
	gosync "sync"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/profiling"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/server/projects"
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
		conf.Housekeeping,
		metrics,
		conf.Kafka,
		conf.StarRocks,
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

	return &Yorkie{
		conf:            conf,
		backend:         be,
		rpcServer:       rpcServer,
		profilingServer: profilingServer,
		shutdownCh:      make(chan struct{}),
	}, nil
}

// Start starts the server by opening the rpc port.
func (r *Yorkie) Start() error {
	r.lock.Lock()
	defer r.lock.Unlock()

	if err := r.RegisterHousekeepingTasks(r.backend); err != nil {
		return err
	}

	if err := r.backend.Start(context.Background()); err != nil {
		return err
	}

	if r.profilingServer != nil {
		if err := r.profilingServer.Start(); err != nil {
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

// DeactivateClient deactivates the given client. It is used for testing.
func (r *Yorkie) DeactivateClient(ctx context.Context, c1 *client.Client) error {
	project, err := r.DefaultProject(ctx)
	if err != nil {
		return err
	}

	_, err = clients.Deactivate(ctx, r.backend, project, types.ClientRefKey{
		ProjectID: project.ID,
		ClientID:  types.IDFromActorID(c1.ID()),
	})
	return err
}

// CompactDocument compacts the given document. It is used for testing.
func (r *Yorkie) CompactDocument(ctx context.Context, docKey key.Key) error {
	project, err := r.DefaultProject(ctx)
	if err != nil {
		return err
	}

	docInfo, err := documents.FindDocInfoByKey(ctx, r.backend, project, docKey)
	if err != nil {
		return err
	}

	return packs.Compact(ctx, r.backend, project.ID, docInfo)
}

// RegisterHousekeepingTasks registers housekeeping tasks.
func (r *Yorkie) RegisterHousekeepingTasks(be *backend.Backend) error {
	interval, err := be.Housekeeping.Config.ParseInterval()
	if err != nil {
		return err
	}

	lastDeactivateProjectID := database.DefaultProjectID
	if err = be.Housekeeping.RegisterTask(interval, func(ctx context.Context) error {
		lastProjectID, err := clients.DeactivateInactives(
			ctx,
			be,
			be.Housekeeping.Config.CandidatesLimitPerProject,
			be.Housekeeping.Config.ProjectFetchSize,
			lastDeactivateProjectID,
		)
		if err != nil {
			return err
		}

		lastDeactivateProjectID = lastProjectID
		return nil
	}); err != nil {
		return err
	}

	lastCompactionProjectID := database.DefaultProjectID
	if err := be.Housekeeping.RegisterTask(interval, func(ctx context.Context) error {
		lastProjectID, err := documents.CompactDocuments(
			ctx,
			be,
			be.Housekeeping.Config.CandidatesLimitPerProject,
			be.Housekeeping.Config.ProjectFetchSize,
			be.Housekeeping.Config.CompactionMinChanges,
			lastCompactionProjectID,
		)
		if err != nil {
			return err
		}

		lastCompactionProjectID = lastProjectID
		return nil
	}); err != nil {
		return err
	}

	return nil
}

// DefaultProject returns the default project.
func (r *Yorkie) DefaultProject(ctx context.Context) (*types.Project, error) {
	return projects.GetProjectFromAPIKey(ctx, r.backend, "")
}

// CreateProject creates a project with the given name.
func (r *Yorkie) CreateProject(ctx context.Context, name string) (*types.Project, error) {
	project, err := r.DefaultProject(ctx)
	if err != nil {
		return nil, err
	}

	return projects.CreateProject(ctx, r.backend, project.Owner, name)
}
