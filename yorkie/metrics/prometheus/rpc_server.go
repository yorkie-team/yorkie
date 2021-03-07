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

// RPCServerMetrics can add metrics for RPC Server.
type RPCServerMetrics struct {
	subsystem string

	pushpullResponseSeconds prometheus.Histogram
	pushpullReceivedChanges prometheus.Counter
	pushpullSentChanges     prometheus.Counter
}

// NewRPCServerMetrics creates an instance of RPCServerMetrics.
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
		Help:      "The number of changes included in a request pack in PushPull API.",
	})
	r.pushpullSentChanges = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: r.subsystem,
		Name:      "pushpull_sent_changes",
		Help:      "The number of changes included in a response pack in PushPull API.",
	})
}

// ObservePushpullResponseSeconds adds response time metrics for PushPull API.
func (r *RPCServerMetrics) ObservePushpullResponseSeconds(seconds float64) {
	r.pushpullResponseSeconds.Observe(seconds)
}

// AddPushpullReceivedChanges adds the number of changes metric
// included in the request pack of the PushPull API.
func (r *RPCServerMetrics) AddPushpullReceivedChanges(count float64) {
	r.pushpullReceivedChanges.Add(count)
}

// AddPushpullSentChanges adds the number of changes metric
// included in the response pack of the PushPull API.
func (r *RPCServerMetrics) AddPushpullSentChanges(count float64) {
	r.pushpullSentChanges.Add(count)
}
