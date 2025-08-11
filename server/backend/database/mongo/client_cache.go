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

// CacheMetrics tracks performance metrics for the cache
type CacheMetrics struct {
	// Overall cache performance
	TotalHits   int64
	TotalMisses int64

	// Operation-specific hit rates
	ActivateClientHits     int64
	ActivateClientMisses   int64
	DeactivateClientHits   int64
	DeactivateClientMisses int64
	TryAttachingHits       int64
	TryAttachingMisses     int64
	FindClientInfoHits     int64
	FindClientInfoMisses   int64
}

// CacheConfig defines configuration parameters for the cache
type CacheConfig struct {
	// BaseFlushInterval is the base interval for flushing cache to DB
	BaseFlushInterval time.Duration
	// MaxFlushInterval is the maximum interval for flushing cache to DB
	MaxFlushInterval time.Duration
	// MinFlushInterval is the minimum interval for flushing cache to DB
	MinFlushInterval time.Duration
	// MaxCacheSize is the maximum number of cached entries
	MaxCacheSize int
	// WritePressureThreshold is the threshold for write pressure detection
	WritePressureThreshold int
	// PressureCheckInterval is the interval for checking write pressure
	PressureCheckInterval time.Duration
	// EnableFlushCleanup enables cache cleanup after flush
	EnableFlushCleanup bool
}

// DefaultCacheConfig returns default cache configuration
func DefaultCacheConfig() *CacheConfig {
	return &CacheConfig{
		BaseFlushInterval:      10 * time.Second,
		MaxFlushInterval:       60 * time.Second,
		MinFlushInterval:       5 * time.Second,
		MaxCacheSize:           20000,
		WritePressureThreshold: 500,
		PressureCheckInterval:  10 * time.Second,
		EnableFlushCleanup:     true,
	}
}

// CachedClientInfo wraps ClientInfo with cache-specific metadata
type CachedClientInfo struct {
	ClientInfo *database.ClientInfo
	UpdatedAt  time.Time
	Dirty      bool
	LastFlush  time.Time
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
	metrics       *CacheMetrics
	metricsMu     sync.RWMutex
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
		metrics:       &CacheMetrics{},
	}

	// Start background goroutines
	go cache.adaptiveFlush()

	// Start metrics logging
	cache.StartMetricsLogging()

	return cache
}

// Get retrieves a ClientInfo from the cache
func (c *ClientInfoCache) Get(refKey types.ClientRefKey) *database.ClientInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if cached, exists := c.cache[refKey]; exists {
		c.recordHit()
		return cached.ClientInfo.DeepCopy()
	}

	c.recordMiss()
	return nil
}

// GetByKey retrieves a ClientInfo from the cache by project ID and key
func (c *ClientInfoCache) GetByKey(projectID types.ID, key string) *database.ClientInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for refKey, cached := range c.cache {
		if refKey.ProjectID == projectID && cached.ClientInfo.Key == key {
			c.recordHit()
			return cached.ClientInfo.DeepCopy()
		}
	}

	c.recordMiss()
	return nil
}

// GetByProject retrieves all ClientInfo for a specific project from the cache
func (c *ClientInfoCache) GetByProject(projectID types.ID) []*database.ClientInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var clients []*database.ClientInfo
	for refKey, cached := range c.cache {
		if refKey.ProjectID == projectID {
			clients = append(clients, cached.ClientInfo.DeepCopy())
		}
	}
	return clients
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
	}

	// Evict oldest if cache is full
	if len(c.cache) > c.config.MaxCacheSize {
		if err := c.evictOldest(); err != nil {
			return fmt.Errorf("failed to evict oldest entry: %w", err)
		}
	}
	return nil
}

// UpdateClientInfo updates an existing cached ClientInfo with new data
func (c *ClientInfoCache) UpdateClientInfo(refKey types.ClientRefKey, clientInfo *database.ClientInfo) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, exists := c.cache[refKey]; exists {
		// Apply $max logic for sequence numbers (same as MongoDB's $max operator)
		merged := c.mergeClientInfo(cached.ClientInfo, clientInfo)
		cached.ClientInfo = merged
		cached.Dirty = true
		cached.UpdatedAt = time.Now()
		return nil
	}

	now := time.Now()
	c.cache[refKey] = &CachedClientInfo{
		ClientInfo: clientInfo.DeepCopy(),
		UpdatedAt:  now,
		Dirty:      true,
		LastFlush:  now,
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
	merged.Status = new.Status
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

// cleanupAfterFlush removes flushed entries from cache
func (c *ClientInfoCache) cleanupAfterFlush() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Remove entries that were just flushed (Dirty = false and recently flushed)
	removedCount := 0
	now := time.Now()

	for refKey, entry := range c.cache {
		// Remove entries that were just flushed (not dirty and recently flushed)
		if !entry.Dirty && now.Sub(entry.LastFlush) < time.Second {
			delete(c.cache, refKey)
			removedCount++
		}
	}

	// Log cleanup metrics
	if removedCount > 0 {
		logging.DefaultLogger().Infof(
			"Cache cleanup after flush: removed %d flushed entries",
			removedCount,
		)
	}
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
					logging.DefaultLogger().Error("flush to db: " + err.Error())
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

	// Cleanup cache after successful flush
	if c.config.EnableFlushCleanup {
		c.cleanupAfterFlush()
	}

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

// StartMetricsLogging starts periodic metrics logging
func (c *ClientInfoCache) StartMetricsLogging() {
	go c.periodicMetricsLogging()
}

// periodicMetricsLogging logs metrics periodically
func (c *ClientInfoCache) periodicMetricsLogging() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.LogMetrics()
		case <-c.stopCh:
			return
		}
	}
}

// Metrics recording functions
func (c *ClientInfoCache) recordHit() {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	c.metrics.TotalHits++
}

func (c *ClientInfoCache) recordMiss() {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	c.metrics.TotalMisses++
}

func (c *ClientInfoCache) recordActivateClientMiss() {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	c.metrics.ActivateClientMisses++
	c.metrics.TotalMisses++
}

func (c *ClientInfoCache) recordDeactivateClientHit() {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	c.metrics.DeactivateClientHits++
	c.metrics.TotalHits++
}

func (c *ClientInfoCache) recordDeactivateClientMiss() {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	c.metrics.DeactivateClientMisses++
	c.metrics.TotalMisses++
}

func (c *ClientInfoCache) recordTryAttachingHit() {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	c.metrics.TryAttachingHits++
	c.metrics.TotalHits++
}

func (c *ClientInfoCache) recordTryAttachingMiss() {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	c.metrics.TryAttachingMisses++
	c.metrics.TotalMisses++
}

func (c *ClientInfoCache) recordFindClientInfoHit() {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	c.metrics.FindClientInfoHits++
	c.metrics.TotalHits++
}

func (c *ClientInfoCache) recordFindClientInfoMiss() {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	c.metrics.FindClientInfoMisses++
	c.metrics.TotalMisses++
}

// GetMetrics returns current cache metrics
func (c *ClientInfoCache) GetMetrics() *CacheMetrics {
	c.metricsMu.RLock()
	defer c.metricsMu.RUnlock()

	// Return a copy to avoid race conditions
	return &CacheMetrics{
		TotalHits:              c.metrics.TotalHits,
		TotalMisses:            c.metrics.TotalMisses,
		ActivateClientHits:     c.metrics.ActivateClientHits,
		ActivateClientMisses:   c.metrics.ActivateClientMisses,
		DeactivateClientHits:   c.metrics.DeactivateClientHits,
		DeactivateClientMisses: c.metrics.DeactivateClientMisses,
		TryAttachingHits:       c.metrics.TryAttachingHits,
		TryAttachingMisses:     c.metrics.TryAttachingMisses,
		FindClientInfoHits:     c.metrics.FindClientInfoHits,
		FindClientInfoMisses:   c.metrics.FindClientInfoMisses,
	}
}

// LogMetrics logs current cache metrics
func (c *ClientInfoCache) LogMetrics() {
	metrics := c.GetMetrics()

	// Calculate overall hit rate
	totalRequests := metrics.TotalHits + metrics.TotalMisses
	overallHitRate := 0.0
	if totalRequests > 0 {
		overallHitRate = float64(metrics.TotalHits) / float64(totalRequests) * 100
	}

	// Calculate operation-specific hit rates
	activateTotal := metrics.ActivateClientHits + metrics.ActivateClientMisses
	activateHitRate := 0.0
	if activateTotal > 0 {
		activateHitRate = float64(metrics.ActivateClientHits) / float64(activateTotal) * 100
	}

	deactivateTotal := metrics.DeactivateClientHits + metrics.DeactivateClientMisses
	deactivateHitRate := 0.0
	if deactivateTotal > 0 {
		deactivateHitRate = float64(metrics.DeactivateClientHits) / float64(deactivateTotal) * 100
	}

	tryAttachingTotal := metrics.TryAttachingHits + metrics.TryAttachingMisses
	tryAttachingHitRate := 0.0
	if tryAttachingTotal > 0 {
		tryAttachingHitRate = float64(metrics.TryAttachingHits) / float64(tryAttachingTotal) * 100
	}

	findClientTotal := metrics.FindClientInfoHits + metrics.FindClientInfoMisses
	findClientHitRate := 0.0
	if findClientTotal > 0 {
		findClientHitRate = float64(metrics.FindClientInfoHits) / float64(findClientTotal) * 100
	}

	// Calculate cache usage ratio
	c.mu.RLock()
	currentCacheSize := len(c.cache)
	maxCacheSize := c.config.MaxCacheSize
	cacheUsageRatio := 0.0
	if maxCacheSize > 0 {
		cacheUsageRatio = float64(currentCacheSize) / float64(maxCacheSize) * 100
	}
	c.mu.RUnlock()

	logging.DefaultLogger().Infof(
		"Cache Metrics - Overall Hit Rate: %.2f%% (%d/%d), Cache Usage: %.2f%% (%d/%d)",
		overallHitRate, metrics.TotalHits, totalRequests,
		cacheUsageRatio, currentCacheSize, maxCacheSize,
	)

	logging.DefaultLogger().Infof(
		"Operation Hit Rates - ActivateClient: %.2f%% (%d/%d), "+
			"DeactivateClient: %.2f%% (%d/%d), TryAttaching: %.2f%% (%d/%d), "+
			"FindClientInfo: %.2f%% (%d/%d)",
		activateHitRate, metrics.ActivateClientHits, activateTotal,
		deactivateHitRate, metrics.DeactivateClientHits, deactivateTotal,
		tryAttachingHitRate, metrics.TryAttachingHits, tryAttachingTotal,
		findClientHitRate, metrics.FindClientInfoHits, findClientTotal,
	)
}
