package types

import "github.com/yorkie-team/yorkie/pkg/document/time"

// Client represents the Client that communicates with the Agent.
type Client struct {
	ID       *time.ActorID
	Metadata map[string]string
}

