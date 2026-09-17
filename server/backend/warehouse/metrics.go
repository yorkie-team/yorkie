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
// dual-read query builders can be shared across the metrics. Each metric
// derives its value from idColumn per (project, day) over baseTable, and the
// daily summary lives in summaryTable.
type metricDesc struct {
	// baseTable is the raw event table, e.g. "user_events".
	baseTable string
	// idColumn is the identifier counted distinct, e.g. "user_id".
	idColumn string
	// summaryTable is the decoupled daily summary, e.g. "sum_user_hll_daily".
	summaryTable string
	// hllColumn is the HLL_UNION sketch column in summaryTable, e.g. "user_hll".
	// It is empty for a metric whose summary stores a plain number instead of a
	// sketch.
	hllColumn string
	// peakColumn is the plain per-day peak column in summaryTable, e.g.
	// "peak_sessions". It is set only for the peak metric, whose daily value is
	// a MAX over independent buckets and therefore needs no sketch to stay
	// mergeable across days.
	peakColumn string
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
	// descSession counts distinct sessions. Its summary is keyed by channel as
	// well as day, a shape the sessions reads themselves do not need: they
	// union every channel-day sketch of a day back together. The peak metric,
	// which does need the per-channel breakdown, no longer reads this table.
	descSession = metricDesc{
		baseTable:    "session_events",
		idColumn:     "session_id",
		summaryTable: "sum_session_hll_daily_ch",
		hllColumn:    "session_hll",
	}
	// descPeak is the daily peak sessions per channel. Its summary is
	// precomputed from descSession's per-channel sketches, one plain integer per
	// (project, day), so a read costs days rather than channels x days.
	descPeak = metricDesc{
		baseTable:    "session_events",
		idColumn:     "session_id",
		summaryTable: "sum_session_peak_daily",
		peakColumn:   "peak_sessions",
	}

	// allDescs is every metric, one per summary table, walked by the coverage
	// probe. A metric missing from this list probes no coverage, so its reads
	// silently fall back to base-only: right numbers, whole-history scan.
	// Peak carries its own entry because its summary is a separate table that
	// can lag descSession's: the split a read takes must follow the coverage of
	// the table that read actually touches.
	allDescs = []metricDesc{descUser, descDocument, descChannel, descClient, descSession, descPeak}
)
