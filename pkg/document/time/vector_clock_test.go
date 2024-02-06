package time_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestVectorClock(t *testing.T) {
	t.Run("MinSyncedVector test", func(t *testing.T) {
		vec := time.SyncedVectorMap{
			"1": time.VectorClock{"1": 1, "2": 1},
			"2": time.VectorClock{"2": 2, "3": 2},
		}

		minVec := vec.MinSyncedVector()

		assert.Equal(t, time.VectorClock{"1": 0, "2": 1, "3": 0}, *minVec)
	})
}
