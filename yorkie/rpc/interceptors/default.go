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

package interceptors

import (
	"context"
	gotime "time"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/yorkie/auth"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
	"github.com/yorkie-team/yorkie/yorkie/clients"
	"github.com/yorkie-team/yorkie/yorkie/packs"
)

// DefaultInterceptor is a interceptor for default.
type DefaultInterceptor struct {
}

// NewDefaultInterceptor creates a new instance of DefaultInterceptor.
func NewDefaultInterceptor() *DefaultInterceptor {
	return &DefaultInterceptor{}
}

// Unary creates a unary server interceptor for default.
func (i *DefaultInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		start := gotime.Now()
		resp, err := handler(ctx, req)
		if err != nil {
			log.Logger.Errorf("RPC : %q %s: %q => %q", info.FullMethod, gotime.Since(start), req, err)
			return nil, toStatusError(err)
		}

		log.Logger.Infof("RPC : %q %s", info.FullMethod, gotime.Since(start))
		return resp, err
	}
}

// Stream creates a stream server interceptor for default.
func (i *DefaultInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		start := gotime.Now()
		err := handler(srv, ss)
		if err != nil {
			log.Logger.Infof("RPC : stream %q %s => %q", info.FullMethod, gotime.Since(start), err.Error())
			return toStatusError(err)
		}

		log.Logger.Infof("RPC : stream %q %s", info.FullMethod, gotime.Since(start))
		return err
	}
}

// toStatusError returns a status.Error from the given logic error. If an error
// occurs while executing logic in API handler, gRPC status.error should be
// returned so that the client can know more about the status of the request.
func toStatusError(err error) error {
	if errors.Is(err, auth.ErrNotAllowed) {
		return status.Error(codes.Unauthenticated, err.Error())
	}

	if errors.Is(err, converter.ErrPackRequired) ||
		errors.Is(err, converter.ErrCheckpointRequired) ||
		errors.Is(err, time.ErrInvalidHexString) ||
		errors.Is(err, db.ErrInvalidID) ||
		errors.Is(err, clients.ErrInvalidClientID) ||
		errors.Is(err, clients.ErrInvalidClientKey) {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	if errors.Is(err, converter.ErrUnsupportedOperation) ||
		errors.Is(err, converter.ErrUnsupportedElement) ||
		errors.Is(err, converter.ErrUnsupportedEventType) ||
		errors.Is(err, converter.ErrUnsupportedValueType) ||
		errors.Is(err, converter.ErrUnsupportedCounterType) {
		return status.Error(codes.Unimplemented, err.Error())
	}

	if errors.Is(err, db.ErrClientNotFound) ||
		errors.Is(err, db.ErrDocumentNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}

	if errors.Is(err, db.ErrClientNotActivated) ||
		errors.Is(err, db.ErrDocumentNotAttached) ||
		errors.Is(err, db.ErrDocumentAlreadyAttached) ||
		errors.Is(err, packs.ErrInvalidServerSeq) ||
		errors.Is(err, db.ErrConflictOnUpdate) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}

	return status.Error(codes.Internal, err.Error())
}
