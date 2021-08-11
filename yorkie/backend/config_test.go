package backend_test

import (
	"testing"

	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"

	"github.com/stretchr/testify/assert"
)

func TestConfig(t *testing.T) {
	t.Run("require auth test", func(t *testing.T) {
		// 1. Specify which methods to allow
		conf1 := backend.Config{
			AuthWebhookURL:     "ValidWebhookURL",
			AuthWebhookMethods: []string{string(types.ActivateClient)},
		}
		assert.True(t, conf1.RequireAuth(types.ActivateClient))
		assert.False(t, conf1.RequireAuth(types.DetachDocument))

		// 2. Allow all
		conf2 := backend.Config{
			AuthWebhookURL:     "ValidWebhookURL",
			AuthWebhookMethods: []string{},
		}
		assert.True(t, conf2.RequireAuth(types.ActivateClient))
		assert.True(t, conf2.RequireAuth(types.DetachDocument))

		// 3. Empty webhook URL
		conf3 := backend.Config{
			AuthWebhookURL: "",
		}
		assert.False(t, conf3.RequireAuth(types.ActivateClient))
	})

	t.Run("validate test", func(t *testing.T) {
		// 1.Success
		conf1 := backend.Config{
			AuthWebhookMethods:         []string{"ActivateClient"},
			AuthWebhookMaxWaitInterval: "0ms",
			AuthWebhookCacheAuthTTL:    "10s",
			AuthWebhookCacheUnauthTTL:  "10s",
		}
		assert.NoError(t, conf1.Validate())

		// 2. Included invalid methods
		conf2 := backend.Config{
			AuthWebhookMethods:         []string{"InvalidMethod"},
			AuthWebhookMaxWaitInterval: "0ms",
			AuthWebhookCacheAuthTTL:    "10s",
			AuthWebhookCacheUnauthTTL:  "10s",
		}
		assert.Error(t, conf2.Validate())

		// 3. Invalid AuthWebhookMaxWaitInterval
		conf3 := backend.Config{
			AuthWebhookMethods:         []string{"ActivateClient"},
			AuthWebhookMaxWaitInterval: "5",
			AuthWebhookCacheAuthTTL:    "10s",
			AuthWebhookCacheUnauthTTL:  "10s",
		}
		assert.Error(t, conf3.Validate())

		// 3. Invalid AuthWebhookCacheAuthTTL
		conf4 := backend.Config{
			AuthWebhookMethods:         []string{"ActivateClient"},
			AuthWebhookMaxWaitInterval: "0ms",
			AuthWebhookCacheAuthTTL:    "s",
			AuthWebhookCacheUnauthTTL:  "10s",
		}
		assert.Error(t, conf4.Validate())

		// 4. Invalid AuthWebhookCacheUnauthTTL
		conf5 := backend.Config{
			AuthWebhookMethods:         []string{"ActivateClient"},
			AuthWebhookMaxWaitInterval: "0ms",
			AuthWebhookCacheAuthTTL:    "10s",
			AuthWebhookCacheUnauthTTL:  "s",
		}
		assert.Error(t, conf5.Validate())
	})
}
