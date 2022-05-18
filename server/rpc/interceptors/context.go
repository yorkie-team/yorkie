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
	"strings"

	grpcmiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcmetadata "google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/grpchelper"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
)

// ContextInterceptor is an interceptor for building additional context.
type ContextInterceptor struct {
	backend *backend.Backend
}

// NewContextInterceptor creates a new instance of ContextInterceptor.
func NewContextInterceptor(be *backend.Backend) *ContextInterceptor {
	return &ContextInterceptor{
		backend: be,
	}
}

// Unary creates a unary server interceptor for building additional context.
func (i *ContextInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp interface{}, err error) {
		if isRPCService(info.FullMethod) {
			ctx, err := i.buildContext(ctx)
			if err != nil {
				return nil, err
			}

			return handler(ctx, req)
		}

		return handler(ctx, req)
	}
}

// Stream creates a stream server interceptor for building additional context.
func (i *ContextInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if isRPCService(info.FullMethod) {
			ctx := ss.Context()

			ctx, err := i.buildContext(ctx)
			if err != nil {
				return err
			}

			wrapped := grpcmiddleware.WrapServerStream(ss)
			wrapped.WrappedContext = ctx
			return handler(srv, wrapped)
		}

		return handler(srv, ss)
	}
}

func isRPCService(method string) bool {
	return strings.HasPrefix(method, "/api.Yorkie/")
}

// buildContext builds a context data for RPC. It includes the metadata of the
// request and the project information.
func (i *ContextInterceptor) buildContext(ctx context.Context) (context.Context, error) {
	// 01. building metadata
	md := metadata.Metadata{}
	data, ok := grpcmetadata.FromIncomingContext(ctx)
	if !ok {
		return nil, grpcstatus.Errorf(codes.Unauthenticated, "metadata is not provided")
	}

	apiKey := data["x-api-key"]
	if len(apiKey) == 0 && !i.backend.Config.UseDefaultProject {
		return nil, grpcstatus.Errorf(codes.Unauthenticated, "api key is not provided")
	}
	if len(apiKey) > 0 {
		md.APIKey = apiKey[0]
	}

	authorization := data["authorization"]
	if len(authorization) > 0 {
		md.Authorization = authorization[0]
	}
	ctx = metadata.With(ctx, md)

	// 02. building project
	// TODO(hackerwins): Improve the performance of this function.
	// Consider using a cache to store the info.
	project, err := projects.GetProjectFromAPIKey(ctx, i.backend, md.APIKey)
	if err != nil {
		return nil, grpchelper.ToStatusError(err)
	}
	ctx = projects.With(ctx, project)

	return ctx, nil
}
