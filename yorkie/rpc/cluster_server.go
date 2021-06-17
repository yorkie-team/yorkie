package rpc

import (
	"context"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/yorkie/backend"
)

// clusterServer is a normal server that processes the broadcast by the agent.
type clusterServer struct {
	backend *backend.Backend
}

// newClusterServer creates a new instance of clusterServer.
func newClusterServer(be *backend.Backend) *clusterServer {
	return &clusterServer{backend: be}
}

// BroadcastEvent publishes the given event to the given Topic.
func (s *clusterServer) BroadcastEvent(
	ctx context.Context,
	request *api.BroadcastEventRequest,
) (*api.BroadcastEventResponse, error) {
	actorID, err := time.ActorIDFromBytes(request.PublisherId)
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	docEvent, err := converter.FromDocEvent(request.Event)
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	s.backend.Coordinator.PublishToLocal(
		ctx,
		actorID,
		*docEvent,
	)

	return &api.BroadcastEventResponse{}, nil
}
