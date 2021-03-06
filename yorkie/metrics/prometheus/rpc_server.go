package prometheus

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type RPCServerMetrics struct {
	subsystem string

	pushpullResponseSeconds prometheus.Histogram
	pushpullReceivedChanges prometheus.Counter
}

func NewRPCServerMetrics() *RPCServerMetrics {
	metrics := &RPCServerMetrics{
		subsystem: "rpcserver",
	}
	metrics.recordMetrics()

	return metrics
}

func (r *RPCServerMetrics) recordMetrics() {
	r.pushpullResponseSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: r.subsystem,
		Name:      "pushpull_response_seconds",
		Help:      "Response time of PushPull API.",
	})
	r.pushpullReceivedChanges = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: r.subsystem,
		Name:      "pushpull_received_changes",
		Help:      "The number of changes included in a response pack in PushPull API.",
	})
}

func (r *RPCServerMetrics) ObservePushpullResponseSeconds(seconds float64) {
	r.pushpullResponseSeconds.Observe(seconds)
}

func (r *RPCServerMetrics) AddPushpullReceivedChanges(count float64) {
	r.pushpullReceivedChanges.Add(count)
}

func (r *RPCServerMetrics) AddPushpullSentChanges(count float64) {

}

func (r *RPCServerMetrics) ObservePushpullSnapshotDurationSeconds(seconds float64) {

}

func (r *RPCServerMetrics) AddPushpullSnapshotBytes(byte float64) {

}
