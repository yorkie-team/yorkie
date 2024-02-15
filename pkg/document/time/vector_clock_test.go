package time_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestVectorClock(t *testing.T) {
	t.Run("ChangeActorID test", func(t *testing.T) {
		newID, err := time.ActorIDFromHex("111111111111111111111111")
		assert.NoError(t, err)
		newID2, err := time.ActorIDFromHex("222222222222222222222222")
		assert.NoError(t, err)

		vec := time.InitialVectorClock()
		vec[newID2.String()] = 2

		vec.ChangeActorID(newID)

		assert.Equal(t, time.VectorClock{
			newID.String():  0,
			newID2.String(): 2,
		}, vec)
	})

	t.Run("MaxVectorClock initial ID test", func(t *testing.T) {
		vec := helper.MaxVectorClock()

		assert.Equal(t, time.VectorClock{
			time.InitialActorID.String(): time.MaxLamport,
		}, vec)
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
		}, vec)
	})
}
