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

package mongo

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

// CacheConfig defines configuration parameters for the cache
type CacheConfig struct {
	// BaseFlushInterval is the base interval for flushing cache to DB
	BaseFlushInterval time.Duration
	// MaxFlushInterval is the maximum interval for flushing cache to DB
	MaxFlushInterval time.Duration
	// MinFlushInterval is the minimum interval for flushing cache to DB
	MinFlushInterval time.Duration
	// TTL is the time-to-live for cached entries
	TTL time.Duration
	// CleanupInterval is the interval for cleaning up expired entries
	CleanupInterval time.Duration
	// MaxCacheSize is the maximum number of cached entries
	MaxCacheSize int
	// WritePressureThreshold is the threshold for write pressure detection
	WritePressureThreshold int
	// PressureCheckInterval is the interval for checking write pressure
	PressureCheckInterval time.Duration
}

// DefaultCacheConfig returns default cache configuration
func DefaultCacheConfig() *CacheConfig {
	return &CacheConfig{
		BaseFlushInterval:      5 * time.Second,
		MaxFlushInterval:       30 * time.Second,
		MinFlushInterval:       1 * time.Second,
		TTL:                    10 * time.Minute,
		CleanupInterval:        1 * time.Minute,
		MaxCacheSize:           1000,
		WritePressureThreshold: 100,
		PressureCheckInterval:  5 * time.Second,
	}
}

// CachedClientInfo wraps ClientInfo with cache-specific metadata
type CachedClientInfo struct {
	ClientInfo *database.ClientInfo
	UpdatedAt  time.Time
	Dirty      bool
	LastFlush  time.Time
	ExpiresAt  time.Time
}

// WritePressure tracks write pressure metrics for adaptive flushing
type WritePressure struct {
	ActiveWrites  int64
	PendingWrites int64
	LastFlushTime time.Time
	FlushInterval time.Duration
	PressureLevel float64
}

// ClientInfoCache manages in-memory caching of ClientInfo objects
type ClientInfoCache struct {
	mu            sync.RWMutex
	cache         map[types.ClientRefKey]*CachedClientInfo
	flushCh       chan struct{}
	stopCh        chan struct{}
	config        *CacheConfig
	client        *Client // Reference to MongoDB client for DB operations
	writePressure *WritePressure
	pressureMu    sync.RWMutex
}

// NewClientInfoCache creates a new ClientInfoCache instance
func NewClientInfoCache(config *CacheConfig, client *Client) *ClientInfoCache {
	if config == nil {
		config = DefaultCacheConfig()
	}

	cache := &ClientInfoCache{
		cache:         make(map[types.ClientRefKey]*CachedClientInfo),
		flushCh:       make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
		config:        config,
		client:        client,
		writePressure: &WritePressure{},
	}

	// Start background goroutines
	go cache.adaptiveFlush()
	go cache.cleanupExpiredEntries()

	return cache
}

// Get retrieves a ClientInfo from the cache
func (c *ClientInfoCache) Get(refKey types.ClientRefKey) *database.ClientInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if cached, exists := c.cache[refKey]; exists {
		// Check if entry has expired
		if time.Now().After(cached.ExpiresAt) {
			return nil
		}
		return cached.ClientInfo
	}

	return nil
}

// GetByKey finds a client by project ID and key
func (c *ClientInfoCache) GetByKey(projectID types.ID, key string) *database.ClientInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for refKey, cached := range c.cache {
		if refKey.ProjectID == projectID && cached.ClientInfo.Key == key {
			return cached.ClientInfo
		}
	}
	return nil
}

// Set stores a ClientInfo in the cache
func (c *ClientInfoCache) Set(refKey types.ClientRefKey, clientInfo *database.ClientInfo) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	c.cache[refKey] = &CachedClientInfo{
		ClientInfo: clientInfo.DeepCopy(),
		UpdatedAt:  now,
		Dirty:      false, // Initially clean
		LastFlush:  now,
		ExpiresAt:  now.Add(c.config.TTL),
	}

	// Evict oldest if cache is full
	if len(c.cache) > c.config.MaxCacheSize {
		if err := c.evictOldest(); err != nil {
			return fmt.Errorf("failed to evict oldest entry: %w", err)
		}
	}
	return nil
}

// UpdateClientInfo updates an existing ClientInfo in the cache, marking it as dirty
func (c *ClientInfoCache) UpdateClientInfo(refKey types.ClientRefKey, clientInfo *database.ClientInfo) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, exists := c.cache[refKey]; exists {
		// Apply $max logic for sequence numbers (same as MongoDB's $max operator)
		merged := c.mergeClientInfo(cached.ClientInfo, clientInfo)
		cached.ClientInfo = merged
		cached.Dirty = true
		cached.UpdatedAt = time.Now()
		cached.ExpiresAt = time.Now().Add(c.config.TTL)
	} else {
		// Add new entry without recursive call
		now := time.Now()
		c.cache[refKey] = &CachedClientInfo{
			ClientInfo: clientInfo.DeepCopy(),
			UpdatedAt:  now,
			Dirty:      true, // Mark as dirty for new entries
			LastFlush:  now,
			ExpiresAt:  now.Add(c.config.TTL),
		}
		// Evict oldest if cache is full
		if len(c.cache) > c.config.MaxCacheSize {
			if err := c.evictOldest(); err != nil {
				return fmt.Errorf("failed to evict oldest entry: %w", err)
			}
		}
	}
	return nil
}

// mergeClientInfo merges two ClientInfo objects, applying $max logic for sequence numbers
// and handling explicit resets for detached/removed documents
func (c *ClientInfoCache) mergeClientInfo(existing, new *database.ClientInfo) *database.ClientInfo {
	merged := existing.DeepCopy()

	// Merge documents using $max logic
	for docID, newDocInfo := range new.Documents {
		if existingDocInfo, exists := merged.Documents[docID]; exists {
			// Reset sequence numbers when document status changes
			if newDocInfo.Status == database.DocumentDetached || newDocInfo.Status == database.DocumentRemoved {
				existingDocInfo.ServerSeq = 0
				existingDocInfo.ClientSeq = 0
				existingDocInfo.Status = newDocInfo.Status
			} else {
				// Apply $max logic for sequence numbers
				if newDocInfo.ServerSeq > existingDocInfo.ServerSeq {
					existingDocInfo.ServerSeq = newDocInfo.ServerSeq
				}
				if newDocInfo.ClientSeq > existingDocInfo.ClientSeq {
					existingDocInfo.ClientSeq = newDocInfo.ClientSeq
				}
				existingDocInfo.Status = newDocInfo.Status
			}
		} else {
			// Add new document info
			merged.Documents[docID] = &database.ClientDocInfo{
				ServerSeq: newDocInfo.ServerSeq,
				ClientSeq: newDocInfo.ClientSeq,
				Status:    newDocInfo.Status,
			}
		}
	}

	// Update other fields
	merged.UpdatedAt = new.UpdatedAt

	return merged
}

// Invalidate removes an entry from the cache
func (c *ClientInfoCache) Invalidate(refKey types.ClientRefKey) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, exists := c.cache[refKey]; exists {
		// Force flush if entry is dirty before removal
		if entry.Dirty {
			if err := c.flushSingleToDB(refKey, entry.ClientInfo); err != nil {
				return fmt.Errorf("failed to flush dirty entry: %w", err)
			}
		}
		delete(c.cache, refKey)
	}
	return nil
}

// InvalidateAll clears all entries from the cache
func (c *ClientInfoCache) InvalidateAll() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Force flush all dirty entries before clearing
	if err := c.FlushToDB(); err != nil {
		return fmt.Errorf("failed to flush all dirty entries: %w", err)
	}
	c.cache = make(map[types.ClientRefKey]*CachedClientInfo)
	return nil
}

// IsDirty checks if an entry needs flushing to DB
func (c *ClientInfoCache) IsDirty(refKey types.ClientRefKey) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if cached, exists := c.cache[refKey]; exists {
		return cached.Dirty
	}
	return false
}

// evictOldest removes the oldest entry from the cache
func (c *ClientInfoCache) evictOldest() error {
	var oldestKey types.ClientRefKey
	var oldestTime time.Time
	var oldestEntry *CachedClientInfo

	for refKey, entry := range c.cache {
		if oldestEntry == nil || entry.UpdatedAt.Before(oldestTime) {
			oldestKey = refKey
			oldestTime = entry.UpdatedAt
			oldestEntry = entry
		}
	}

	if oldestEntry != nil {
		// Force flush if oldest entry is dirty before eviction
		if oldestEntry.Dirty {
			if err := c.flushSingleToDB(oldestKey, oldestEntry.ClientInfo); err != nil {
				return fmt.Errorf("failed to flush oldest dirty entry: %w", err)
			}
		}
		delete(c.cache, oldestKey)
	}
	return nil
}

// adaptiveFlush runs background goroutine for periodic flushing
func (c *ClientInfoCache) adaptiveFlush() {
	ticker := time.NewTicker(c.config.PressureCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.measureWritePressure()
			interval := c.calculateFlushInterval()
			if c.shouldFlush(interval) {
				if err := c.FlushToDB(); err != nil {
					// TODO: Add proper logging
				}
			}
		case <-c.stopCh:
			return
		}
	}
}

// measureWritePressure measures current write pressure
func (c *ClientInfoCache) measureWritePressure() {
	c.mu.RLock()
	pendingWrites := 0
	for _, entry := range c.cache {
		if entry.Dirty {
			pendingWrites++
		}
	}
	c.mu.RUnlock()

	c.pressureMu.Lock()
	c.writePressure.PendingWrites = int64(pendingWrites)

	// Calculate pressure level (0.0 to 1.0)
	if c.config.WritePressureThreshold > 0 {
		c.writePressure.PressureLevel = float64(pendingWrites) / float64(c.config.WritePressureThreshold)
		if c.writePressure.PressureLevel > 1.0 {
			c.writePressure.PressureLevel = 1.0
		}
	}
	c.pressureMu.Unlock()
}

// calculateFlushInterval calculates adaptive flush interval based on pressure
func (c *ClientInfoCache) calculateFlushInterval() time.Duration {
	c.pressureMu.RLock()
	pressureLevel := c.writePressure.PressureLevel
	c.pressureMu.RUnlock()

	// Higher pressure = shorter interval
	ratio := 1.0 - pressureLevel
	interval := c.config.MinFlushInterval +
		time.Duration(float64(c.config.MaxFlushInterval-c.config.MinFlushInterval)*ratio)

	return interval
}

// shouldFlush determines if it's time to flush based on interval and last flush time
func (c *ClientInfoCache) shouldFlush(interval time.Duration) bool {
	c.pressureMu.RLock()
	lastFlush := c.writePressure.LastFlushTime
	c.pressureMu.RUnlock()

	return time.Since(lastFlush) >= interval
}

// cleanupExpiredEntries runs background goroutine for periodic cleanup
func (c *ClientInfoCache) cleanupExpiredEntries() {
	ticker := time.NewTicker(c.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := c.cleanupExpired(); err != nil {
				logging.DefaultLogger().Errorf("cleanup expired entries failed: %v", err)
				// Continue to prevent blocking the cleanup goroutine
			}
		case <-c.stopCh:
			return
		}
	}
}

// cleanupExpired removes expired entries from the cache
func (c *ClientInfoCache) cleanupExpired() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	expiredCount := 0

	for refKey, entry := range c.cache {
		if now.After(entry.ExpiresAt) {
			// Force flush if expired entry is dirty before removal
			if entry.Dirty {
				if err := c.flushSingleToDB(refKey, entry.ClientInfo); err != nil {
					return fmt.Errorf("failed to flush expired dirty entry: %w", err)
				}
			}
			delete(c.cache, refKey)
			expiredCount++
		}
	}

	if expiredCount > 0 {
		logging.DefaultLogger().Infof("cleaned up %d expired cache entries", expiredCount)
	}
	return nil
}

// ExtendTTL extends the TTL for a specific cached entry
func (c *ClientInfoCache) ExtendTTL(refKey types.ClientRefKey) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, exists := c.cache[refKey]; exists {
		cached.ExpiresAt = time.Now().Add(c.config.TTL)
	}
}

// GetExpirationTime returns the expiration time for a cached entry
func (c *ClientInfoCache) GetExpirationTime(refKey types.ClientRefKey) (time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if cached, exists := c.cache[refKey]; exists {
		return cached.ExpiresAt, true
	}
	return time.Time{}, false
}

// FlushToDB flushes dirty entries to the database
func (c *ClientInfoCache) FlushToDB() error {
	c.mu.Lock()
	dirtyClients := make(map[types.ClientRefKey]*database.ClientInfo)

	for refKey, cached := range c.cache {
		if cached.Dirty {
			dirtyClients[refKey] = cached.ClientInfo
			cached.Dirty = false
			cached.LastFlush = time.Now()
		}
	}
	c.mu.Unlock()

	if len(dirtyClients) == 0 {
		return nil
	}

	// Build bulk update operations
	updates := make([]mongo.WriteModel, 0, len(dirtyClients))
	for refKey, clientInfo := range dirtyClients {
		update := c.buildUpdateModel(refKey, clientInfo)
		updates = append(updates, update)
	}

	// Execute bulk update
	if len(updates) > 0 {
		_, err := c.client.collection(ColClients).BulkWrite(context.Background(), updates)
		if err != nil {
			return fmt.Errorf("bulk write client info: %w", err)
		}
	}

	// Update last flush time
	c.pressureMu.Lock()
	c.writePressure.LastFlushTime = time.Now()
	c.pressureMu.Unlock()

	return nil
}

// buildUpdateModel builds a MongoDB update model for the given client info
func (c *ClientInfoCache) buildUpdateModel(refKey types.ClientRefKey,
	clientInfo *database.ClientInfo) mongo.WriteModel {
	// Build the update document
	updateDoc := bson.M{
		"$set": bson.M{
			"updated_at": clientInfo.UpdatedAt,
		},
		"$max": bson.M{},
	}

	// Add document-specific updates
	for docID, docInfo := range clientInfo.Documents {
		docKey := clientDocInfoKey(docID, "server_seq")
		updateDoc["$max"].(bson.M)[docKey] = docInfo.ServerSeq

		docKey = clientDocInfoKey(docID, "client_seq")
		updateDoc["$max"].(bson.M)[docKey] = docInfo.ClientSeq

		docKey = clientDocInfoKey(docID, StatusKey)
		updateDoc["$set"].(bson.M)[docKey] = docInfo.Status
	}

	// Update attached_docs array with correct state
	attachedDocs := clientInfo.AttachedDocuments()
	updateDoc["$set"].(bson.M)["attached_docs"] = attachedDocs

	return mongo.NewUpdateOneModel().
		SetFilter(bson.M{
			"project_id": refKey.ProjectID,
			"_id":        refKey.ClientID,
		}).
		SetUpdate(updateDoc)
}

// flushSingleToDB flushes a single client info entry to the database
func (c *ClientInfoCache) flushSingleToDB(refKey types.ClientRefKey, clientInfo *database.ClientInfo) error {
	update := c.buildUpdateModel(refKey, clientInfo)

	_, err := c.client.collection(ColClients).BulkWrite(context.Background(), []mongo.WriteModel{update})
	if err != nil {
		return fmt.Errorf("flush single client info: %w", err)
	}

	return nil
}

// Close stops the cache and flushes any remaining dirty entries
func (c *ClientInfoCache) Close() error {
	close(c.stopCh)

	// Flush any remaining dirty entries
	return c.FlushToDB()
}
