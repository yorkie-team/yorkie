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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
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

	// TTL-related metrics
	TTLExpirations int64
	TTLFlushErrors int64
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
	// TTL is the time-to-live for cached entries
	TTL time.Duration
}

// DefaultCacheConfig returns default cache configuration
func DefaultCacheConfig() *CacheConfig {
	return &CacheConfig{
		BaseFlushInterval:      10 * time.Second,
		MaxFlushInterval:       7 * time.Second,
		MinFlushInterval:       5 * time.Second,
		MaxCacheSize:           20000,
		WritePressureThreshold: 500,
		PressureCheckInterval:  10 * time.Second,
		EnableFlushCleanup:     true,
		TTL:                    10 * time.Second,
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
	client        *Client
	writePressure *WritePressure
	pressureMu    sync.RWMutex
	metrics       *CacheMetrics
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
	go cache.ttlCleanup()

	// Start metrics logging
	cache.StartMetricsLogging()

	return cache
}

// handleExpiredEntry processes an expired cache entry
// Status information is discarded, checkpoint information is flushed to DB using max operator
func (c *ClientInfoCache) handleExpiredEntry(refKey types.ClientRefKey, cached *CachedClientInfo) {
	atomic.AddInt64(&c.metrics.TTLExpirations, 1)

	// Create a copy of client info with only checkpoint information
	checkpointInfo := &database.ClientInfo{
		ID:        cached.ClientInfo.ID,
		ProjectID: cached.ClientInfo.ProjectID,
		Key:       cached.ClientInfo.Key,
		Status:    cached.ClientInfo.Status,
		Documents: make(map[types.ID]*database.ClientDocInfo),
		Metadata:  cached.ClientInfo.Metadata,
		CreatedAt: cached.ClientInfo.CreatedAt,
		UpdatedAt: cached.ClientInfo.UpdatedAt,
	}

	// Copy only checkpoint information (ServerSeq, ClientSeq) from documents
	for docID, docInfo := range cached.ClientInfo.Documents {
		checkpointInfo.Documents[docID] = &database.ClientDocInfo{
			ServerSeq: docInfo.ServerSeq,
			ClientSeq: docInfo.ClientSeq,
			Status:    docInfo.Status,
		}
	}

	// Flush checkpoint information to DB using max operator
	if err := c.flushSingleToDB(refKey, checkpointInfo); err != nil {
		atomic.AddInt64(&c.metrics.TTLFlushErrors, 1)
		logging.DefaultLogger().Errorf("Failed to flush expired entry checkpoint to DB: %v", err)
	}
}

// Get retrieves a ClientInfo from the cache
func (c *ClientInfoCache) Get(refKey types.ClientRefKey) *database.ClientInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if cached, exists := c.cache[refKey]; exists {
		// Check if entry has expired
		if time.Now().After(cached.ExpiresAt) {
			// Schedule expiration handling in background
			go c.handleExpiration(refKey)
			c.recordMiss()
			return nil
		}

		c.recordHit()
		return cached.ClientInfo.DeepCopy()
	}

	c.recordMiss()
	return nil
}

// handleExpiration handles expired entry cleanup in background
func (c *ClientInfoCache) handleExpiration(refKey types.ClientRefKey) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, exists := c.cache[refKey]; exists && time.Now().After(cached.ExpiresAt) {
		c.handleExpiredEntry(refKey, cached)
		delete(c.cache, refKey)
	}
}

// GetByKey retrieves a ClientInfo from the cache by project ID and key
func (c *ClientInfoCache) GetByKey(projectID types.ID, key string) *database.ClientInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for refKey, cached := range c.cache {
		if refKey.ProjectID == projectID && cached.ClientInfo.Key == key {
			// Check if entry has expired
			if time.Now().After(cached.ExpiresAt) {
				// Schedule expiration handling in background
				go c.handleExpiration(refKey)
				c.recordMiss()
				return nil
			}

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
	var expiredKeys []types.ClientRefKey

	now := time.Now()
	for refKey, cached := range c.cache {
		if refKey.ProjectID == projectID {
			// Check if entry has expired
			if now.After(cached.ExpiresAt) {
				expiredKeys = append(expiredKeys, refKey)
				continue
			}
			clients = append(clients, cached.ClientInfo.DeepCopy())
		}
	}

	// Handle expired entries in background if any found
	if len(expiredKeys) > 0 {
		go c.handleBatchExpiration(expiredKeys)
	}

	return clients
}

// handleBatchExpiration handles multiple expired entries in batch
func (c *ClientInfoCache) handleBatchExpiration(refKeys []types.ClientRefKey) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for _, refKey := range refKeys {
		if cached, exists := c.cache[refKey]; exists && now.After(cached.ExpiresAt) {
			c.handleExpiredEntry(refKey, cached)
			delete(c.cache, refKey)
		}
	}
}

// Set stores a ClientInfo in the cache
func (c *ClientInfoCache) Set(refKey types.ClientRefKey, clientInfo *database.ClientInfo) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	c.cache[refKey] = &CachedClientInfo{
		ClientInfo: clientInfo.DeepCopy(),
		UpdatedAt:  now,
		Dirty:      false,
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
		cached.ExpiresAt = time.Now().Add(c.config.TTL)
		return nil
	}

	now := time.Now()
	c.cache[refKey] = &CachedClientInfo{
		ClientInfo: clientInfo.DeepCopy(),
		UpdatedAt:  now,
		Dirty:      true,
		LastFlush:  now,
		ExpiresAt:  now.Add(c.config.TTL),
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

// ttlCleanup runs background goroutine for periodic TTL cleanup
func (c *ClientInfoCache) ttlCleanup() {
	ticker := time.NewTicker(c.config.TTL)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			now := time.Now()
			for refKey, cached := range c.cache {
				if now.After(cached.ExpiresAt) {
					c.handleExpiredEntry(refKey, cached)
					delete(c.cache, refKey)
				}
			}
			c.mu.Unlock()
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
	atomic.AddInt64(&c.metrics.TotalHits, 1)
}

func (c *ClientInfoCache) recordMiss() {
	atomic.AddInt64(&c.metrics.TotalMisses, 1)
}

func (c *ClientInfoCache) recordActivateClientMiss() {
	atomic.AddInt64(&c.metrics.ActivateClientMisses, 1)
	atomic.AddInt64(&c.metrics.TotalMisses, 1)
}

func (c *ClientInfoCache) recordDeactivateClientHit() {
	atomic.AddInt64(&c.metrics.DeactivateClientHits, 1)
	atomic.AddInt64(&c.metrics.TotalHits, 1)
}

func (c *ClientInfoCache) recordDeactivateClientMiss() {
	atomic.AddInt64(&c.metrics.DeactivateClientMisses, 1)
	atomic.AddInt64(&c.metrics.TotalMisses, 1)
}

func (c *ClientInfoCache) recordTryAttachingHit() {
	atomic.AddInt64(&c.metrics.TryAttachingHits, 1)
	atomic.AddInt64(&c.metrics.TotalHits, 1)
}

func (c *ClientInfoCache) recordTryAttachingMiss() {
	atomic.AddInt64(&c.metrics.TryAttachingMisses, 1)
	atomic.AddInt64(&c.metrics.TotalMisses, 1)
}

func (c *ClientInfoCache) recordFindClientInfoHit() {
	atomic.AddInt64(&c.metrics.FindClientInfoHits, 1)
	atomic.AddInt64(&c.metrics.TotalHits, 1)
}

func (c *ClientInfoCache) recordFindClientInfoMiss() {
	atomic.AddInt64(&c.metrics.FindClientInfoMisses, 1)
	atomic.AddInt64(&c.metrics.TotalMisses, 1)
}

// GetMetrics returns current cache metrics
func (c *ClientInfoCache) GetMetrics() *CacheMetrics {
	// Use atomic loads to read metrics without locking
	return &CacheMetrics{
		TotalHits:              atomic.LoadInt64(&c.metrics.TotalHits),
		TotalMisses:            atomic.LoadInt64(&c.metrics.TotalMisses),
		ActivateClientHits:     atomic.LoadInt64(&c.metrics.ActivateClientHits),
		ActivateClientMisses:   atomic.LoadInt64(&c.metrics.ActivateClientMisses),
		DeactivateClientHits:   atomic.LoadInt64(&c.metrics.DeactivateClientHits),
		DeactivateClientMisses: atomic.LoadInt64(&c.metrics.DeactivateClientMisses),
		TryAttachingHits:       atomic.LoadInt64(&c.metrics.TryAttachingHits),
		TryAttachingMisses:     atomic.LoadInt64(&c.metrics.TryAttachingMisses),
		FindClientInfoHits:     atomic.LoadInt64(&c.metrics.FindClientInfoHits),
		FindClientInfoMisses:   atomic.LoadInt64(&c.metrics.FindClientInfoMisses),
		TTLExpirations:         atomic.LoadInt64(&c.metrics.TTLExpirations),
		TTLFlushErrors:         atomic.LoadInt64(&c.metrics.TTLFlushErrors),
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

	logging.DefaultLogger().Infof(
		"TTL Metrics - Expirations: %d, Flush Errors: %d",
		metrics.TTLExpirations, metrics.TTLFlushErrors,
	)
}

// UpdateCheckpoint updates only the checkpoint (ServerSeq, ClientSeq) for a specific document
// This uses Write-back strategy with max operator for consistency
func (c *ClientInfoCache) UpdateCheckpoint(refKey types.ClientRefKey, docID types.ID, cp change.Checkpoint) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, exists := c.cache[refKey]; exists {
		// Update checkpoint in cache with max operator
		if cached.ClientInfo.Documents == nil {
			cached.ClientInfo.Documents = make(map[types.ID]*database.ClientDocInfo)
		}

		if docInfo, exists := cached.ClientInfo.Documents[docID]; exists {
			// Apply max operator for sequence numbers
			if cp.ServerSeq > docInfo.ServerSeq {
				docInfo.ServerSeq = cp.ServerSeq
			}
			if cp.ClientSeq > docInfo.ClientSeq {
				docInfo.ClientSeq = cp.ClientSeq
			}
		} else {
			// Create new document info
			cached.ClientInfo.Documents[docID] = &database.ClientDocInfo{
				Status:    database.DocumentAttached,
				ServerSeq: cp.ServerSeq,
				ClientSeq: cp.ClientSeq,
			}
		}

		cached.Dirty = true
		cached.UpdatedAt = time.Now()
		cached.ExpiresAt = time.Now().Add(c.config.TTL)
		return nil
	}

	// Cache miss - load from DB and update
	clientInfo, err := c.loadClientInfoFromDB(refKey)
	if err != nil {
		return fmt.Errorf("load client info from DB: %w", err)
	}

	// Update checkpoint
	if clientInfo.Documents == nil {
		clientInfo.Documents = make(map[types.ID]*database.ClientDocInfo)
	}

	if docInfo, exists := clientInfo.Documents[docID]; exists {
		if cp.ServerSeq > docInfo.ServerSeq {
			docInfo.ServerSeq = cp.ServerSeq
		}
		if cp.ClientSeq > docInfo.ClientSeq {
			docInfo.ClientSeq = cp.ClientSeq
		}
	} else {
		clientInfo.Documents[docID] = &database.ClientDocInfo{
			Status:    database.DocumentAttached,
			ServerSeq: cp.ServerSeq,
			ClientSeq: cp.ClientSeq,
		}
	}

	// Store in cache
	now := time.Now()
	c.cache[refKey] = &CachedClientInfo{
		ClientInfo: clientInfo,
		UpdatedAt:  now,
		Dirty:      true,
		LastFlush:  now,
		ExpiresAt:  now.Add(c.config.TTL),
	}
	return nil
}

// UpdateStatus updates the client status using CAS Write-through strategy
func (c *ClientInfoCache) UpdateStatus(refKey types.ClientRefKey, status string) error {
	// Check if update is needed by comparing with cache first
	c.mu.Lock()
	if cached, exists := c.cache[refKey]; exists {
		if cached.ClientInfo.Status == status {
			// No change needed, skip database update
			c.mu.Unlock()
			return nil
		}
	}
	c.mu.Unlock()

	// Update in DB (Write-through)
	if err := c.updateStatusInDB(refKey, status); err != nil {
		return fmt.Errorf("update status in DB: %w", err)
	}

	// Then update cache
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, exists := c.cache[refKey]; exists {
		cached.ClientInfo.Status = status
		cached.UpdatedAt = time.Now()
		// Not dirty since already written to DB
		cached.Dirty = false
	} else {
		// If not in cache, load from DB and cache it
		clientInfo, err := c.loadClientInfoFromDB(refKey)
		if err != nil {
			return fmt.Errorf("load client info from DB: %w", err)
		}

		// Update the status in the loaded client info
		clientInfo.Status = status

		now := time.Now()
		c.cache[refKey] = &CachedClientInfo{
			ClientInfo: clientInfo,
			UpdatedAt:  now,
			Dirty:      false,
			LastFlush:  now,
			ExpiresAt:  now.Add(c.config.TTL),
		}
	}
	return nil
}

// UpdateDocumentStatus updates the document status using CAS Write-through strategy
// For DocumentAttaching status, it also resets ServerSeq and ClientSeq to 0
func (c *ClientInfoCache) UpdateDocumentStatus(refKey types.ClientRefKey, docID types.ID, status string) error {
	// Check if update is needed by comparing with cache first
	c.mu.Lock()
	if cached, exists := c.cache[refKey]; exists {
		if cached.ClientInfo.Documents != nil {
			if docInfo, exists := cached.ClientInfo.Documents[docID]; exists {
				if docInfo.Status == status {
					// No change needed, skip database update
					c.mu.Unlock()
					return nil
				}
			}
		}
	}
	c.mu.Unlock()

	// Update in DB (Write-through)
	if err := c.updateDocumentStatusInDB(refKey, docID, status); err != nil {
		return fmt.Errorf("update document status in DB: %w", err)
	}

	// Then update cache
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, exists := c.cache[refKey]; exists {
		if cached.ClientInfo.Documents == nil {
			cached.ClientInfo.Documents = make(map[types.ID]*database.ClientDocInfo)
		}

		if docInfo, exists := cached.ClientInfo.Documents[docID]; exists {
			docInfo.Status = status
			// Reset sequence numbers for detached/removed/attaching documents
			if status == database.DocumentDetached || status == database.DocumentRemoved ||
				status == database.DocumentAttaching {
				docInfo.ServerSeq = 0
				docInfo.ClientSeq = 0
			}
		} else {
			cached.ClientInfo.Documents[docID] = &database.ClientDocInfo{
				Status:    status,
				ServerSeq: 0,
				ClientSeq: 0,
			}
		}

		cached.UpdatedAt = time.Now()
		// Not dirty since already written to DB
		cached.Dirty = false
	} else {
		// If not in cache, try to load from DB first
		clientInfo, err := c.loadClientInfoFromDB(refKey)
		if err != nil {
			// If client not found in DB, this might be a new document attachment
			// In this case, we need to create a minimal client info structure
			if strings.Contains(err.Error(), "client not found") {
				// For new document attachments, we'll create a minimal cache entry
				// The actual client info will be loaded when needed
				logging.DefaultLogger().Warnf(
					"Client not found in DB for document attachment, creating minimal cache entry: %v", err,
				)
				return nil
			}
			return fmt.Errorf("load client info from DB: %w", err)
		}

		// Update the document status in the loaded client info
		if clientInfo.Documents == nil {
			clientInfo.Documents = make(map[types.ID]*database.ClientDocInfo)
		}

		if docInfo, exists := clientInfo.Documents[docID]; exists {
			docInfo.Status = status
			// Reset sequence numbers for detached/removed/attaching documents
			if status == database.DocumentDetached || status == database.DocumentRemoved ||
				status == database.DocumentAttaching {
				docInfo.ServerSeq = 0
				docInfo.ClientSeq = 0
			}
		} else {
			clientInfo.Documents[docID] = &database.ClientDocInfo{
				Status:    status,
				ServerSeq: 0,
				ClientSeq: 0,
			}
		}

		now := time.Now()
		c.cache[refKey] = &CachedClientInfo{
			ClientInfo: clientInfo,
			UpdatedAt:  now,
			Dirty:      false,
			LastFlush:  now,
			ExpiresAt:  now.Add(c.config.TTL),
		}
	}
	return nil
}

// loadClientInfoFromDB loads client info from database
func (c *ClientInfoCache) loadClientInfoFromDB(refKey types.ClientRefKey) (*database.ClientInfo, error) {
	result := c.client.collection(ColClients).FindOne(context.Background(), bson.M{
		"project_id": refKey.ProjectID,
		"_id":        refKey.ClientID,
	})

	if result.Err() != nil {
		if result.Err() == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", refKey.ClientID, database.ErrClientNotFound)
		}
		return nil, fmt.Errorf("find client by id: %w", result.Err())
	}

	var clientInfo database.ClientInfo
	if err := result.Decode(&clientInfo); err != nil {
		return nil, fmt.Errorf("decode client info: %w", err)
	}

	return &clientInfo, nil
}

// updateStatusInDB updates client status in database
func (c *ClientInfoCache) updateStatusInDB(refKey types.ClientRefKey, status string) error {
	_, err := c.client.collection(ColClients).UpdateOne(
		context.Background(),
		bson.M{
			"project_id": refKey.ProjectID,
			"_id":        refKey.ClientID,
		},
		bson.M{
			"$set": bson.M{
				"status":     status,
				"updated_at": time.Now(),
			},
		},
	)
	return err
}

// updateDocumentStatusInDB updates document status in database
func (c *ClientInfoCache) updateDocumentStatusInDB(refKey types.ClientRefKey, docID types.ID, status string) error {
	updateDoc := bson.M{
		"$set": bson.M{
			"updated_at": time.Now(),
		},
	}

	// Add document-specific updates
	docKey := clientDocInfoKey(docID, StatusKey)
	updateDoc["$set"].(bson.M)[docKey] = status

	// Reset sequence numbers for detached/removed/attaching documents
	if status == database.DocumentDetached || status == database.DocumentRemoved || status == database.DocumentAttaching {
		serverSeqKey := clientDocInfoKey(docID, "server_seq")
		clientSeqKey := clientDocInfoKey(docID, "client_seq")
		updateDoc["$set"].(bson.M)[serverSeqKey] = int64(0)
		updateDoc["$set"].(bson.M)[clientSeqKey] = uint32(0)
	}

	_, err := c.client.collection(ColClients).UpdateOne(
		context.Background(),
		bson.M{
			"project_id": refKey.ProjectID,
			"_id":        refKey.ClientID,
		},
		updateDoc,
	)
	return err
}
