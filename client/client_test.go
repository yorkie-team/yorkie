package client_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
)

func TestClient(t *testing.T) {
	t.Run("new test", func(t *testing.T) {
		opts := client.Option{Metadata: map[string]string{
			"Name": "ClientName",
		}}
		cli, err := client.NewClient(opts)
		assert.NoError(t, err)

		assert.Equal(t, opts.Metadata, cli.Metadata())
	})
}
