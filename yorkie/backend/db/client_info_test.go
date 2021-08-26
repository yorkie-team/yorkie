package db_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
)

func TestClientInfo(t *testing.T) {
	t.Run("attach/detach document test", func(t *testing.T) {
		docID := db.ID("000000000000000000000000")
		clientInfo := db.ClientInfo{
			Status: db.ClientActivated,
		}

		err := clientInfo.AttachDocument(docID)
		assert.NoError(t, err)
		isAttached, err := clientInfo.IsAttached(docID)
		assert.NoError(t, err)
		assert.True(t, isAttached)

		err = clientInfo.UpdateCheckpoint(docID, checkpoint.Max)
		assert.NoError(t, err)

		err = clientInfo.DetachDocument(docID)
		assert.NoError(t, err)
		isAttached, err = clientInfo.IsAttached(docID)
		assert.NoError(t, err)
		assert.False(t, isAttached)
	})
}
