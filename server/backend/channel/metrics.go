/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package channel

import (
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/heap"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
)

var (
	// TopChannelSessionsMaxSize is the maximum size of top channel sessions.
	TopChannelSessionsMaxSize = 10
)

// Metrics is used to collect prometheus metrics.
type Metrics struct {
	Hostname string
	Metrics  *prometheus.Metrics
}

// channelMetric represents metrics for a single channel.
type channelMetric struct {
	Key         types.ChannelRefKey
	ProjectName string
	Sessions    int
}

// projectKey represents pair of project ID and its name.
type projectKey struct {
	ID   types.ID
	Name string
}

// channelMetrics aggregates channel and session metrics by project.
type channelMetrics struct {
	channelsByProject map[projectKey]int
	sessionsByProject map[projectKey]int
	topChannels       *heap.Heap[channelMetric]
}

// newChannelMetrics creates a new channelMetrics instance.
func newChannelMetrics() *channelMetrics {
	return &channelMetrics{
		channelsByProject: make(map[projectKey]int),
		sessionsByProject: make(map[projectKey]int),
		topChannels: heap.New(TopChannelSessionsMaxSize, func(a, b channelMetric) bool {
			return a.Sessions < b.Sessions
		}),
	}
}

// record adds metrics for a single channel.
func (pm *channelMetrics) record(project *types.Project, key types.ChannelRefKey, sessions int) {
	metricKey := projectKey{ID: project.ID, Name: project.Name}
	pm.channelsByProject[metricKey]++
	pm.sessionsByProject[metricKey] += sessions
	pm.topChannels.Push(channelMetric{
		Key:         key,
		ProjectName: project.Name,
		Sessions:    sessions,
	})
}

// publish sends all collected metrics to Prometheus.
func (pm *channelMetrics) publish(m *Metrics) {
	pm.publishChannelCounts(m)
	pm.publishSessionCounts(m)
	pm.publishTopChannels(m)
}

// publishChannelCounts updates channel count metrics.
func (pm *channelMetrics) publishChannelCounts(m *Metrics) {
	m.Metrics.ResetChannelTotal()
	for pInfo, count := range pm.channelsByProject {
		m.Metrics.SetChannelTotal(m.Hostname, pInfo.ID, pInfo.Name, count)
	}
}

// publishSessionCounts updates session count metrics.
func (pm *channelMetrics) publishSessionCounts(m *Metrics) {
	m.Metrics.ResetChannelSessionsTotal()
	for pInfo, count := range pm.sessionsByProject {
		m.Metrics.SetChannelSessionsTotal(m.Hostname, pInfo.ID, pInfo.Name, count)
	}
}

// publishTopChannels updates top-N channel metrics.
func (pm *channelMetrics) publishTopChannels(m *Metrics) {
	m.Metrics.ResetChannelSessionsTopN()
	if pm.topChannels.IsEmpty() {
		return
	}

	for _, ch := range pm.topChannels.Items() {
		m.Metrics.SetChannelSessionsTopN(
			m.Hostname,
			ch.Key.ProjectID,
			ch.ProjectName,
			ch.Key.ChannelKey.String(),
			ch.Sessions,
		)
	}
}
