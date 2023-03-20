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
	"os"
	"strings"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	grpcmetadata "google.golang.org/grpc/metadata"

	grpcprometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/internal/version"
)

const (
	namespace          = "yorkie"
	yorkieSDKType      = "yorkie_sdk_type"
	yorkieSDKVersion   = "yorkie_sdk_version"
	grpcMethod         = "grpc_method"
	projectID          = "project_id"
	projectName        = "project_name"
	serverInstanceName = "server_instance_name"
)

var (
	podName       = os.Getenv("POD_NAME") // TODO: add pod name env in cluster mode
	containerName = os.Getenv("HOSTNAME") // docker container name
	defaultName   = "local"
)

// Metrics manages the metric information that Yorkie is trying to measure.
type Metrics struct {
	registry      *prometheus.Registry
	serverMetrics *grpcprometheus.ServerMetrics

	serverVersion *prometheus.GaugeVec

	pushPullResponseSeconds         prometheus.Histogram
	pushPullReceivedChangesTotal    prometheus.Counter
	pushPullSentChangesTotal        prometheus.Counter
	pushPullReceivedOperationsTotal prometheus.Counter
	pushPullSentOperationsTotal     prometheus.Counter
	pushPullSnapshotDurationSeconds prometheus.Histogram
	pushPullSnapshotBytesTotal      prometheus.Counter

	yorkieUserAgent *prometheus.CounterVec
}

// NewMetrics creates a new instance of Metrics.
func NewMetrics() (*Metrics, error) {
	reg := prometheus.NewRegistry()
	serverMetrics := grpcprometheus.NewServerMetrics()

	if err := reg.Register(serverMetrics); err != nil {
		return nil, fmt.Errorf("register server metrics: %w", err)
	}
	if err := reg.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, fmt.Errorf("register process collector: %w", err)
	}
	if err := reg.Register(collectors.NewGoCollector()); err != nil {
		return nil, fmt.Errorf("register go collector: %w", err)
	}

	metrics := &Metrics{
		registry:      reg,
		serverMetrics: serverMetrics,
		serverVersion: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "server",
			Name:      "version",
			Help:      "Which version is running. 1 for 'server_version' label with current version.",
		}, []string{"server_version"}),
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
		yorkieUserAgent: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "user_agent",
			Name:      "type_version_count",
			Help:      "description",
		}, []string{
			yorkieSDKType,
			yorkieSDKVersion,
			grpcMethod,
			projectID,
			projectName,
			serverInstanceName,
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

// AddYorkieUserAgent adds the request count per yorkie sdk type and version
func (m *Metrics) AddYorkieUserAgent(labels prometheus.Labels) {
	m.yorkieUserAgent.With(labels).Inc()
}

// RegisterGRPCServer registers the given gRPC server.
func (m *Metrics) RegisterGRPCServer(server *grpc.Server) {
	m.serverMetrics.InitializeMetrics(server)
}

// ServerMetrics returns the serverMetrics.
func (m *Metrics) ServerMetrics() *grpcprometheus.ServerMetrics {
	return m.serverMetrics
}

// Registry returns the registry of this metrics.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// MetricYorkieUserAgent metric yorkie sdk with information
func (m *Metrics) MetricYorkieUserAgent(ctx context.Context, project *types.Project, methodName string) {

	data, ok := grpcmetadata.FromIncomingContext(ctx)
	if !ok {
		return
	}

	sdkType, sdkVersion := getYorkieSDKTypeAndVersion(data)
	if sdkType == "" || sdkVersion == "" {
		return
	}

	m.AddYorkieUserAgent(prometheus.Labels{
		yorkieSDKType:      sdkType,
		yorkieSDKVersion:   sdkVersion,
		grpcMethod:         methodName,
		projectID:          project.ID.String(),
		projectName:        project.Name,
		serverInstanceName: getServerInstanceName(),
	})
}

// MetricYorkieUserAgentWithDefaultProject metric yorkie sdk with information (default project)
func (m *Metrics) MetricYorkieUserAgentWithDefaultProject(ctx context.Context, grpcMethod string) {
	project := &types.Project{
		Name: "default",
		ID:   types.ID("000000000000000000000000"),
	}

	m.MetricYorkieUserAgent(ctx, project, grpcMethod)
}

func getYorkieSDKTypeAndVersion(data grpcmetadata.MD) (string, string) {
	yorkieUserAgentSlice := data["x-yorkie-user-agent"]
	if len(yorkieUserAgentSlice) == 0 {
		return "", ""
	}

	yorkieUserAgent := yorkieUserAgentSlice[0]
	agent := strings.Split(yorkieUserAgent, "/")
	return agent[0], agent[1]
}

func getServerInstanceName() string {
	switch {
	case podName != "":
		return podName
	case containerName != "":
		return containerName
	default:
		return defaultName
	}
}
