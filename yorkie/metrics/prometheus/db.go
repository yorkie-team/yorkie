package prometheus

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type DBMetrics struct {
	subsystem string

	pushpullSnapshotDurationSeconds prometheus.Histogram
}

func NewDBMetrics() *DBMetrics {
	metrics := &DBMetrics{
		subsystem: "db",
	}
	metrics.recordMetrics()

	return metrics
}

func (d *DBMetrics) recordMetrics() {
	d.pushpullSnapshotDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: d.subsystem,
		Name:      "pushpull_snapshot_duration_seconds",
		Help:      "The creation time of snapshot in PushPull API.",
	})
}

func (d *DBMetrics) ObservePushpullSnapshotDurationSeconds(seconds float64) {
	d.pushpullSnapshotDurationSeconds.Observe(seconds)
}
