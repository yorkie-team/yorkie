package types

import (
	"encoding/json"
	"fmt"

	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Client represents the Client that communicates with the Server.
type Client struct {
	ID           *time.ActorID
	PresenceInfo presence.PresenceInfo
}

// NewClient creates a new Client from the given JSON.
func NewClient(encoded []byte) (*Client, error) {
	cli := &Client{}
	err := json.Unmarshal(encoded, cli)
	if err != nil {
		return nil, fmt.Errorf("unmarshal client: %w", err)
	}
	return cli, nil
}

// Marshal serializes the Client to JSON.
func (c *Client) Marshal() (string, error) {
	encoded, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshal client: %w", err)
	}

	return string(encoded), nil
}
