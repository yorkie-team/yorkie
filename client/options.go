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

package client

import (
	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/pkg/types"
)

// Option configures Options.
type Option func(*Options)

// Options configures how we set up the client.
type Options struct {
	// Key is the key of the client. It is used to identify the client.
	Key string

	// Metadata is the metadata of the client.
	Metadata types.Metadata

	// Token is the token of the client. Each request will be authenticated with this token.
	Token string

	// CertFile is the path to the certificate file.
	CertFile string

	// ServerNameOverride is the server name override.
	ServerNameOverride string

	// Logger is the Logger of the client.
	Logger *zap.Logger
}

// WithKey configures the key of the client.
func WithKey(key string) Option {
	return func(o *Options) { o.Key = key }
}

// WithMetadata configures the metadata of the client.
func WithMetadata(metadata types.Metadata) Option {
	return func(o *Options) { o.Metadata = metadata }
}

// WithToken configures the token of the client.
func WithToken(token string) Option {
	return func(o *Options) { o.Token = token }
}

// WithCertFile configures the certificate file of the client.
func WithCertFile(certFile string) Option {
	return func(o *Options) { o.CertFile = certFile }
}

// WithServerNameOverride configures the server name override of the client.
func WithServerNameOverride(serverNameOverride string) Option {
	return func(o *Options) { o.ServerNameOverride = serverNameOverride }
}

// WithLogger configures the Logger of the client.
func WithLogger(logger *zap.Logger) Option {
	return func(o *Options) { o.Logger = logger }
}
