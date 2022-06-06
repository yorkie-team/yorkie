package cache_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/cache"

	"github.com/yorkie-team/yorkie/api/types"
)

func TestCache(t *testing.T) {
	t.Run("create lru expire cache test", func(t *testing.T) {
		lruCache, err := cache.NewLRUExpireCache(1)
		assert.NoError(t, err)
		assert.NotNil(t, lruCache)

		lruCache, err = cache.NewLRUExpireCache(0)
		assert.ErrorIs(t, err, cache.ErrInvalidMaxSize)
		assert.Nil(t, lruCache)
	})

	t.Run("add test", func(t *testing.T) {
		lruCache, err := cache.NewLRUExpireCache(1)
		assert.NoError(t, err)

		lruCache.Add("request1", &types.AuthWebhookResponse{}, time.Second)
		response1, ok := lruCache.Get("request1")
		assert.True(t, ok)
		assert.NotNil(t, response1)

		lruCache.Add("request2", &types.AuthWebhookResponse{}, time.Second)
		response2, ok := lruCache.Get("request2")
		assert.True(t, ok)
		assert.NotNil(t, response2)

		// max size of the current cache is 1
		response1, ok = lruCache.Get("request1")
		assert.False(t, ok)
		assert.Nil(t, response1)
	})

	t.Run("get expired cache test", func(t *testing.T) {
		lruCache, err := cache.NewLRUExpireCache(1)
		assert.NoError(t, err)

		ttl := time.Millisecond
		lruCache.Add("request", &types.AuthWebhookResponse{}, ttl)

		time.Sleep(ttl)
		response, ok := lruCache.Get("request")
		assert.False(t, ok)
		assert.Nil(t, response)
	})

	t.Run("update expired cache test", func(t *testing.T) {
		lruCache, err := cache.NewLRUExpireCache(1)
		assert.NoError(t, err)

		var ttl time.Duration
		ttl = time.Minute

		lruCache.Add("request", &types.AuthWebhookResponse{}, ttl)
		response1, ok := lruCache.Get("request")
		assert.True(t, ok)
		assert.Equal(t, &types.AuthWebhookResponse{}, response1)

		ttl = time.Minute
		lruCache.Add("request", &types.AuthWebhookResponse{}, ttl)
		response2, ok := lruCache.Get("request")
		assert.True(t, ok)
		assert.Equal(t, &types.AuthWebhookResponse{}, response2)
	})
}
