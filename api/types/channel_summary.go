package types

import (
	"github.com/yorkie-team/yorkie/pkg/key"
)

// PresenceSummary represents a summary of presence.
type ChannelSummary struct {
	// Key is the key of the channel.
	Key key.Key

	// PresenceCount is the count of presence.
	PresenceCount int64
}
