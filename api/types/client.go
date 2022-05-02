package types

import (
	"encoding/json"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Client represents the Client that communicates with the Server.
type Client struct {
	ID           *time.ActorID
	MetadataInfo MetadataInfo
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

// Metadata represents custom metadata that can be defined in the client.
type Metadata map[string]string

// MetadataInfo is a metadata information with logical clock.
type MetadataInfo struct {
	Clock int32
	Data  Metadata
}

// Update updates the given metadata information with the given clock.
func (i *MetadataInfo) Update(info MetadataInfo) bool {
	if info.Clock > i.Clock {
		i.Clock = info.Clock
		i.Data = info.Data
		return true
	}
	return false
}
