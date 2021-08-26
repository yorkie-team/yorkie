package backend_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"
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
		validConf := backend.Config{
			AuthWebhookMethods:         []string{"ActivateClient"},
			AuthWebhookMaxWaitInterval: "0ms",
			AuthWebhookCacheAuthTTL:    "10s",
			AuthWebhookCacheUnauthTTL:  "10s",
		}
		assert.NoError(t, validConf.Validate())

		// 2. Included invalid methods
		conf1 := validConf
		conf1.AuthWebhookMethods = []string{"InvalidMethod"}
		assert.Error(t, conf1.Validate())

		// 3. Invalid AuthWebhookMaxWaitInterval
		conf2 := validConf
		conf2.AuthWebhookMaxWaitInterval = "5"
		assert.Error(t, conf2.Validate())

		// 3. Invalid AuthWebhookCacheAuthTTL
		conf3 := validConf
		conf3.AuthWebhookCacheAuthTTL = "s"
		assert.Error(t, conf3.Validate())

		// 4. Invalid AuthWebhookCacheUnauthTTL
		conf4 := validConf
		conf4.AuthWebhookCacheUnauthTTL = "s"
		assert.Error(t, conf4.Validate())
	})
}
