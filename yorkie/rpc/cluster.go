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
	_ context.Context,
	request *api.BroadcastEventRequest,
) (*api.BroadcastEventResponse, error) {
	actorID, err := time.ActorIDFromBytes(request.PublisherId)
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	docEvent, err := converter.FromDocEvent(request.DocEvent)
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	s.backend.PubSub.PublishToLocal(
		actorID,
		request.Topic,
		*docEvent,
	)

	return &api.BroadcastEventResponse{
		Topic: request.Topic,
	}, nil
}
