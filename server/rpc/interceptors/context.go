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

// Package interceptors provides the interceptors for RPC.
package interceptors

import (
	"context"
	"strings"

	grpcmiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcmetadata "google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/grpchelper"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
)

// ContextInterceptor is an interceptor for building additional context.
type ContextInterceptor struct {
	backend          *backend.Backend
	projectInfoCache *cache.LRUExpireCache[string, *types.Project]
}

// NewContextInterceptor creates a new instance of ContextInterceptor.
func NewContextInterceptor(be *backend.Backend) *ContextInterceptor {
	projectInfoCache, err := cache.NewLRUExpireCache[string, *types.Project](be.Config.ProjectInfoCacheSize)
	if err != nil {
		logging.DefaultLogger().Fatal("Failed to create project info cache: %v", err)
	}
	return &ContextInterceptor{
		backend:          be,
		projectInfoCache: projectInfoCache,
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
		if !isYorkieService(info.FullMethod) {
			return handler(ctx, req)
		}

		ctx, err = i.buildContext(ctx)
		if err != nil {
			return nil, err
		}

		resp, err = handler(ctx, req)

		data, ok := grpcmetadata.FromIncomingContext(ctx)
		if ok {
			sdkType, sdkVersion := grpchelper.SDKTypeAndVersion(data)
			i.backend.Metrics.AddUserAgent(
				i.backend.Config.Hostname,
				projects.From(ctx),
				sdkType,
				sdkVersion,
				info.FullMethod,
			)
		}

		return resp, err
	}
}

// Stream creates a stream server interceptor for building additional context.
func (i *ContextInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		if !isYorkieService(info.FullMethod) {
			return handler(srv, ss)
		}

		ctx, err := i.buildContext(ss.Context())
		if err != nil {
			return err
		}

		wrapped := grpcmiddleware.WrapServerStream(ss)
		wrapped.WrappedContext = ctx

		err = handler(srv, wrapped)

		data, ok := grpcmetadata.FromIncomingContext(ctx)
		if ok {
			sdkType, sdkVersion := grpchelper.SDKTypeAndVersion(data)
			i.backend.Metrics.AddUserAgent(
				i.backend.Config.Hostname,
				projects.From(ctx),
				sdkType,
				sdkVersion,
				info.FullMethod,
			)
		}

		return err
	}
}

func isYorkieService(method string) bool {
	return strings.HasPrefix(method, "/yorkie.v1.YorkieService/")
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

	apiKey := data[types.APIKeyKey]
	if len(apiKey) == 0 && !i.backend.Config.UseDefaultProject {
		return nil, grpcstatus.Errorf(codes.Unauthenticated, "api key is not provided")
	}
	if len(apiKey) > 0 {
		md.APIKey = apiKey[0]
	}

	authorization := data[types.AuthorizationKey]
	if len(authorization) > 0 {
		md.Authorization = authorization[0]
	}
	ctx = metadata.With(ctx, md)
	cacheKey := md.APIKey

	// 02. building project
	if cachedProjectInfo, ok := i.projectInfoCache.Get(cacheKey); ok {
		ctx = projects.With(ctx, cachedProjectInfo)
	} else {
		project, err := projects.GetProjectFromAPIKey(ctx, i.backend, md.APIKey)
		if err != nil {
			return nil, grpchelper.ToStatusError(err)
		}
		i.projectInfoCache.Add(cacheKey, project, i.backend.Config.ParseProjectInfoCacheTTL())
		ctx = projects.With(ctx, project)
	}

	return ctx, nil
}
