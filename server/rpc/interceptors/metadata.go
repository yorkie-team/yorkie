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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
)

// MetadataInterceptor is an interceptor for extracting metadata from gRPC context.
type MetadataInterceptor struct {
	backend *backend.Backend
}

// NewMetadataInterceptor creates a new instance of MetadataInterceptor.
func NewMetadataInterceptor(be *backend.Backend) *MetadataInterceptor {
	return &MetadataInterceptor{
		backend: be,
	}
}

// Unary creates a unary server interceptor for authorization.
func (i *MetadataInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp interface{}, err error) {
		if isRPCService(info.FullMethod) {
			md, err := i.extractMetadata(ctx)
			if err != nil {
				return nil, err
			}
			return handler(auth.CtxWithMetadata(ctx, md), req)
		}

		return handler(ctx, req)
	}
}

// Stream creates a stream server interceptor for authorization.
func (i *MetadataInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if isRPCService(info.FullMethod) {
			md, err := i.extractMetadata(ss.Context())
			if err != nil {
				return err
			}
			wrapped := grpcmiddleware.WrapServerStream(ss)
			wrapped.WrappedContext = auth.CtxWithMetadata(ss.Context(), md)
			return handler(srv, wrapped)
		}

		return handler(srv, ss)
	}
}

func isRPCService(method string) bool {
	return strings.HasPrefix(method, "/api.Yorkie/")
}

func (i *MetadataInterceptor) extractMetadata(ctx context.Context) (auth.Metadata, error) {
	md := auth.Metadata{}
	data, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return md, status.Errorf(codes.Unauthenticated, "metadata is not provided")
	}

	apiKey := data["x-api-key"]
	if len(apiKey) == 0 && !i.backend.Config.UseDefaultProject {
		return md, status.Errorf(codes.Unauthenticated, "api key is not provided")
	}

	if len(apiKey) > 0 {
		md.APIKey = apiKey[0]
	}

	authorization := data["authorization"]
	if len(authorization) > 0 {
		md.Authorization = authorization[0]
	}

	return md, nil
}
