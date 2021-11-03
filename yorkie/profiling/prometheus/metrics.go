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
	grpcprometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"

	"github.com/yorkie-team/yorkie/internal/version"
)

const (
	namespace = "yorkie"
)

// Metrics manages the metric information that Yorkie is trying to measure.
type Metrics struct {
	registry      *prometheus.Registry
	serverMetrics *grpcprometheus.ServerMetrics

	agentVersion *prometheus.GaugeVec

	pushPullResponseSeconds         prometheus.Histogram
	pushPullReceivedChangesTotal    prometheus.Counter
	pushPullSentChangesTotal        prometheus.Counter
	pushPullSnapshotDurationSeconds prometheus.Histogram
	pushPullSnapshotBytesTotal      prometheus.Counter
}

// NewMetrics creates a new instance of Metrics.
func NewMetrics() (*Metrics, error) {
	reg := prometheus.NewRegistry()
	serverMetrics := grpcprometheus.NewServerMetrics()

	if err := reg.Register(serverMetrics); err != nil {
		return nil, err
	}
	if err := reg.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, err
	}
	if err := reg.Register(collectors.NewGoCollector()); err != nil {
		return nil, err
	}

	metrics := &Metrics{
		registry:      reg,
		serverMetrics: serverMetrics,
		agentVersion: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "agent",
			Name:      "version",
			Help:      "Which version is running. 1 for 'agent_version' label with current version.",
		}, []string{"agent_version"}),
		pushPullResponseSeconds: promauto.With(reg).NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "pushpull",
			Name:      "response_seconds",
			Help:      "The response time of PushPull.",
		}),
		pushPullReceivedChangesTotal: promauto.With(reg).NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "pushpull",
			Name:      "received_changes_total",
			Help:      "The total count of changes included in request packs in PushPull.",
		}),
		pushPullSentChangesTotal: promauto.With(reg).NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "pushpull",
			Name:      "sent_changes_total",
			Help:      "The total count of changes included in response packs in PushPull.",
		}),
		pushPullSnapshotDurationSeconds: promauto.With(reg).NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "pushpull",
			Name:      "snapshot_duration_seconds",
			Help:      "The creation time of snapshot for response packs in PushPull.",
		}),
		pushPullSnapshotBytesTotal: promauto.With(reg).NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "pushpull",
			Name:      "snapshot_bytes_total",
			Help:      "The total bytes of snapshots for response packs in PushPull.",
		}),
	}

	metrics.agentVersion.With(prometheus.Labels{
		"agent_version": version.Version,
	}).Set(1)

	return metrics, nil
}

// ObservePushPullResponseSeconds adds an observation for response time of
// PushPull.
func (m *Metrics) ObservePushPullResponseSeconds(seconds float64) {
	m.pushPullResponseSeconds.Observe(seconds)
}

// AddPushPullReceivedChanges sets the number of changes
// included in the request pack of PushPull.
func (m *Metrics) AddPushPullReceivedChanges(count int) {
	m.pushPullReceivedChangesTotal.Add(float64(count))
}

// AddPushPullSentChanges adds the number of changes
// included in the response pack of PushPull.
func (m *Metrics) AddPushPullSentChanges(count int) {
	m.pushPullSentChangesTotal.Add(float64(count))
}

// ObservePushPullSnapshotDurationSeconds adds an observation
// for creating snapshot for the response pack.
func (m *Metrics) ObservePushPullSnapshotDurationSeconds(seconds float64) {
	m.pushPullSnapshotDurationSeconds.Observe(seconds)
}

// AddPushPullSnapshotBytes adds the snapshot byte size of response pack.
func (m *Metrics) AddPushPullSnapshotBytes(bytes int) {
	m.pushPullSnapshotBytesTotal.Add(float64(bytes))
}

// RegisterGRPCServer registers the given gRPC server.
func (m *Metrics) RegisterGRPCServer(server *grpc.Server) {
	m.serverMetrics.InitializeMetrics(server)
}

// Registry returns the registry of this metrics.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}
