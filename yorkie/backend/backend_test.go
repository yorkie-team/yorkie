package backend_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"
)

func TestConfig(t *testing.T) {
	t.Run("register methods for authorization webhook test", func(t *testing.T) {
		config := backend.Config{}

		err := config.RegisterAuthWebhookMethods([]string{"InvalidMethod"})
		assert.Error(t, err)

		err = config.RegisterAuthWebhookMethods([]string{string(types.ActivateClient)})
		assert.NoError(t, err)
		assert.True(t, config.IsRunAuthWebhook(types.ActivateClient))
		assert.False(t, config.IsRunAuthWebhook(types.DetachDocument))
	})

	t.Run("register all methods for authorization webhook test", func(t *testing.T) {
		config := backend.Config{}
		err := config.RegisterAuthWebhookMethods([]string{})
		assert.NoError(t, err)
		assert.True(t, config.IsRunAuthWebhook(types.ActivateClient))
		assert.True(t, config.IsRunAuthWebhook(types.DetachDocument))
	})

	t.Run("register an already registered method test", func(t *testing.T) {
		config := backend.Config{
			AuthorizationWebhookMethods: []types.Method{types.ActivateClient},
		}
		err := config.RegisterAuthWebhookMethods([]string{string(types.ActivateClient)})
		assert.NoError(t, err)

		assert.Len(t, config.AuthorizationWebhookMethods, 1)
	})

	t.Run("validate methods for authorization webhook test", func(t *testing.T) {
		config := backend.Config{
			AuthorizationWebhookMethods: []types.Method{types.Method("invalidMethod")},
		}
		err := config.ValidateAuthWebhookMethods()
		assert.Error(t, err)
	})
}
