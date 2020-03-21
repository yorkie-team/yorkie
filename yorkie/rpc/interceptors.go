/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package rpc

import (
	"context"
	"time"

	"google.golang.org/grpc"

	"github.com/yorkie-team/yorkie/pkg/log"
)

func unaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	if err == nil {
		log.Logger.Infof("RPC : %q %s", info.FullMethod, time.Since(start))
	} else {
		log.Logger.Errorf("RPC : %q %s: %q => %q", info.FullMethod, time.Since(start), req, err)
	}

	return resp, err
}

func streamInterceptor(
	srv interface{},
	ss grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	err := handler(srv, ss)
	if err == nil {
		log.Logger.Infof("stream %q => ok", info.FullMethod)
	} else {
		log.Logger.Infof(
			"stream %q => %s", info.FullMethod, err.Error(),
		)
	}

	return err
}
