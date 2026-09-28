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
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	gotime "time"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
)

const clusterSecretHeader = "x-cluster-secret"

func isClusterService(method string) bool {
	return strings.HasPrefix(method, "/yorkie.v1.ClusterService/")
}

// ClusterServiceInterceptor is an interceptor for building additional context
// and handling metrics for server-to-server communication via ClusterService.
type ClusterServiceInterceptor struct {
	backend       *backend.Backend
	requestID     *requestID
	clusterSecret string
}

// NewClusterServiceInterceptor creates a new instance of ClusterServiceInterceptor.
func NewClusterServiceInterceptor(be *backend.Backend, clusterSecret string) *ClusterServiceInterceptor {
	return &ClusterServiceInterceptor{
		backend:       be,
		requestID:     newRequestID("c"),
		clusterSecret: clusterSecret,
	}
}

// authenticate validates the cluster secret from the request header.
//
// ClusterService shares the listener with YorkieService and AdminService, and
// its RPCs take the project straight out of the request message rather than
// going through the auth webhook, so an unconfigured secret must fail closed
// rather than wave every caller through. Callers construct the interceptor
// with backend.Config.EffectiveClusterSecret, which falls back to a random
// per-process secret rather than to SecretKey, so the empty case here is a
// defensive guard rather than a reachable state.
//
// Peers must send the header: a node still running a build that was configured
// without any cluster secret sends none and is rejected, so a rolling upgrade
// has to set the shared secret on the old binaries first. A missing header is
// reported separately from a wrong one because it is the signature of exactly
// that half-upgraded cluster. See docs/design/cluster-service-auth.md.
func (i *ClusterServiceInterceptor) authenticate(header http.Header) error {
	if i.clusterSecret == "" {
		return connect.NewError(connect.CodeUnauthenticated,
			errors.New("cluster secret is not configured"))
	}

	secret := header.Get(clusterSecretHeader)
	if secret == "" {
		return connect.NewError(connect.CodeUnauthenticated, errors.New(
			"missing cluster secret header: the caller was started without "+
				"--cluster-secret, see docs/design/cluster-service-auth.md",
		))
	}
	if subtle.ConstantTimeCompare([]byte(secret), []byte(i.clusterSecret)) != 1 {
		return connect.NewError(connect.CodeUnauthenticated,
			errors.New("invalid cluster secret"))
	}

	return nil
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

		if err := i.authenticate(req.Header()); err != nil {
			return nil, err
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

		if err := i.authenticate(conn.RequestHeader()); err != nil {
			return err
		}

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
		}

		return err
	}
}

// buildContext builds a context data for server-to-server RPC.
func (i *ClusterServiceInterceptor) buildContext(ctx context.Context) context.Context {
	return logging.With(ctx, logging.NewNamed(i.requestID.next()))
}
