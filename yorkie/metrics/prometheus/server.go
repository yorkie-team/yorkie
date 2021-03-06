package prometheus

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type ServerMetrics struct {
	subsystem string

	currentVersion *prometheus.GaugeVec
}

func NewServerMetrics() *ServerMetrics {
	metrics := &ServerMetrics{
		subsystem: "server",
	}
	metrics.recordMetrics()

	return metrics
}

func (s *ServerMetrics) recordMetrics() {
	s.currentVersion = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "yorkie",
		Subsystem: "server",
		Name:      "version",
		Help:      "Which version is running. 1 for 'server_version' label with current version.",
	}, []string{"server_version"})
}

func (s *ServerMetrics) WithServerVersion(version string) {
	s.currentVersion.With(prometheus.Labels{
		"server_version": version,
	}).Set(1)
}
