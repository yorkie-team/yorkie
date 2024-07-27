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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/key"
)

// Option configures Options.
type Option func(*Options)

// Options configures how we set up the client.
type Options struct {
	// Key is the key of the client. It is used to identify the client.
	Key string

	// APIKey is the API key of the client.
	APIKey string

	// Token is the token of the client. Each request will be authenticated with this token.
	Token string

	// CertFile is the path to the certificate file.
	CertFile string

	// ServerNameOverride is the server name override.
	ServerNameOverride string

	// Logger is the Logger of the client.
	Logger *zap.Logger

	// MaxCallRecvMsgSize is the maximum message size in bytes the client can receive.
	MaxCallRecvMsgSize int
}

// WithKey configures the key of the client.
func WithKey(key string) Option {
	return func(o *Options) { o.Key = key }
}

// WithAPIKey configures the API key of the client.
func WithAPIKey(apiKey string) Option {
	return func(o *Options) { o.APIKey = apiKey }
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

// WithMaxRecvMsgSize configures the maximum message size in bytes the client can receive.
func WithMaxRecvMsgSize(maxRecvMsgSize int) Option {
	return func(o *Options) { o.MaxCallRecvMsgSize = maxRecvMsgSize }
}

// AttachOption configures AttachOptions.
type AttachOption func(*AttachOptions)

// AttachOptions configures how we set up the document.
type AttachOptions struct {
	// Presence is the presence of the client.
	Presence innerpresence.Presence
	IsManual bool
}

// WithPresence configures the presence of the client.
func WithPresence(presence innerpresence.Presence) AttachOption {
	return func(o *AttachOptions) { o.Presence = presence }
}

// WithManualSync configures the manual sync of the client.
func WithManualSync() AttachOption {
	return func(o *AttachOptions) { o.IsManual = true }
}

// DetachOption configures DetachOptions.
type DetachOption func(*DetachOptions)

// DetachOptions configures how we set up the document.
type DetachOptions struct {
	removeIfNotAttached bool
}

// WithRemoveIfNotAttached configures the removeIfNotAttached of the document.
func WithRemoveIfNotAttached() DetachOption {
	return func(o *DetachOptions) { o.removeIfNotAttached = true }
}

// SyncOptions is an option for sync. It contains the key of the document to
// sync and the sync mode.
type SyncOptions struct {
	key  key.Key
	mode types.SyncMode
}

// WithDocKey creates a SyncOptions with the given document key.
func WithDocKey(k key.Key) SyncOptions {
	return SyncOptions{
		key:  k,
		mode: types.SyncModePushPull,
	}
}

// WithPushOnly returns a SyncOptions with the sync mode set to PushOnly.
func (o SyncOptions) WithPushOnly() SyncOptions {
	return SyncOptions{
		key:  o.key,
		mode: types.SyncModePushOnly,
	}
}
