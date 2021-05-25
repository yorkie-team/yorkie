package rpc

import (
	"context"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
)

func (s *Server) Publish(_ context.Context, request *api.PublishRequest) (*api.PublishResponse, error) {
	actorID, err := time.ActorIDFromBytes(request.PublisherId)
	if err != nil {
		log.Logger.Fatal(err)
		return nil, err
	}

	eventType, err := converter.FromEventType(request.DocEvent.EventType)
	if err != nil {
		log.Logger.Fatal(err)
		return nil, err
	}

	publisher, err := converter.FromClient(request.DocEvent.Publisher)
	if err != nil {
		log.Logger.Fatal(err)
		return nil, err
	}

	s.backend.PubSub.PublishToLocal(
		actorID,
		request.Topic,
		sync.DocEvent{
			Type:      eventType,
			DocKey:    request.DocEvent.DocKey,
			Publisher: *publisher,
		},
	)
	return &api.PublishResponse{
		Topic: request.Topic,
	}, nil
}
