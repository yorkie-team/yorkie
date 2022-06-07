package cache_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/cache"
)

func TestCache(t *testing.T) {
	t.Run("create lru expire cache test", func(t *testing.T) {
		lruCache, err := cache.NewLRUExpireCache[string, string](1)
		assert.NoError(t, err)
		assert.NotNil(t, lruCache)

		lruCache, err = cache.NewLRUExpireCache[string, string](0)
		assert.ErrorIs(t, err, cache.ErrInvalidMaxSize)
		assert.Nil(t, lruCache)
	})

	t.Run("add test", func(t *testing.T) {
		lruCache, err := cache.NewLRUExpireCache[string, string](1)
		assert.NoError(t, err)

		lruCache.Add("request1", "response1", time.Second)
		response1, ok := lruCache.Get("request1")
		assert.True(t, ok)
		assert.NotEmpty(t, response1)

		lruCache.Add("request2", "response2", time.Second)
		response2, ok := lruCache.Get("request2")
		assert.True(t, ok)
		assert.NotEmpty(t, response2)

		// max size of the current cache is 1
		response1, ok = lruCache.Get("request1")
		assert.False(t, ok)
		assert.Empty(t, response1)
	})

	t.Run("get expired cache test", func(t *testing.T) {
		lruCache, err := cache.NewLRUExpireCache[string, string](1)
		assert.NoError(t, err)

		ttl := time.Millisecond
		lruCache.Add("request", "response", ttl)

		time.Sleep(ttl)
		response, ok := lruCache.Get("request")
		assert.False(t, ok)
		assert.Empty(t, response)
	})

	t.Run("update expired cache test", func(t *testing.T) {
		lruCache, err := cache.NewLRUExpireCache[string, string](1)
		assert.NoError(t, err)

		var ttl time.Duration
		ttl = time.Minute

		lruCache.Add("request", "response1", ttl)
		response1, ok := lruCache.Get("request")
		assert.True(t, ok)
		assert.Equal(t, "response1", response1)

		ttl = time.Minute
		lruCache.Add("request", "response2", ttl)
		response2, ok := lruCache.Get("request")
		assert.True(t, ok)
		assert.Equal(t, "response2", response2)
	})
}
