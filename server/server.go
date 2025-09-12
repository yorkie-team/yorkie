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
	"time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/profiling"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc"
)

// housekeepingState is a common structure to hold the state of housekeeping tasks.
type housekeepingState struct {
	gosync.Mutex
	lastID          types.ID
	term            int
	totalCandidates int
	totalProcessed  int
}

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

// FindActiveClusterNodes returns nodes considered active within the given time window.
// It is used for testing.
func (r *Yorkie) FindActiveClusterNodes(
	ctx context.Context,
	renewalInterval time.Duration,
) ([]*database.ClusterNodeInfo, error) {
	return r.backend.DB.FindActiveClusterNodes(ctx, renewalInterval)
}

// ClearClusterNodes removes the current clusternode information for testing purposes.
func (r *Yorkie) ClearClusterNodes(ctx context.Context) error {
	return r.backend.DB.ClearClusterNodes(ctx)
}

// FindLeadership returns the current leadership information for testing purposes.
func (r *Yorkie) FindLeadership(ctx context.Context) (*database.ClusterNodeInfo, error) {
	return r.backend.DB.FindLeadership(ctx)
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

	deactivateState := &housekeepingState{lastID: database.ZeroID}
	compactionState := &housekeepingState{lastID: database.ZeroID}

	if err = be.Housekeeping.RegisterTask(interval, func(ctx context.Context) error {
		deactivateState.Lock()
		currentLastID := deactivateState.lastID
		deactivateState.Unlock()

		isNewTerm := currentLastID == database.ZeroID

		start := time.Now()
		lastID, candidatesCount, processedCount, err := clients.DeactivateInactives(
			ctx,
			be,
			be.Housekeeping.Config.CandidatesLimit,
			currentLastID,
		)
		if err != nil {
			return err
		}

		deactivateState.Lock()
		if isNewTerm {
			deactivateState.term++
		}

		deactivateState.lastID = lastID
		deactivateState.totalCandidates += candidatesCount
		deactivateState.totalProcessed += processedCount

		if processedCount > 0 {
			logging.From(ctx).Infof(
				"HSKP: deactivation #%d %s candidates %d/%d deactivated %d/%d %s",
				deactivateState.term,
				currentLastID,
				candidatesCount,
				deactivateState.totalCandidates,
				processedCount,
				deactivateState.totalProcessed,
				time.Since(start),
			)
		}

		deactivateState.Unlock()
		return nil
	}); err != nil {
		return err
	}

	if err := be.Housekeeping.RegisterTask(interval, func(ctx context.Context) error {
		compactionState.Lock()
		currentLastID := compactionState.lastID
		compactionState.Unlock()

		isNewTerm := currentLastID == database.ZeroID

		start := time.Now()
		lastID, candidatesCount, processedCount, err := documents.CompactDocuments(
			ctx,
			be,
			be.Housekeeping.Config.CandidatesLimit,
			be.Housekeeping.Config.CompactionMinChanges,
			currentLastID,
		)
		if err != nil {
			return err
		}

		compactionState.Lock()
		if isNewTerm {
			compactionState.term++
		}

		compactionState.lastID = lastID
		compactionState.totalCandidates += candidatesCount
		compactionState.totalProcessed += processedCount

		if processedCount > 0 {
			logging.From(ctx).Infof(
				"HSKP: compaction #%d %s candidates %d/%d compacted %d/%d %s",
				compactionState.term,
				currentLastID,
				candidatesCount,
				compactionState.totalCandidates,
				processedCount,
				compactionState.totalProcessed,
				time.Since(start),
			)
		}

		compactionState.Unlock()
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
