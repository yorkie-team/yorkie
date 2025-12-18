package types

import (
	"github.com/yorkie-team/yorkie/pkg/key"
)

// ChannelSummary represents a summary of channel.
type ChannelSummary struct {
	// Key is the key of the channel.
	Key key.Key

	// SessionCount is the count of channel.
	SessionCount int64
}
