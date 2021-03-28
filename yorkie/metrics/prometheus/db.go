/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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
)

// DBMetrics can add metrics for DB.
type DBMetrics struct {
	subsystem string

	pushpullSnapshotDurationSeconds prometheus.Histogram
	pushpullSnapshotBytes           prometheus.Gauge
}

// NewDBMetrics creates an instance of DBMetrics.
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
	d.pushpullSnapshotBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: d.subsystem,
		Name:      "pushpull_snapshot_bytes",
		Help:      "The number of bytes of Snapshot.",
	})
}

// ObservePushpullSnapshotDurationSeconds adds the time
// spent metric when taking snapshots.
func (d *DBMetrics) ObservePushpullSnapshotDurationSeconds(seconds float64) {
	d.pushpullSnapshotDurationSeconds.Observe(seconds)
}

// SetPushpullSnapshotBytes sets the snapshot byte size.
func (d *DBMetrics) SetPushpullSnapshotBytes(bytes []byte) {
	d.pushpullSnapshotBytes.Set(float64(len(bytes)))
}
