package types

import (
	"encoding/json"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Client represents the Client that communicates with the Server.
type Client struct {
	ID           *time.ActorID
	PresenceInfo PresenceInfo
}

// NewClient creates a new Client from the given JSON.
func NewClient(encoded []byte) (*Client, error) {
	cli := &Client{}
	err := json.Unmarshal(encoded, cli)
	if err != nil {
		return nil, err
	}
	return cli, nil
}

// Marshal serializes the Client to JSON.
func (c *Client) Marshal() (string, error) {
	encoded, err := json.Marshal(c)
	if err != nil {
		return "", err
	}

	return string(encoded), nil
}

// Presence represents custom presence that can be defined in the client.
type Presence map[string]string

// PresenceInfo is a presence information with logical clock.
type PresenceInfo struct {
	Clock    int32
	Presence Presence
}

// Update updates the given presence information with the given clock.
func (i *PresenceInfo) Update(info PresenceInfo) bool {
	if info.Clock > i.Clock {
		i.Clock = info.Clock
		i.Presence = info.Presence
		return true
	}
	return false
}
