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

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
)

// hintConnector answers every query that names a rollup with hintErr, and
// every other query with errCaptured, recording each query's SQL.
type hintConnector struct {
	hintErr error
	queries []string
}

func (c *hintConnector) Connect(context.Context) (driver.Conn, error) { return &hintConn{c: c}, nil }
func (c *hintConnector) Driver() driver.Driver                        { return captureDriver{} }

type hintConn struct {
	c *hintConnector
}

func (c *hintConn) Prepare(string) (driver.Stmt, error) { return nil, errCaptured }
func (c *hintConn) Close() error                        { return nil }
func (c *hintConn) Begin() (driver.Tx, error)           { return nil, errCaptured }

func (c *hintConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.c.queries = append(c.c.queries, query)
	if strings.Contains(query, "[_SYNC_MV_]") {
		return nil, c.c.hintErr
	}
	return nil, errCaptured
}

// sessionsReads runs both sessions reads against r over a window the summary
// covers, so each one names the rollup.
func sessionsReads(t *testing.T, r *StarRocks) []error {
	t.Helper()

	today := todayUTC()
	r.coverage.probed = map[string]summaryDays{
		descSession.summaryTable: {Min: today.AddDate(0, 0, -90), Max: today.AddDate(0, 0, -2)},
	}
	r.coverage.fetchedAt = time.Now()

	from, to := today.AddDate(0, 0, -30), today
	_, countErr := r.GetSessionsCount(context.Background(), types.ID("p1"), from, to)
	_, seriesErr := r.GetSessions(context.Background(), types.ID("p1"), from, to)
	return []error{countErr, seriesErr}
}

// A cluster whose summary table has no rl_session_daily yet, or whose build has
// not reached FINISHED, answers the hinted read with Unknown table. Each
// sessions read then runs again on the summary table, the plan it had before
// the hint, rather than failing GetProjectStats as a whole with it.
func TestUnknownRollupRetriesOnTheSummaryTable(t *testing.T) {
	unknown := &mysql.MySQLError{
		Number:  5502,
		Message: "Getting analyzing error. Detail message: Unknown table 'yorkie.rl_session_daily'.",
	}
	conn := &hintConnector{hintErr: unknown}
	r := &StarRocks{conf: &Config{SummaryEnabled: true}, driver: sql.OpenDB(conn)}
	defer func() { _ = r.Close() }()

	for _, err := range sessionsReads(t, r) {
		require.ErrorIs(t, err, errCaptured, "the retry's own answer is what the read returns")
	}
	require.Len(t, conn.queries, 4, "one hinted attempt and one retry per read")
	for i, q := range conn.queries {
		got := norm(q)
		if i%2 == 0 {
			assert.Contains(t, got, "FROM rl_session_daily [_SYNC_MV_] WHERE project_id = 'p1'")
			continue
		}
		assert.Contains(t, got, "FROM sum_session_hll_daily_ch WHERE project_id = 'p1'")
		assert.NotContains(t, got, "_SYNC_MV_")
	}
}

// Only Unknown table says the rollup does not resolve. StarRocks reports every
// analysis error as 5502, and a timeout or a lost BE also arrives as a server
// error; those are the read's answer, returned as is rather than retried on
// the heavier plan.
func TestOtherHintedReadErrorsAreNotRetried(t *testing.T) {
	for name, hintErr := range map[string]error{
		"unreachable":  errCaptured,
		"server error": &mysql.MySQLError{Number: 1064, Message: "Backend node not found"},
		"analysis":     &mysql.MySQLError{Number: 5502, Message: "Getting analyzing error. Detail message: timeout"},
	} {
		t.Run(name, func(t *testing.T) {
			conn := &hintConnector{hintErr: hintErr}
			r := &StarRocks{conf: &Config{SummaryEnabled: true}, driver: sql.OpenDB(conn)}
			defer func() { _ = r.Close() }()

			for _, err := range sessionsReads(t, r) {
				require.ErrorIs(t, err, hintErr)
			}
			require.Len(t, conn.queries, 2, "no retry")
		})
	}
}
