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

		assert.Equal(t, time.VectorClock{"1": 0, "2": 1, "3": 0}, minVec)
	})

	t.Run("ChangeActorID test", func(t *testing.T) {
		initID := time.InitialActorID.String()
		newID, err := time.ActorIDFromHex("111111111111111111111111")
		assert.NoError(t, err)

		vec := time.InitialSyncedVectorMap(1)
		vec["B"] = time.VectorClock{"B": 3}
		vec[initID]["B"] = 2

		vec.ChangeActorID(newID)

		assert.Equal(t, time.SyncedVectorMap{
			newID.String(): time.VectorClock{newID.String(): 1, "B": 2},
			"B":            time.VectorClock{"B": 3}}, vec)
	})
}
