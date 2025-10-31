/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
 */

package mongo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestProjectCacheInitialized ensures that Dial initializes the project cache.
// NOTE: This is an integration-style unit that requires a running MongoDB at
// localhost:27017. Run it locally with a test DB to verify behavior.
func TestProjectCacheInitialized(t *testing.T) {
	// Use local test values to avoid import cycles with test helper.
	config := &Config{
		ConnectionTimeout:  "5s",
		ConnectionURI:      "mongodb://localhost:27017",
		YorkieDatabase:     "yorkie_test_project_cache",
		PingTimeout:        "5s",
		CacheStatsInterval: "1s",
		ClientCacheSize:    16,
		DocCacheSize:       16,
		ChangeCacheSize:    16,
		VectorCacheSize:    16,
		ProjectCacheSize:   256,
		ProjectCacheTTL:    "5s",
	}

	assert.NoError(t, config.Validate())

	cli, err := Dial(config)
	assert.NoError(t, err)
	if cli == nil {
		t.Fatal("Dial returned nil client")
	}
	defer cli.Close()

	// projectCache is an unexported field; this test lives in package mongo so
	// it can access it directly.
	assert.NotNil(t, cli.projectCache)
}
