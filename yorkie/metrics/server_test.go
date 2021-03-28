package metrics_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/test/helper"
	"github.com/yorkie-team/yorkie/yorkie/metrics"
)

const (
	// to avoid conflict with metrics port used for client test
	testMetricsPort = helper.MetricsPort + 100
)

func TestMetricsServer(t *testing.T) {
	t.Run("new server test", func(t *testing.T) {
		server, err := metrics.NewServer(&metrics.Config{
			Port: testMetricsPort,
		})
		assert.NoError(t, err)
		assert.NotNil(t, server)
		assert.NotNil(t, server.Metrics)
		server.Shutdown(true)
	})

	t.Run("new server without config test", func(t *testing.T) {
		server, err := metrics.NewServer(nil)
		assert.NoError(t, err)
		assert.Nil(t, server)
	})
}
