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

// Package prometheus provides a Prometheus metrics exporter.
package prometheus

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/internal/version"
)

const (
	namespace        = "yorkie"
	sdkTypeLabel     = "sdk_type"
	sdkVersionLabel  = "sdk_version"
	methodLabel      = "grpc_method"
	projectIDLabel   = "project_id"
	projectNameLabel = "project_name"
	hostnameLabel    = "hostname"
	taskTypeLabel    = "task_type"
)

var (
	// emptyProject is used when the project is not specified.
	emptyProject = &types.Project{
		Name: "",
		ID:   types.ID(""),
	}
)

// Metrics manages the metric information that Yorkie is trying to measure.
type Metrics struct {
	registry *prometheus.Registry

	serverVersion        *prometheus.GaugeVec
	serverHandledCounter *prometheus.CounterVec

	pushPullResponseSeconds         prometheus.Histogram
	pushPullReceivedChangesTotal    prometheus.Counter
	pushPullSentChangesTotal        prometheus.Counter
	pushPullReceivedOperationsTotal prometheus.Counter
	pushPullSentOperationsTotal     prometheus.Counter
	pushPullSnapshotDurationSeconds prometheus.Histogram
	pushPullSnapshotBytesTotal      prometheus.Counter

	backgroundGoroutinesTotal *prometheus.GaugeVec

	userAgentTotal *prometheus.CounterVec
}

// NewMetrics creates a new instance of Metrics.
func NewMetrics() (*Metrics, error) {
	reg := prometheus.NewRegistry()

	if err := reg.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, fmt.Errorf("register process collector: %w", err)
	}
	if err := reg.Register(collectors.NewGoCollector()); err != nil {
		return nil, fmt.Errorf("register go collector: %w", err)
	}

	metrics := &Metrics{
		registry: reg,
		serverVersion: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "server",
			Name:      "version",
			Help:      "Which version is running. 1 for 'server_version' label with current version.",
		}, []string{"server_version"}),
		serverHandledCounter: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "rpc",
			Name:      "server_handled_total",
			Help:      "Total number of RPCs completed on the server, regardless of success or failure.",
		}, []string{"rpc_type", "rpc_service", "rpc_method", "rpc_code"}),
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
		pushPullReceivedOperationsTotal: promauto.With(reg).NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "pushpull",
				Name:      "received_operations_total",
				Help: "The total count of operations included in request" +
					" packs in PushPull.",
			}),
		pushPullSentOperationsTotal: promauto.With(reg).NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "pushpull",
			Name:      "sent_operations_total",
			Help: "The total count of operations included in response" +
				" packs in PushPull.",
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
		backgroundGoroutinesTotal: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "background",
			Name:      "goroutines_total",
			Help:      "The total number of goroutines attached by a particular background task.",
		}, []string{taskTypeLabel}),
		userAgentTotal: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "user_agent",
			Name:      "total",
			Help:      "description",
		}, []string{
			sdkTypeLabel,
			sdkVersionLabel,
			methodLabel,
			projectIDLabel,
			projectNameLabel,
			hostnameLabel,
		}),
	}

	metrics.serverVersion.With(prometheus.Labels{
		"server_version": version.Version,
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

// AddPushPullReceivedOperations sets the number of operations
// included in the request pack of PushPull.
func (m *Metrics) AddPushPullReceivedOperations(count int) {
	m.pushPullReceivedOperationsTotal.Add(float64(count))
}

// AddPushPullSentOperations adds the number of operations
// included in the response pack of PushPull.
func (m *Metrics) AddPushPullSentOperations(count int) {
	m.pushPullSentOperationsTotal.Add(float64(count))
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

// AddUserAgent adds the number of user agent.
func (m *Metrics) AddUserAgent(
	hostname string,
	project *types.Project,
	sdkType, sdkVersion string,
	methodName string,
) {
	m.userAgentTotal.With(prometheus.Labels{
		sdkTypeLabel:     sdkType,
		sdkVersionLabel:  sdkVersion,
		methodLabel:      methodName,
		projectIDLabel:   project.ID.String(),
		projectNameLabel: project.Name,
		hostnameLabel:    hostname,
	}).Inc()
}

// AddUserAgentWithEmptyProject adds the number of user agent with empty project.
func (m *Metrics) AddUserAgentWithEmptyProject(hostname string, sdkType, sdkVersion, methodName string) {
	m.AddUserAgent(hostname, emptyProject, sdkType, sdkVersion, methodName)
}

// AddServerHandledCounter adds the number of RPCs completed on the server.
func (m *Metrics) AddServerHandledCounter(
	rpcType,
	rpcService,
	rpcMethod,
	rpcCode string,
) {
	m.serverHandledCounter.With(prometheus.Labels{
		"rpc_type":    rpcType,
		"rpc_service": rpcService,
		"rpc_method":  rpcMethod,
		"rpc_code":    rpcCode,
	}).Inc()
}

// AddBackgroundGoroutines adds the number of goroutines attached by a particular background task.
func (m *Metrics) AddBackgroundGoroutines(taskType string) {
	m.backgroundGoroutinesTotal.With(prometheus.Labels{
		taskTypeLabel: taskType,
	}).Inc()
}

// RemoveBackgroundGoroutines removes the number of goroutines attached by a particular background task.
func (m *Metrics) RemoveBackgroundGoroutines(taskType string) {
	m.backgroundGoroutinesTotal.With(prometheus.Labels{
		taskTypeLabel: taskType,
	}).Dec()
}

// Registry returns the registry of this metrics.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}
