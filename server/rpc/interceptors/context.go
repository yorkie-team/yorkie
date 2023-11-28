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
	"connectrpc.com/connect"
	"context"
	"errors"
	"net/http"
	"strings"

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

// WrapUnary creates a unary server interceptor for building additional context.
func (i *ContextInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		if !isYorkieService(req.Spec().Procedure) {
			return next(ctx, req)
		}

		ctx, err := i.buildContext(ctx, req.Header())
		if err != nil {
			return nil, err
		}

		//data, ok := grpcmetadata.FromIncomingContext(ctx)
		//if ok {
		//	sdkType, sdkVersion := grpchelper.SDKTypeAndVersion(data)
		//	i.backend.Metrics.AddUserAgent(
		//		i.backend.Config.Hostname,
		//		projects.From(ctx),
		//		sdkType,
		//		sdkVersion,
		//		info.FullMethod,
		//	)
		//}

		return next(ctx, req)
	}
}

// WrapStreamingClient creates a stream client interceptor for building additional context.
func (i *ContextInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		conn := next(ctx, spec)
		return conn
	}
}

// WrapStreamingHandler creates a stream server interceptor for building additional context.
func (i *ContextInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		if !isYorkieService(conn.Spec().Procedure) {
			return next(ctx, conn)
		}

		ctx, err := i.buildContext(ctx, conn.RequestHeader())
		if err != nil {
			return err
		}

		//data, ok := grpcmetadata.FromIncomingContext(ctx)
		//if ok {
		//	sdkType, sdkVersion := grpchelper.SDKTypeAndVersion(data)
		//	i.backend.Metrics.AddUserAgent(
		//		i.backend.Config.Hostname,
		//		projects.From(ctx),
		//		sdkType,
		//		sdkVersion,
		//		info.FullMethod,
		//	)
		//}

		return next(ctx, conn)
	}
}

func isYorkieService(method string) bool {
	return strings.HasPrefix(method, "/yorkie.v1.YorkieService/")
}

// buildContext builds a context data for RPC. It includes the metadata of the
// request and the project information.
func (i *ContextInterceptor) buildContext(ctx context.Context, header http.Header) (context.Context, error) {
	// 01. building metadata
	md := metadata.Metadata{}

	apiKey := header.Get(types.APIKeyKey)
	if len(apiKey) == 0 && !i.backend.Config.UseDefaultProject {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("api key is not provided"))
	}
	if len(apiKey) > 0 {
		md.APIKey = apiKey
	}

	authorization := header.Get(types.AuthorizationKey)
	if len(authorization) > 0 {
		md.Authorization = authorization
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
