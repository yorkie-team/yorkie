package rpc

import (
	"context"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
)

// Publish publishes the given event to the given Topic.
func (s *Server) Publish(_ context.Context, request *api.PublishRequest) (*api.PublishResponse, error) {
	actorID, err := time.ActorIDFromBytes(request.PublisherId)
	if err != nil {
		log.Logger.Fatal(err)
		return nil, err
	}

	docEvent, err := converter.FromDocEvent(request.DocEvent)
	if err != nil {
		log.Logger.Fatal(err)
		return nil, err
	}

	s.backend.PubSub.PublishToLocal(
		actorID,
		request.Topic,
		*docEvent,
	)

	return &api.PublishResponse{
		Topic: request.Topic,
	}, nil
}
