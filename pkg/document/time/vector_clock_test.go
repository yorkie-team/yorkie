package time_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
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

	t.Run("MaxVectorClock initial ID test", func(t *testing.T) {
		vec := helper.MaxVectorClock()

		assert.Equal(t, time.VectorClock{
			time.InitialActorID.String(): time.MaxLamport,
		}, vec.MinSyncedVector())
	})

	t.Run("MaxVectorClock arbitrary IDs test", func(t *testing.T) {
		newID1, err := time.ActorIDFromHex("111111111111111111111111")
		assert.NoError(t, err)

		newID2, err := time.ActorIDFromHex("222222222222222222222222")
		assert.NoError(t, err)

		vec := helper.MaxVectorClock(newID1, newID2)

		assert.Equal(t, time.VectorClock{
			newID1.String(): time.MaxLamport,
			newID2.String(): time.MaxLamport,
		}, vec.MinSyncedVector())
	})
}
