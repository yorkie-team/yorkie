package backend_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"
)

func TestCache(t *testing.T) {
	t.Run("create lru expire cache test", func(t *testing.T) {
		cache, err := backend.NewLRUExpireCache(1)
		assert.NoError(t, err)
		assert.NotNil(t, cache)

		cache, err = backend.NewLRUExpireCache(0)
		assert.Error(t, err)
		assert.Nil(t, cache)
	})

	t.Run("add test", func(t *testing.T) {
		cache, err := backend.NewLRUExpireCache(1)
		assert.NoError(t, err)

		cache.Add("request1", &types.AuthWebhookResponse{Allowed: true}, time.Second*5)
		response1, ok := cache.Get("request1")
		assert.True(t, ok)
		assert.NotNil(t, response1)

		cache.Add("request2", &types.AuthWebhookResponse{Allowed: true}, time.Second*5)
		response2, ok := cache.Get("request2")
		assert.True(t, ok)
		assert.NotNil(t, response2)

		// max size of the current cache is 1
		response1, ok = cache.Get("request1")
		assert.False(t, ok)
		assert.Nil(t, response1)
	})

	t.Run("get expired cache test", func(t *testing.T) {
		cache, err := backend.NewLRUExpireCache(1)
		assert.NoError(t, err)

		ttl := time.Second * 1
		cache.Add("request", &types.AuthWebhookResponse{Allowed: true}, ttl)

		time.Sleep(ttl)
		response, ok := cache.Get("request")
		assert.False(t, ok)
		assert.Nil(t, response)
	})
}
