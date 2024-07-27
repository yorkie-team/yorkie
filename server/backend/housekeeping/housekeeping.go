/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package housekeeping

import (
	"context"
	"fmt"
	"time"

	"github.com/go-co-op/gocron/v2"

	"github.com/yorkie-team/yorkie/server/logging"
)

const ( // existing commit
	deactivateCandidatesKey     = "housekeeping/deactivateCandidates"
	documentHardDeletionLockKey = "housekeeping/DocumentHardDeletionLock"
)

// Housekeeping is the housekeeping service. It periodically runs housekeeping
// tasks.
type Housekeeping struct {
	Config *Config
	/* existing commit

	database    database.Database
	coordinator sync.Coordinator

	intervalDeactivateCandidates                 time.Duration
	intervalDeleteDocuments                      time.Duration
	documentHardDeletionGracefulPeriod           time.Duration
	clientDeactivationCandidateLimitPerProject   int
	DocumentHardDeletionCandidateLimitPerProject int
	projectFetchSize                             int
	*/
	scheduler gocron.Scheduler
}

// New creates a new housekeeping instance.
func New(conf *Config) (*Housekeeping, error) {
	scheduler, err := gocron.NewScheduler()
	/* existing commit
	func New(
		conf *Config,
		database database.Database,
		coordinator sync.Coordinator,
	) (*Housekeeping, error) {
		intervalDeactivateCandidates, err := time.ParseDuration(conf.IntervalDeactivateCandidates)
		if err != nil {
			return nil, fmt.Errorf("parse intervalDeactivateCandidates %s: %w",
				conf.IntervalDeactivateCandidates, err)
		}

		intervalDeleteDocuments, err := time.ParseDuration(conf.IntervalDeleteDocuments)
		if err != nil {
	*/
	return nil, fmt.Errorf("new scheduler: %w", err)
	/* existing commit
		return nil, fmt.Errorf("parse intervalDeleteDocuments %s: %w", conf.IntervalDeleteDocuments, err)
	}

	documentHardDeletionGracefulPeriod, err := time.ParseDuration(conf.DocumentHardDeletionGracefulPeriod)
	if err != nil {
		return nil, fmt.Errorf("parse documentHardDeletionGracefulPeriod %s: %w",
			conf.DocumentHardDeletionGracefulPeriod, err)
	}
	*/
	return &Housekeeping{
		Config:    conf,
		scheduler: scheduler,
		/* existing commit
		database:    database,
		coordinator: coordinator,

		intervalDeactivateCandidates:                 intervalDeactivateCandidates,
		intervalDeleteDocuments:                      intervalDeleteDocuments,
		documentHardDeletionGracefulPeriod:           documentHardDeletionGracefulPeriod,
		clientDeactivationCandidateLimitPerProject:   conf.ClientDeactivationCandidateLimitPerProject,
		DocumentHardDeletionCandidateLimitPerProject: conf.DocumentHardDeletionCandidateLimitPerProject,
		projectFetchSize:                             conf.ProjectFetchSize,

		ctx:        ctx,
		cancelFunc: cancelFunc,
		*/
	}, nil
}

// RegisterTask registers task the housekeeping service.
func (h *Housekeeping) RegisterTask(
	interval time.Duration,
	task func(ctx context.Context) error,
) error {
	if _, err := h.scheduler.NewJob(
		gocron.DurationJob(interval),
		gocron.NewTask(func() {
			ctx := context.Background()
			if err := task(ctx); err != nil {
				logging.From(ctx).Error(err)
			}
		}),
	); err != nil {
		return fmt.Errorf("scheduler new job: %w", err)
	}

	return nil
}

// Start starts the housekeeping service.
func (h *Housekeeping) Start() error {
	h.scheduler.Start()
	/* existing commit
	go h.AttachDeactivateCandidates()밋
	go h.AttachDocumentHardDeletion()
	*/
	return nil
}

// Stop stops the housekeeping service.
func (h *Housekeeping) Stop() error {
	if err := h.scheduler.StopJobs(); err != nil {
		return fmt.Errorf("scheduler stop jobs: %w", err)
	}

	if err := h.scheduler.Shutdown(); err != nil {
		return fmt.Errorf("scheduler shutdown: %w", err)
	}
	/* existing commit
	h.cancelFunc()
	*/
	return nil
}

/* existing commit
// AttachDeactivateCandidates is the housekeeping loop for DeactivateCandidates
func (h *Housekeeping) AttachDeactivateCandidates() {
	housekeepingLastProjectID := database.DefaultProjectID

	for {
		ctx := context.Background()
		lastProjectID, err := h.deactivateCandidates(ctx, housekeepingLastProjectID)
		if err != nil {
			logging.From(ctx).Error(err)
			continue
		}

		housekeepingLastProjectID = lastProjectID

		select {
		case <-time.After(h.intervalDeactivateCandidates):
		case <-h.ctx.Done():
			return
		}
	}
}

// AttachDocumentHardDeletion is the housekeeping loop for DocumentHardDeletion
func (h *Housekeeping) AttachDocumentHardDeletion() {
	housekeepingLastProjectID := database.DefaultProjectID

	for {
		ctx := context.Background()
		lastProjectID, err := h.DeleteDocument(ctx, housekeepingLastProjectID)
		if err != nil {
			logging.From(ctx).Error(err)
			continue
		}

		housekeepingLastProjectID = lastProjectID

		select {
		case <-time.After(h.intervalDeleteDocuments):
		case <-h.ctx.Done():
			return
		}
	}
}

// DeleteDocument deletes a document
func (h *Housekeeping) DeleteDocument(
	ctx context.Context,
	housekeepingLastProjectID types.ID,
) (types.ID, error) {
	start := time.Now()
	locker, err := h.coordinator.NewLocker(ctx, documentHardDeletionLockKey)
	if err != nil {
		return database.DefaultProjectID, err
	}

	if err := locker.Lock(ctx); err != nil {
		return database.DefaultProjectID, err
	}

	defer func() {
		if err := locker.Unlock(ctx); err != nil {
			logging.From(ctx).Error(err)
		}
	}()

	lastProjectID, candidates, err := h.FindDocumentHardDeletionCandidates(
		ctx,
		h.DocumentHardDeletionCandidateLimitPerProject,
		h.projectFetchSize,
		h.documentHardDeletionGracefulPeriod,
		housekeepingLastProjectID,
	)

	if err != nil {
		return database.DefaultProjectID, err
	}

	deletedDocumentsCount, err := h.database.DeleteDocument(ctx, candidates)

	if err != nil {
		return database.DefaultProjectID, err
	}

	if len(candidates) > 0 {
		logging.From(ctx).Infof(
			"HSKP: candidates %d, hard deleted %d, %s",
			len(candidates),
			deletedDocumentsCount,
			time.Since(start),
		)
	}

	return lastProjectID, nil
}

// deactivateCandidates deactivates candidates.
func (h *Housekeeping) deactivateCandidates(
	ctx context.Context,
	housekeepingLastProjectID types.ID,
) (types.ID, error) {
	start := time.Now()
	locker, err := h.coordinator.NewLocker(ctx, deactivateCandidatesKey)
	if err != nil {
		return database.DefaultProjectID, err
	}

	if err := locker.Lock(ctx); err != nil {
		return database.DefaultProjectID, err
	}

	defer func() {
		if err := locker.Unlock(ctx); err != nil {
			logging.From(ctx).Error(err)
		}
	}()

	lastProjectID, candidates, err := h.FindDeactivateCandidates(
		ctx,
		h.clientDeactivationCandidateLimitPerProject,
		h.projectFetchSize,
		housekeepingLastProjectID,
	)
	if err != nil {
		return database.DefaultProjectID, err
	}

	deactivatedCount := 0
	for _, clientInfo := range candidates {
		if _, err := clients.Deactivate(
			ctx,
			h.database,
			clientInfo.RefKey(),
		); err != nil {
			return database.DefaultProjectID, err
		}

		deactivatedCount++
	}

	if len(candidates) > 0 {
		logging.From(ctx).Infof(
			"HSKP: candidates %d, deactivated %d, %s",
			len(candidates),
			deactivatedCount,
			time.Since(start),
		)
	}

	return lastProjectID, nil
}

// FindDeactivateCandidates finds the housekeeping candidates.
func (h *Housekeeping) FindDeactivateCandidates(
	ctx context.Context,
	clientDeactivationCandidateLimitPerProject int,
	projectFetchSize int,
	lastProjectID types.ID,
) (types.ID, []*database.ClientInfo, error) {
	projects, err := h.database.FindNextNCyclingProjectInfos(ctx, projectFetchSize, lastProjectID)
	if err != nil {
		return database.DefaultProjectID, nil, err
	}

	var candidates []*database.ClientInfo
	for _, project := range projects {
		infos, err := h.database.FindDeactivateCandidatesPerProject(ctx, project, clientDeactivationCandidateLimitPerProject)
		if err != nil {
			return database.DefaultProjectID, nil, err
		}

		candidates = append(candidates, infos...)
	}

	var topProjectID types.ID
	if len(projects) < projectFetchSize {
		topProjectID = database.DefaultProjectID
	} else {
		topProjectID = projects[len(projects)-1].ID
	}

	return topProjectID, candidates, nil
}

// FindDocumentHardDeletionCandidates finds the clients that need housekeeping.
func (h *Housekeeping) FindDocumentHardDeletionCandidates(
	ctx context.Context,
	documentHardDeletionCandidateLimitPerProject int,
	projectFetchSize int,
	deletedAfterTime time.Duration,
	lastProjectID types.ID,
) (types.ID, []*database.DocInfo, error) {
	projects, err := h.database.FindNextNCyclingProjectInfos(ctx, projectFetchSize, lastProjectID)
	if err != nil {
		return database.DefaultProjectID, nil, err
	}

	var candidates []*database.DocInfo
	for _, project := range projects {
		infos, err := h.database.FindDocumentHardDeletionCandidatesPerProject(
			ctx,
			project,
			documentHardDeletionCandidateLimitPerProject,
			deletedAfterTime,
		)
		if err != nil {
			return database.DefaultProjectID, nil, err
		}

		candidates = append(candidates, infos...)
	}

	var topProjectID types.ID
	if len(projects) < projectFetchSize {
		topProjectID = database.DefaultProjectID
	} else {
		topProjectID = projects[len(projects)-1].ID
	}

	return topProjectID, candidates, nil
}
*/
