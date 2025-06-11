/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

// Package cache provides cache management for Yorkie backend.
package cache

import (
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	pkgtypes "github.com/yorkie-team/yorkie/pkg/types"
)

// Manager manages all caches used in the backend.
type Manager struct {
	// AuthWebhook is used to cache the response of the auth webhook.
	AuthWebhook *expirable.LRU[string, pkgtypes.Pair[int, *types.AuthWebhookResponse]]

	// Project is used to cache the project information.
	Project *expirable.LRU[string, *types.Project]

	// Snapshot is used to cache the snapshot information.
	Snapshot *lru.Cache[types.DocRefKey, *document.InternalDocument]
}

// Options contains configuration for cache manager.
type Options struct {
	// Auth related cache options
	AuthWebhookCacheSize int
	AuthWebhookCacheTTL  time.Duration

	// Project related cache options
	ProjectCacheSize int
	ProjectCacheTTL  time.Duration

	// Document related cache options
	SnapshotCacheSize int
}

// New creates a new cache manager.
func New(opts Options) (*Manager, error) {
	authWebhookCache := expirable.NewLRU[string, pkgtypes.Pair[int, *types.AuthWebhookResponse]](
		opts.AuthWebhookCacheSize,
		nil,
		opts.AuthWebhookCacheTTL,
	)

	projectCache := expirable.NewLRU[string, *types.Project](
		opts.ProjectCacheSize,
		nil,
		opts.ProjectCacheTTL,
	)

	snapshotCache, err := lru.New[types.DocRefKey, *document.InternalDocument](
		opts.SnapshotCacheSize,
	)
	if err != nil {
		return nil, err
	}

	return &Manager{
		AuthWebhook: authWebhookCache,
		Project:     projectCache,
		Snapshot:    snapshotCache,
	}, nil
}
