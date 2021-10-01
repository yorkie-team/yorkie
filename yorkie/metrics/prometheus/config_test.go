package prometheus_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/yorkie/metrics/prometheus"
)

func TestConfig(t *testing.T) {
	scenarios := []*struct {
		config   *prometheus.Config
		expected error
	}{
		{config: &prometheus.Config{Port: -1}, expected: prometheus.ErrInvalidMetricPort},
		{config: &prometheus.Config{Port: 0}, expected: prometheus.ErrInvalidMetricPort},
		{config: &prometheus.Config{Port: 11102}, expected: nil},
	}
	for _, scenario := range scenarios {
		assert.ErrorIs(t, scenario.config.Validate(), scenario.expected, "provided config: %#v", scenario.config)
	}
}
