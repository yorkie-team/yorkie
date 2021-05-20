/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package prometheus

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/yorkie-team/yorkie/pkg/version"
)

const (
	namespace = "yorkie"
)

// Metrics manages the metric information that Yorkie is trying to measure.
type Metrics struct {
	registry *prometheus.Registry

	agentVersion *prometheus.GaugeVec

	pushPullResponseSeconds         prometheus.Histogram
	pushPullReceivedChanges         prometheus.Gauge
	pushPullSentChanges             prometheus.Gauge
	pushPullSnapshotDurationSeconds prometheus.Histogram
	pushPullSnapshotBytes           prometheus.Gauge
}

// NewMetrics creates a new instance of Metrics.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	metrics := &Metrics{
		agentVersion: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "yorkie",
			Subsystem: "agent",
			Name:      "version",
			Help:      "Which version is running. 1 for 'agent_version' label with current version.",
		}, []string{"agent_version"}),
		pushPullResponseSeconds: promauto.With(reg).NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "rpcserver",
			Name:      "pushpull_response_seconds",
			Help:      "Response time of PushPull API.",
		}),
		pushPullReceivedChanges: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "rpcserver",
			Name:      "pushpull_received_changes",
			Help:      "The number of changes included in a request pack in PushPull API.",
		}),
		pushPullSentChanges: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "rpcserver",
			Name:      "pushpull_sent_changes",
			Help:      "The number of changes included in a response pack in PushPull API.",
		}),
		pushPullSnapshotDurationSeconds: promauto.With(reg).NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "db",
			Name:      "pushpull_snapshot_duration_seconds",
			Help:      "The creation time of snapshot in PushPull API.",
		}),
		pushPullSnapshotBytes: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "db",
			Name:      "pushpull_snapshot_bytes",
			Help:      "The number of bytes of Snapshot.",
		}),
	}

	metrics.agentVersion.With(prometheus.Labels{
		"agent_version": version.Version,
	}).Set(1)

	return metrics
}

// ObservePushPullResponseSeconds adds response time metrics for PushPull API.
func (m *Metrics) ObservePushPullResponseSeconds(seconds float64) {
	m.pushPullResponseSeconds.Observe(seconds)
}

// SetPushPullReceivedChanges sets the number of changes metric
// included in the request pack of the PushPull API.
func (m *Metrics) SetPushPullReceivedChanges(count int) {
	m.pushPullReceivedChanges.Set(float64(count))
}

// SetPushPullSentChanges sets the number of changes metric
// included in the response pack of the PushPull API.
func (m *Metrics) SetPushPullSentChanges(count int) {
	m.pushPullSentChanges.Set(float64(count))
}

// ObservePushPullSnapshotDurationSeconds adds the time
// spent metric when taking snapshots.
func (m *Metrics) ObservePushPullSnapshotDurationSeconds(seconds float64) {
	m.pushPullSnapshotDurationSeconds.Observe(seconds)
}

// SetPushPullSnapshotBytes sets the snapshot byte size.
func (m *Metrics) SetPushPullSnapshotBytes(bytes int) {
	m.pushPullSnapshotBytes.Set(float64(bytes))
}

// Registry returns the registry of this metrics.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}
