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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	pkgtypes "github.com/yorkie-team/yorkie/pkg/types"
)

// Manager manages all caches used in the backend.
type Manager struct {
	// AuthWebhook is used to cache the response of the auth webhook.
	// Use our expirable wrapper that exposes statistics.
	AuthWebhook *cache.LRUWithExpires[string, pkgtypes.Pair[int, *types.AuthWebhookResponse]]

	// Snapshot is used to cache the snapshot information.
	Snapshot *cache.LRU[types.DocRefKey, *document.InternalDocument]

	// SessionCount is used to cache the session count of channels
	// to reduce RPC calls between AdminServer and ClusterServer.
	SessionCount *cache.LRUWithExpires[string, int64]

	// OversizedCompaction remembers, per document, the server seq at which
	// compaction failed because the compacted change would not fit in one
	// record. Housekeeping skips the document until its server seq moves,
	// instead of rebuilding it every cycle to fail the same way.
	OversizedCompaction *cache.LRU[types.DocRefKey, int64]

	// PresenceBase holds, per document and client, the full presence the
	// client last pushed, so a presence patch can be folded into a full put.
	// The LRU bounds entries, not bytes: the writer (packs.presenceFold) is
	// what keeps one entry small, dropping a presence over
	// maxPresenceBaseSize instead of caching it, so the cache as a whole is
	// bounded by PresenceBaseCacheSize times that.
	// See docs/design/presence-patch.md.
	PresenceBase *cache.LRU[PresenceBaseKey, PresenceBase]
}

// PresenceBaseKey identifies the presence of one client in one document.
type PresenceBaseKey struct {
	DocRefKey types.DocRefKey
	ClientID  types.ID
}

// PresenceBase is the full presence a client held when its checkpoint in the
// document was Checkpoint, in Epoch. It is a valid base for the client's next
// push only while the client's checkpoint and epoch are still those: any push
// or pull that went through another server moves one of them. A detach does
// not -- it zeroes the checkpoint to the value a fresh attach seeds -- so the
// writer drops the entry when the client is no longer attached, and never
// stores one at the initial checkpoint.
type PresenceBase struct {
	Checkpoint change.Checkpoint
	Epoch      int64
	Presence   presence.Data
}

// oversizedCompactionCacheSize bounds OversizedCompaction. An entry is a key
// and an int64; evicting one only costs that document one more rebuild.
const oversizedCompactionCacheSize = 10000

// Options contains configuration for cache manager.
type Options struct {
	// Auth related cache options
	AuthWebhookCacheSize int
	AuthWebhookCacheTTL  time.Duration

	// Document related cache options
	SnapshotCacheSize     int
	PresenceBaseCacheSize int

	// Channel related cache options
	ChannelSessionCountCacheSize int
	ChannelSessionCountCacheTTL  time.Duration
}

// New creates a new cache manager.
func New(opts Options) (*Manager, error) {
	authWebhookCache, err := cache.NewLRUWithExpires[string, pkgtypes.Pair[int, *types.AuthWebhookResponse]](
		opts.AuthWebhookCacheSize,
		opts.AuthWebhookCacheTTL,
		"auth-webhook",
	)
	if err != nil {
		return nil, err
	}

	snapshotCache, err := cache.NewLRU[types.DocRefKey, *document.InternalDocument](
		opts.SnapshotCacheSize,
		"snapshots",
	)
	if err != nil {
		return nil, err
	}

	sessionCountCache, err := cache.NewLRUWithExpires[string, int64](
		opts.ChannelSessionCountCacheSize,
		opts.ChannelSessionCountCacheTTL,
		"session-count",
	)
	if err != nil {
		return nil, err
	}

	oversizedCompactionCache, err := cache.NewLRU[types.DocRefKey, int64](
		oversizedCompactionCacheSize,
		"oversized-compaction",
	)
	if err != nil {
		return nil, err
	}

	presenceBaseCache, err := cache.NewLRU[PresenceBaseKey, PresenceBase](
		opts.PresenceBaseCacheSize,
		"presence-base",
	)
	if err != nil {
		return nil, err
	}

	m := &Manager{
		AuthWebhook:         authWebhookCache,
		Snapshot:            snapshotCache,
		SessionCount:        sessionCountCache,
		OversizedCompaction: oversizedCompactionCache,
		PresenceBase:        presenceBaseCache,
	}
	return m, nil
}
