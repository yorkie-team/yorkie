package time_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestTicket(t *testing.T) {
	t.Run("get base64 from actorId of ticket test", func(t *testing.T) {
		actorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")
		ticket := time.NewTicket(0, 0, actorID)
		assert.Equal(t, actorID.StringBase64(), ticket.ActorIDBase64())
	})

	t.Run("get bytes from actorId of ticket test", func(t *testing.T) {
		hexString := "0123456789abcdef01234567"
		actorID, _ := time.ActorIDFromHex(hexString)
		ticket := time.NewTicket(0, 0, actorID)
		assert.Equal(t, actorID.Bytes(), ticket.ActorIDBytes())
	})

	t.Run("constructor and getter method test", func(t *testing.T) {
		actorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")
		ticket := time.NewTicket(0, 1, actorID)
		assert.Equal(t, int64(0), ticket.Lamport())
		assert.Equal(t, uint32(1), ticket.Delimiter())
		assert.Equal(t, actorID, ticket.ActorID())

		assert.Equal(t, "0:1:"+ticket.ActorIDBase64(), ticket.Key())
		assert.Equal(t, "0:1:"+ticket.ActorIDBase64()[14:16],
			ticket.ToTestString())
	})

	t.Run("ticket comparing test", func(t *testing.T) {
		beforeActorID, _ := time.ActorIDFromHex("0000000000abcdef01234567")
		afterActorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")

		before := time.NewTicket(0, 0, beforeActorID)
		after := time.NewTicket(1, 0, afterActorID)
		assert.True(t, after.After(before))
		assert.False(t, before.After(after))

		before = time.NewTicket(0, 0, beforeActorID)
		after = time.NewTicket(0, 0, afterActorID)
		assert.True(t, after.After(before))
		assert.False(t, before.After(after))

		before = time.NewTicket(0, 0, beforeActorID)
		after = time.NewTicket(0, 1, beforeActorID)
		assert.True(t, after.After(before))
		assert.False(t, before.After(after))

		assert.False(t, before.After(before))
	})
}
