package backend_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"
)

func TestConfig(t *testing.T) {
	t.Run("resister methods for authorization webhook test", func(t *testing.T) {
		config := backend.Config{}

		err := config.ResisterAuthWebhookMethods([]string{"InvalidMethod"})
		assert.Error(t, err)

		err = config.ResisterAuthWebhookMethods([]string{string(types.ActivateClient)})
		assert.NoError(t, err)
		assert.True(t, config.IsRunAuthWebhook(types.ActivateClient))
		assert.False(t, config.IsRunAuthWebhook(types.DetachDocument))
	})

	t.Run("resister all methods for authorization webhook test", func(t *testing.T) {
		config := backend.Config{}
		err := config.ResisterAuthWebhookMethods([]string{})
		assert.NoError(t, err)
		assert.True(t, config.IsRunAuthWebhook(types.ActivateClient))
		assert.True(t, config.IsRunAuthWebhook(types.DetachDocument))
	})
}
