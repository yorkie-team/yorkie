/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
)

func isSystemService(method string) bool {
	return strings.HasPrefix(method, "/yorkie.v1.SystemService/")
}

// SystemServiceInterceptor is an interceptor for building additional context
// and handling authentication for SystemService.
type SystemServiceInterceptor struct {
	backend          *backend.Backend
	requestID        *requestID
	projectInfoCache *cache.LRUExpireCache[string, *types.Project]
}

// NewSystemServiceInterceptor creates a new instance of SystemServiceInterceptor.
func NewSystemServiceInterceptor(be *backend.Backend) *SystemServiceInterceptor {
	projectInfoCache, err := cache.NewLRUExpireCache[string, *types.Project](be.Config.ProjectInfoCacheSize)
	if err != nil {
		logging.DefaultLogger().Fatal("Failed to create project info cache: %v", err)
	}
	return &SystemServiceInterceptor{
		backend:          be,
		requestID:        newRequestID("r"),
		projectInfoCache: projectInfoCache,
	}
}

// WrapUnary creates a unary server interceptor for building additional context.
func (i *SystemServiceInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		if !isSystemService(req.Spec().Procedure) {
			return next(ctx, req)
		}

		ctx, err := i.buildContext(ctx, req.Header())
		if err != nil {
			return nil, err
		}

		res, err := next(ctx, req)

		sdkType, sdkVersion := connecthelper.SDKTypeAndVersion(req.Header())
		i.backend.Metrics.AddUserAgent(
			i.backend.Config.Hostname,
			projects.From(ctx),
			sdkType,
			sdkVersion,
			req.Spec().Procedure,
		)

		if split := strings.Split(req.Spec().Procedure, "/"); len(split) == 3 {
			i.backend.Metrics.AddServerHandledCounter(
				"unary",
				split[1],
				split[2],
				connecthelper.ToRPCCodeString(err),
			)
		}

		return res, err
	}
}

// WrapStreamingClient creates a stream client interceptor for building additional context.
func (i *SystemServiceInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		return next(ctx, spec)
	}
}

// WrapStreamingHandler creates a stream server interceptor for building additional context.
func (i *SystemServiceInterceptor) WrapStreamingHandler(
	next connect.StreamingHandlerFunc,
) connect.StreamingHandlerFunc {
	return func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		if !isSystemService(conn.Spec().Procedure) {
			return next(ctx, conn)
		}

		ctx, err := i.buildContext(ctx, conn.RequestHeader())
		if err != nil {
			return err
		}

		err = next(ctx, conn)

		sdkType, sdkVersion := connecthelper.SDKTypeAndVersion(conn.RequestHeader())
		i.backend.Metrics.AddUserAgent(
			i.backend.Config.Hostname,
			projects.From(ctx),
			sdkType,
			sdkVersion,
			conn.Spec().Procedure,
		)

		if split := strings.Split(conn.Spec().Procedure, "/"); len(split) == 3 {
			i.backend.Metrics.AddServerHandledCounter(
				"server_stream",
				split[1],
				split[2],
				connecthelper.ToRPCCodeString(err),
			)
		}

		return err
	}
}

// buildContext builds a context data for RPC. It includes the metadata of the
// request and the project information.
func (i *SystemServiceInterceptor) buildContext(ctx context.Context, header http.Header) (context.Context, error) {
	// 01. building metadata
	md := metadata.Metadata{}
	// TODO(raararaara): server token injection required
	//md.Authorization = os.Getenv("YORKIE_SERVER_TOKEN")

	apiKey := header.Get(types.APIKeyKey)
	if apiKey == "" && !i.backend.Config.UseDefaultProject {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("api key is not provided"))
	}
	if apiKey != "" {
		md.APIKey = apiKey
	}

	if authorization := header.Get(types.AuthorizationKey); authorization != "" {
		md.Authorization = authorization
	}
	ctx = metadata.With(ctx, md)
	cacheKey := md.APIKey

	// 02. building project
	if _, ok := i.projectInfoCache.Get(cacheKey); !ok {
		prj, err := projects.GetProjectFromAPIKey(ctx, i.backend, md.APIKey)
		if err != nil {
			return nil, connecthelper.ToStatusError(err)
		}
		i.projectInfoCache.Add(cacheKey, prj, i.backend.Config.ParseProjectInfoCacheTTL())
	}
	project, _ := i.projectInfoCache.Get(cacheKey)
	ctx = projects.With(ctx, project)

	// 03. building logger
	ctx = logging.With(ctx, logging.New(i.requestID.next(), logging.NewField("prj", project.Name)))

	return ctx, nil
}
