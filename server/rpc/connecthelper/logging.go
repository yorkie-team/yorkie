/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

// Package connecthelper provides helper functions for connectRPC.
package connecthelper

import (
	"context"
	"strconv"
	"sync/atomic"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/server/logging"
)

type reqID int32

func (c *reqID) next() string {
	next := atomic.AddInt32((*int32)(c), 1)
	return "r" + strconv.Itoa(int(next))
}

// LoggingInterceptor is an interceptor for request logging.
type LoggingInterceptor struct {
	reqID reqID
}

// NewLoggingInterceptor creates a new instance of LoggingInterceptor.
func NewLoggingInterceptor() *LoggingInterceptor {
	return &LoggingInterceptor{}
}

// WrapUnary creates a unary server interceptor for request logging.
func (i *LoggingInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		reqLogger := logging.New(i.reqID.next())
		return next(logging.With(ctx, reqLogger), req)
	}
}

// WrapStreamingClient creates a stream client interceptor for request logging.
func (i *LoggingInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		return next(ctx, spec)
	}
}

// WrapStreamingHandler creates a stream server interceptor for request logging.
func (i *LoggingInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		reqLogger := logging.New(i.reqID.next())
		return next(logging.With(ctx, reqLogger), conn)
	}
}
