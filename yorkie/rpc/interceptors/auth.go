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
	"bytes"
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/pkg/types"
)

// AuthInterceptor is a interceptor for authentication.
type AuthInterceptor struct {
	webhook string
}

// NewAuthInterceptor creates a new instance of AuthInterceptor.
func NewAuthInterceptor(webhook string) *AuthInterceptor {
	return &AuthInterceptor{
		webhook: webhook,
	}
}

// Unary creates a unary server interceptor for authorization.
func (i *AuthInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp interface{}, err error) {
		if i.needAuth() {
			if err := i.authorize(ctx); err != nil {
				return nil, err
			}
		}

		return handler(ctx, req)
	}
}

// Stream creates a stream server interceptor for authorization.
func (i *AuthInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if i.needAuth() {
			if err := i.authorize(ss.Context()); err != nil {
				return err
			}
		}

		return handler(srv, ss)
	}
}

func (i *AuthInterceptor) needAuth() bool {
	return len(i.webhook) > 0
}

func (i *AuthInterceptor) extractToken(ctx context.Context) (string, error) {
	data, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Errorf(codes.Unauthenticated, "metadata is not provided")
	}

	values := data["authorization"]
	if len(values) == 0 {
		return "", status.Errorf(codes.Unauthenticated, "authorization token is not provided")
	}

	return values[0], nil
}

func (i *AuthInterceptor) authorize(ctx context.Context) error {
	token, err := i.extractToken(ctx)
	if err != nil {
		return err
	}

	// TODO(hackerwins): We need to extract docKeys and verbs from RPC.
	reqBody, err := json.Marshal(types.AuthWebhookRequest{
		Token: token,
	})
	if err != nil {
		return err
	}

	// TODO(hackerwins): We need to apply retryBackoff in case of failure
	resp, err := http.Post(i.webhook, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Logger.Error(err)
		}
	}()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var authResp types.AuthWebhookResponse
	if err = json.Unmarshal(respBody, &authResp); err != nil {
		return err
	}

	if !authResp.Allowed {
		return status.Errorf(codes.Unauthenticated, authResp.Reason)
	}

	return nil
}
