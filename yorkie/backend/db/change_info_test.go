package db_test

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
)

func TestChangeInfo(t *testing.T) {
	t.Run("comparing actorID equals after calling ToChange test", func(t *testing.T) {
		actorID := time.ActorID{}
		_, err := rand.Read(actorID[:])
		assert.NoError(t, err)

		expectedID := actorID.Hex()
		changeInfo := db.ChangeInfo{
			Actor: db.ID(expectedID),
		}

		change, err := changeInfo.ToChange()
		assert.NoError(t, err)
		assert.Equal(t, change.ID().Actor().Hex(), expectedID)
	})
}
