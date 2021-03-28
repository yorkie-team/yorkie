package prometheus

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	namespace = "yorkie"
)

// Metrics manages the metric information that Yorkie is trying to measure.
type Metrics struct {
	currentVersion *prometheus.GaugeVec

	pushpullResponseSeconds prometheus.Histogram
	pushpullReceivedChanges prometheus.Counter
	pushpullSentChanges     prometheus.Counter

	pushpullSnapshotDurationSeconds prometheus.Histogram
	pushpullSnapshotBytes           prometheus.Gauge
}

// NewMetrics creates a new instance of Metrics.
func NewMetrics() *Metrics {
	return &Metrics{
		currentVersion: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "yorkie",
			Subsystem: "server",
			Name:      "version",
			Help:      "Which version is running. 1 for 'server_version' label with current version.",
		}, []string{"server_version"}),
		pushpullResponseSeconds: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "rpcserver",
			Name:      "pushpull_response_seconds",
			Help:      "Response time of PushPull API.",
		}),
		pushpullReceivedChanges: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "rpcserver",
			Name:      "pushpull_received_changes",
			Help:      "The number of changes included in a request pack in PushPull API.",
		}),
		pushpullSentChanges: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "rpcserver",
			Name:      "pushpull_sent_changes",
			Help:      "The number of changes included in a response pack in PushPull API.",
		}),
		pushpullSnapshotDurationSeconds: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "db",
			Name:      "pushpull_snapshot_duration_seconds",
			Help:      "The creation time of snapshot in PushPull API.",
		}),
		pushpullSnapshotBytes: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "db",
			Name:      "pushpull_snapshot_bytes",
			Help:      "The number of bytes of Snapshot.",
		}),
	}
}

// WithServerVersion adds a server's version information metric.
func (m *Metrics) WithServerVersion(version string) {
	m.currentVersion.With(prometheus.Labels{
		"server_version": version,
	}).Set(1)
}

// ObservePushpullResponseSeconds adds response time metrics for PushPull API.
func (m *Metrics) ObservePushpullResponseSeconds(seconds float64) {
	m.pushpullResponseSeconds.Observe(seconds)
}

// AddPushpullReceivedChanges adds the number of changes metric
// included in the request pack of the PushPull API.
func (m *Metrics) AddPushpullReceivedChanges(count float64) {
	m.pushpullReceivedChanges.Add(count)
}

// AddPushpullSentChanges adds the number of changes metric
// included in the response pack of the PushPull API.
func (m *Metrics) AddPushpullSentChanges(count float64) {
	m.pushpullSentChanges.Add(count)
}

// ObservePushpullSnapshotDurationSeconds adds the time
// spent metric when taking snapshots.
func (m *Metrics) ObservePushpullSnapshotDurationSeconds(seconds float64) {
	m.pushpullSnapshotDurationSeconds.Observe(seconds)
}

// SetPushpullSnapshotBytes sets the snapshot byte size.
func (m *Metrics) SetPushpullSnapshotBytes(bytes []byte) {
	m.pushpullSnapshotBytes.Set(float64(len(bytes)))
}
