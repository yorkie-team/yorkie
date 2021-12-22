/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package rpc

import (
	"context"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/log"
)

// clusterServer is a normal server that processes the broadcast by the agent.
type clusterServer struct {
	backend *backend.Backend
}

// newClusterServer creates a new instance of clusterServer.
func newClusterServer(be *backend.Backend) *clusterServer {
	return &clusterServer{backend: be}
}

// BroadcastEvent publishes the given event to the given document.
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

	switch docEvent.Type {
	case types.DocumentsWatchedEvent,
		types.DocumentsUnwatchedEvent,
		types.DocumentsChangedEvent:
		s.backend.Coordinator.PublishToLocal(ctx, actorID, *docEvent)
	case types.MetadataChangedEvent:
		if _, err := s.backend.Coordinator.UpdateMetadata(
			ctx,
			&docEvent.Publisher,
			docEvent.DocumentKeys,
		); err != nil {
			return nil, err
		}

		s.backend.Coordinator.PublishToLocal(ctx, actorID, *docEvent)
	}

	return &api.BroadcastEventResponse{}, nil
}
