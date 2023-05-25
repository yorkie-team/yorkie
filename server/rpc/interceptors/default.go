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

	"google.golang.org/grpc"

	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/rpc/grpchelper"
)

// DefaultInterceptor is a interceptor for default.
type DefaultInterceptor struct{}

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
		reqLogger := logging.From(ctx)
		if err != nil {
			reqLogger.Warnf("RPC : %q %s: %q => %q", info.FullMethod, gotime.Since(start), req, err)
			return nil, grpchelper.ToStatusError(err)
		}

		if gotime.Since(start) > 100*gotime.Millisecond {
			reqLogger.Infof("RPC : %q %s", info.FullMethod, gotime.Since(start))
		}
		return resp, nil
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
		reqLogger := logging.From(ss.Context())

		start := gotime.Now()
		err := handler(srv, ss)
		if err != nil {
			reqLogger.Warnf("RPC : stream %q %s => %q", info.FullMethod, gotime.Since(start), err.Error())
			return grpchelper.ToStatusError(err)
		}

		reqLogger.Infof("RPC : stream %q %s", info.FullMethod, gotime.Since(start))
		return nil
	}
}
