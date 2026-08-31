/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package warehouse

import "github.com/yorkie-team/yorkie/api/types/events"

// metricDesc describes one warehouse metric's base and summary shapes so the
// dual-read query builders can be shared across the six metrics. Each metric
// counts distinct idColumn per (project, day) over baseTable, and the daily
// summary lives in summaryTable with an HLL_UNION column named hllColumn.
type metricDesc struct {
	// baseTable is the raw event table, e.g. "user_events".
	baseTable string
	// idColumn is the identifier counted distinct, e.g. "user_id".
	idColumn string
	// summaryTable is the decoupled daily HLL summary, e.g. "sum_user_hll_daily".
	summaryTable string
	// hllColumn is the HLL_UNION sketch column in summaryTable, e.g. "user_hll".
	hllColumn string
	// byChannel is true for the session metric, which groups by channel_key so
	// one table serves both sessions and peak sessions per channel.
	byChannel bool
	// eventType, when non-empty, filters the base query and keys the summary,
	// used only by the client metric ("client-activated").
	eventType string
}

var (
	descUser = metricDesc{
		baseTable:    "user_events",
		idColumn:     "user_id",
		summaryTable: "sum_user_hll_daily",
		hllColumn:    "user_hll",
	}
	descDocument = metricDesc{
		baseTable:    "document_events",
		idColumn:     "document_key",
		summaryTable: "sum_document_hll_daily",
		hllColumn:    "document_hll",
	}
	descChannel = metricDesc{
		baseTable:    "channel_events",
		idColumn:     "channel_key",
		summaryTable: "sum_channel_hll_daily",
		hllColumn:    "channel_hll",
	}
	descClient = metricDesc{
		baseTable:    "client_events",
		idColumn:     "client_id",
		summaryTable: "sum_client_hll_daily",
		hllColumn:    "client_hll",
		eventType:    string(events.ClientActivatedEvent),
	}
	descSession = metricDesc{
		baseTable:    "session_events",
		idColumn:     "session_id",
		summaryTable: "sum_session_hll_daily_ch",
		hllColumn:    "session_hll",
		byChannel:    true,
	}
)
