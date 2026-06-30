/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

package clients

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

const (
	deactivationKey = "housekeeping/deactivation"
)

// CandidatePair represents a pair of Project and Client.
type CandidatePair struct {
	Project *types.Project
	Client  *database.ClientInfo
}

// DeactivateInactives deactivates clients that have not been active for a
// long time using direct client iteration.
//
// When concurrency > 1, per-candidate Deactivate calls are dispatched across
// at most `concurrency` goroutines. Each Deactivate is independent (its own
// DB read + cluster RPC fan-out + atomic DB update), so the parallelism
// shortens per-cycle wallclock without changing per-candidate semantics.
// The atomic $expr guard on DeactivateClient continues to reject candidates
// that became non-trivial between the snapshot and the update.
//
// Note: this changes per-cycle wallclock, not throughput cap. The cycle
// rate (Interval) and batch size (CandidatesLimit) still determine
// throughput; concurrency only frees the per-cycle duration gate so the
// operator can lower Interval or raise CandidatesLimit safely.
func DeactivateInactives(
	ctx context.Context,
	be *backend.Backend,
	candidatesLimit int,
	concurrency int,
	lastClientID types.ID,
) (types.ID, int, int, error) {
	locker, ok := be.Lockers.LockerWithTryLock(deactivationKey)
	if !ok {
		return lastClientID, 0, 0, nil
	}
	defer locker.Unlock()

	lastID, candidates, err := FindDeactivateCandidates(
		ctx,
		be,
		candidatesLimit,
		lastClientID,
	)
	if err != nil {
		return lastClientID, 0, 0, err
	}

	// If no candidates found, reset to beginning for next cycle.
	if len(candidates) == 0 {
		return database.ZeroID, 0, 0, nil
	}

	deactivatedCount := deactivateCandidates(ctx, be, candidates, concurrency)

	return lastID, len(candidates), deactivatedCount, nil
}

// deactivateCandidates dispatches per-candidate Deactivate calls against the
// real backend. Splits sequential vs parallel by `concurrency` and delegates
// the actual dispatch / counting to dispatchDeactivate so the scheduling
// logic stays unit-testable independent of the backend.
func deactivateCandidates(
	ctx context.Context,
	be *backend.Backend,
	candidates []CandidatePair,
	concurrency int,
) int {
	return dispatchDeactivate(ctx, candidates, concurrency, func(c CandidatePair) error {
		_, err := Deactivate(ctx, be, c.Project, c.Client.RefKey())
		return err
	})
}

// dispatchDeactivate runs `deactivate` for each candidate. With concurrency
// <= 1, runs sequentially (legacy behavior). With concurrency > 1, runs up
// to `concurrency` calls in parallel using a weighted semaphore. Errors are
// logged and skipped — only successes increment the returned count.
//
// Pulled out as a separate function so the dispatch behavior (sequential vs
// parallel, count correctness, error skipping) can be unit-tested without a
// full backend.
func dispatchDeactivate(
	ctx context.Context,
	candidates []CandidatePair,
	concurrency int,
	deactivate func(CandidatePair) error,
) int {
	if concurrency <= 1 {
		count := 0
		for _, c := range candidates {
			if err := deactivate(c); err != nil {
				logging.From(ctx).Warnf("failed to deactivate client %s: %v", c.Client.ID, err)
				continue
			}
			count++
		}
		return count
	}

	sem := semaphore.NewWeighted(int64(concurrency))
	var deactivated atomic.Int32
	var wg sync.WaitGroup

	for _, candidate := range candidates {
		if err := sem.Acquire(ctx, 1); err != nil {
			// Context cancelled mid-cycle. Stop dispatching; in-flight
			// goroutines will finish on their own.
			logging.From(ctx).Warnf("acquire deactivate slot: %v", err)
			break
		}
		wg.Add(1)
		go func(c CandidatePair) {
			defer wg.Done()
			defer sem.Release(1)
			if err := deactivate(c); err != nil {
				logging.From(ctx).Warnf("failed to deactivate client %s: %v", c.Client.ID, err)
				return
			}
			deactivated.Add(1)
		}(candidate)
	}
	wg.Wait()

	return int(deactivated.Load())
}

// FindDeactivateCandidates finds candidates to deactivate using cache for projects.
// This improves performance by avoiding complex aggregation queries and using in-memory filtering.
func FindDeactivateCandidates(
	ctx context.Context,
	be *backend.Backend,
	candidatesLimit int,
	lastClientID types.ID,
) (types.ID, []CandidatePair, error) {
	candidates, lastID, err := be.DB.FindActiveClients(ctx, candidatesLimit, lastClientID)
	if err != nil {
		return database.ZeroID, nil, err
	}

	var pairs []CandidatePair
	now := time.Now()

	for _, candidate := range candidates {
		info, err := be.DB.FindProjectInfoByID(ctx, candidate.ProjectID)
		if err != nil {
			return database.ZeroID, nil, err
		}

		// Check if client needs deactivation based on project's threshold
		project := info.ToProject()
		threshold, err := project.ClientDeactivateThresholdAsTimeDuration()
		if err != nil {
			return database.ZeroID, nil, err
		}

		if candidate.UpdatedAt.Before(now.Add(-threshold)) {
			pairs = append(pairs, CandidatePair{
				Project: project,
				Client:  candidate,
			})
		}
	}

	return lastID, pairs, nil
}
