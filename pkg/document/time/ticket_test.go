package time_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestTicket(t *testing.T) {
	t.Run("get hex from actorId of ticket test", func(t *testing.T) {
		actorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")
		ticket := time.NewTicket(0, 0, actorID)
		assert.Equal(t, actorID.String(), ticket.ActorIDHex())
	})

	t.Run("get bytes from actorId of ticket test", func(t *testing.T) {
		hexString := "0123456789abcdef01234567"
		actorID, _ := time.ActorIDFromHex(hexString)
		ticket := time.NewTicket(0, 0, actorID)
		assert.Equal(t, actorID.Bytes(), ticket.ActorIDBytes())
	})
}
