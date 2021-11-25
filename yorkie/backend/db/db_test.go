package db_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/yorkie/backend/db"
)

func TestID(t *testing.T) {
	t.Run("get ID from hex test", func(t *testing.T) {
		str := "0123456789abcdef01234567"
		ID := db.ID(str)
		assert.Equal(t, str, ID.String())
	})

	t.Run("get ID from bytes test", func(t *testing.T) {
		bytes := make([]byte, 12)
		ID := db.IDFromBytes(bytes)
		bytesID, err := ID.Bytes()
		assert.NoError(t, err)
		assert.Equal(t, bytes, bytesID)
	})
}
