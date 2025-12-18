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

// channelSessionCount represents channel session count for metrics.
type channelSessionCount struct {
	ProjectName string
	ProjectID   types.ID
	ChannelKey  string
	Sessions    int
}

// projectInfo holds project ID and name for metrics aggregation.
type projectInfo struct {
	ID   types.ID
	Name string
}

// projectMetrics aggregates channel and session metrics by project.
type projectMetrics struct {
	channels map[projectInfo]int
	sessions map[projectInfo]int
	top      *heap.Heap[channelSessionCount]
}

// newProjectMetrics creates a new projectMetrics instance.
func newProjectMetrics() *projectMetrics {
	return &projectMetrics{
		channels: make(map[projectInfo]int),
		sessions: make(map[projectInfo]int),
		top: heap.New(TopChannelSessionsMaxSize, func(a, b channelSessionCount) bool {
			return a.Sessions < b.Sessions
		}),
	}
}

// record adds metrics for a single channel.
func (pm *projectMetrics) record(pInfo projectInfo, channelKey string, sessions int) {
	pm.channels[pInfo]++
	pm.sessions[pInfo] += sessions
	pm.top.Push(channelSessionCount{
		ProjectID:   pInfo.ID,
		ProjectName: pInfo.Name,
		ChannelKey:  channelKey,
		Sessions:    sessions,
	})
}

// publish sends all collected metrics to Prometheus.
func (pm *projectMetrics) publish(m *Metrics) {
	pm.publishChannelCounts(m)
	pm.publishSessionCounts(m)
	pm.publishTopChannels(m)
}

// publishChannelCounts updates channel count metrics.
func (pm *projectMetrics) publishChannelCounts(m *Metrics) {
	m.Metrics.ResetChannelTotal()
	for pInfo, count := range pm.channels {
		m.Metrics.SetChannelTotal(m.Hostname, pInfo.ID, pInfo.Name, count)
	}
}

// publishSessionCounts updates session count metrics.
func (pm *projectMetrics) publishSessionCounts(m *Metrics) {
	m.Metrics.ResetChannelSessionsTotal()
	for pInfo, count := range pm.sessions {
		m.Metrics.SetChannelSessionsTotal(m.Hostname, pInfo.ID, pInfo.Name, count)
	}
}

// publishTopChannels updates top-N channel metrics.
func (pm *projectMetrics) publishTopChannels(m *Metrics) {
	m.Metrics.ResetChannelSessionsTopN()
	if pm.top.IsEmpty() {
		return
	}

	for _, ch := range pm.top.Items() {
		m.Metrics.SetChannelSessionsTopN(m.Hostname, ch.ProjectID, ch.ProjectName, ch.ChannelKey, ch.Sessions)
	}
}
