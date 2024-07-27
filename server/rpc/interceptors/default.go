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
	"errors"
	gotime "time"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
)

// DefaultInterceptor is an interceptor for common RPC.
type DefaultInterceptor struct{}

// NewDefaultInterceptor creates a new instance of DefaultInterceptor.
func NewDefaultInterceptor() *DefaultInterceptor {
	return &DefaultInterceptor{}
}

const (
	// SlowThreshold is the threshold for slow RPC.
	SlowThreshold = 100 * gotime.Millisecond
)

// WrapUnary creates a unary server interceptor for default.
func (i *DefaultInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		start := gotime.Now()
		resp, err := next(ctx, req)
		reqLogger := logging.From(ctx)
		if err != nil {
			reqLogger.Warnf("RPC : %q %s: %q => %q", req.Spec().Procedure, gotime.Since(start), req, err)
			return nil, connecthelper.ToStatusError(err)
		}

		if gotime.Since(start) > SlowThreshold {
			reqLogger.Infof("RPC : %q %s", req.Spec().Procedure, gotime.Since(start))
		}
		return resp, nil
	}
}

// WrapStreamingClient creates a stream client interceptor for default.
func (i *DefaultInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		return next(ctx, spec)
	}
}

// WrapStreamingHandler creates a stream server interceptor for default.
func (i *DefaultInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		reqLogger := logging.From(ctx)

		start := gotime.Now()
		err := next(ctx, conn)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				reqLogger.Debugf("RPC : stream %q %s => %q", conn.Spec().Procedure, gotime.Since(start), err.Error())
				return connecthelper.ToStatusError(err)
			}

			reqLogger.Warnf("RPC : stream %q %s => %q", conn.Spec().Procedure, gotime.Since(start), err.Error())
			return connecthelper.ToStatusError(err)
		}

		reqLogger.Debugf("RPC : stream %q %s", conn.Spec().Procedure, gotime.Since(start))
		return nil
	}
}
