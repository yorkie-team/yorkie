package rpc

import (
	"context"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// BroadcastEvent publishes the given event to the given Topic.
func (s *Server) BroadcastEvent(
	ctx context.Context,
	request *api.BroadcastEventRequest,
) (*api.BroadcastEventResponse, error) {
	actorID, err := time.ActorIDFromBytes(request.PublisherId)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	docEvent, err := converter.FromDocEvent(request.Event)
	if err != nil {
		log.Logger.Errorf("%+v", err)
		return nil, err
	}

	s.backend.Coordinator.PublishToLocal(
		ctx,
		actorID,
		*docEvent,
	)

	return &api.BroadcastEventResponse{}, nil
}
