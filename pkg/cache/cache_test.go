package cache_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/cache"
)

func TestCache(t *testing.T) {
	t.Run("create lru expire cache test", func(t *testing.T) {
		lruCache, err := cache.NewLRUExpireCache(1)
		assert.NoError(t, err)
		assert.NotNil(t, lruCache)

		lruCache, err = cache.NewLRUExpireCache(0)
		assert.Error(t, err)
		assert.Nil(t, lruCache)
	})

	t.Run("add test", func(t *testing.T) {
		lruCache, err := cache.NewLRUExpireCache(1)
		assert.NoError(t, err)

		lruCache.Add("request1", "response1", time.Second*5)
		response1, ok := lruCache.Get("request1")
		assert.True(t, ok)
		assert.NotNil(t, response1)

		lruCache.Add("request2", "response2", time.Second*5)
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

		ttl := time.Second * 1
		lruCache.Add("request", "response", ttl)

		time.Sleep(ttl)
		response, ok := lruCache.Get("request")
		assert.False(t, ok)
		assert.Nil(t, response)
	})
}
