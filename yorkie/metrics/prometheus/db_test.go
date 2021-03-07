package prometheus

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDBMetrics(t *testing.T) {
	dbMetrics := NewDBMetrics()

	t.Run("observe pushpull snapshot duration seconds test", func(t *testing.T) {
		dbMetrics.ObservePushpullSnapshotDurationSeconds(2)
		dbMetrics.ObservePushpullSnapshotDurationSeconds(3)

		expected := `
			# HELP yorkie_db_pushpull_snapshot_duration_seconds The creation time of snapshot in PushPull API.
            # TYPE yorkie_db_pushpull_snapshot_duration_seconds histogram
            yorkie_db_pushpull_snapshot_duration_seconds_bucket{le="0.005"} 0
            yorkie_db_pushpull_snapshot_duration_seconds_bucket{le="0.01"} 0
            yorkie_db_pushpull_snapshot_duration_seconds_bucket{le="0.025"} 0
            yorkie_db_pushpull_snapshot_duration_seconds_bucket{le="0.05"} 0
            yorkie_db_pushpull_snapshot_duration_seconds_bucket{le="0.1"} 0
            yorkie_db_pushpull_snapshot_duration_seconds_bucket{le="0.25"} 0
            yorkie_db_pushpull_snapshot_duration_seconds_bucket{le="0.5"} 0
            yorkie_db_pushpull_snapshot_duration_seconds_bucket{le="1"} 0
            yorkie_db_pushpull_snapshot_duration_seconds_bucket{le="2.5"} 1
            yorkie_db_pushpull_snapshot_duration_seconds_bucket{le="5"} 2
            yorkie_db_pushpull_snapshot_duration_seconds_bucket{le="10"} 2
            yorkie_db_pushpull_snapshot_duration_seconds_bucket{le="+Inf"} 2
            yorkie_db_pushpull_snapshot_duration_seconds_sum 5
            yorkie_db_pushpull_snapshot_duration_seconds_count 2
		`
		if err := testutil.CollectAndCompare(dbMetrics.pushpullSnapshotDurationSeconds, strings.NewReader(expected)); err != nil {
			t.Errorf("unexpected collecting result:\n%s", err)
		}
	})
}
