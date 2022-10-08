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

// Package grpchelper provides helper functions for gRPC.
package grpchelper

import (
	"context"
	"strconv"
	"sync/atomic"

	grpcmiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"google.golang.org/grpc"

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

// Unary creates a unary server interceptor for request logging.
func (i *LoggingInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp interface{}, err error) {
		reqLogger := logging.New(i.reqID.next())
		return handler(logging.With(ctx, reqLogger), req)
	}
}

// Stream creates a stream server interceptor for request logging.
func (i *LoggingInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		reqLogger := logging.New(i.reqID.next())
		wrapped := grpcmiddleware.WrapServerStream(ss)
		wrapped.WrappedContext = logging.With(ss.Context(), reqLogger)
		return handler(srv, wrapped)
	}
}
