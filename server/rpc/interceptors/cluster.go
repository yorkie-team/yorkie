/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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
	gotime "time"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
)

func isClusterService(method string) bool {
	return strings.HasPrefix(method, "/yorkie.v1.ClusterService/")
}

// ClusterServiceInterceptor is an interceptor for building additional context
// and handling metrics for server-to-server communication via ClusterService.
type ClusterServiceInterceptor struct {
	backend   *backend.Backend
	requestID *requestID
}

// NewClusterServiceInterceptor creates a new instance of ClusterServiceInterceptor.
func NewClusterServiceInterceptor(be *backend.Backend) *ClusterServiceInterceptor {
	return &ClusterServiceInterceptor{
		backend:   be,
		requestID: newRequestID("c"),
	}
}

// WrapUnary creates a unary server interceptor for building additional context
// and collecting metrics for server-to-server communication.
func (i *ClusterServiceInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		if !isClusterService(req.Spec().Procedure) {
			return next(ctx, req)
		}

		start := gotime.Now()
		ctx = i.buildContext(ctx)

		res, err := next(ctx, req)

		// Collect metrics for server-to-server communication
		if split := strings.Split(req.Spec().Procedure, "/"); len(split) == 3 {
			code := connecthelper.CodeOf(err)
			i.backend.Metrics.AddServerHandledCounter("unary", split[1], split[2], code, i.backend.Config.Hostname)
			i.backend.Metrics.ObserveServerHandledResponseSeconds(
				"unary",
				split[1],
				split[2],
				code,
				i.backend.Config.Hostname,
				gotime.Since(start).Seconds(),
			)
		}

		return res, err
	}
}

// WrapStreamingClient creates a stream client interceptor for server-to-server communication.
func (i *ClusterServiceInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		return next(ctx, spec)
	}
}

// WrapStreamingHandler creates a stream server interceptor for building additional context
// and collecting metrics for server-to-server communication.
func (i *ClusterServiceInterceptor) WrapStreamingHandler(
	next connect.StreamingHandlerFunc,
) connect.StreamingHandlerFunc {
	return func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		if !isClusterService(conn.Spec().Procedure) {
			return next(ctx, conn)
		}

		start := gotime.Now()
		ctx = i.buildContext(ctx)

		err := next(ctx, conn)

		// Collect metrics for server-to-server communication
		if split := strings.Split(conn.Spec().Procedure, "/"); len(split) == 3 {
			code := connecthelper.CodeOf(err)
			i.backend.Metrics.AddServerHandledCounter(
				"server_stream",
				split[1],
				split[2],
				code,
				i.backend.Config.Hostname,
			)
			i.backend.Metrics.ObserveServerHandledResponseSeconds(
				"server_stream",
				split[1],
				split[2],
				code,
				i.backend.Config.Hostname,
				gotime.Since(start).Seconds(),
			)
		}

		return err
	}
}

// buildContext builds a context data for server-to-server RPC.
func (i *ClusterServiceInterceptor) buildContext(ctx context.Context) context.Context {
	return logging.With(ctx, logging.New(i.requestID.next()))
}
