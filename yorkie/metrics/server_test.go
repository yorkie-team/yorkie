package metrics_test

import (
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/testhelper"
	"github.com/yorkie-team/yorkie/yorkie/metrics"
)

const (
	// to avoid conflict with metrics port used for client test
	testMetricsPort = testhelper.MetricsPort + 100
)

var testMetricsServer *metrics.Server

func TestMetricsServer(t *testing.T) {
	t.Run("new server test", func(t *testing.T) {
		testMetricsServer, err := metrics.NewServer(&metrics.Config{
			Port: testMetricsPort,
		})
		assert.NoError(t, err)
		assert.NotNil(t, testMetricsServer)
		testMetricsServer.Shutdown(true)
	})

	t.Run("new server without config test", func(t *testing.T) {
		testMetricsServer, err := metrics.NewServer(nil)
		assert.NoError(t, err)
		assert.Nil(t, testMetricsServer)
	})
}
