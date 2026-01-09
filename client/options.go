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
	gotime "time"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/pkg/key"
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

	// SyncLoopDuration is the duration of the sync loop.
	// After each sync loop, the client waits for the duration to next sync.
	// The default value is 50ms (same as JS SDK).
	SyncLoopDuration gotime.Duration

	// RetrySyncLoopDelay is the delay of the retry sync loop.
	// If the sync loop fails, the client waits for the delay to retry the sync loop.
	// The default value is 1000ms.
	RetrySyncLoopDelay gotime.Duration

	// ChannelHeartbeatInterval is the interval of the presence heartbeat.
	// The client sends a heartbeat to the server to refresh the presence TTL.
	// The default value is 30 seconds.
	ChannelHeartbeatInterval gotime.Duration
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

// WithSyncLoopDuration configures the duration of the sync loop.
func WithSyncLoopDuration(duration gotime.Duration) Option {
	return func(o *Options) { o.SyncLoopDuration = duration }
}

// WithRetrySyncLoopDelay configures the delay of the retry sync loop.
func WithRetrySyncLoopDelay(delay gotime.Duration) Option {
	return func(o *Options) { o.RetrySyncLoopDelay = delay }
}

// WithChannelHeartbeatInterval configures the interval of the channel heartbeat.
func WithChannelHeartbeatInterval(interval gotime.Duration) Option {
	return func(o *Options) { o.ChannelHeartbeatInterval = interval }
}

// DeactivateOption configures DeactivateOptions.
type DeactivateOption func(*DeactivateOptions)

// DeactivateOptions configures how we set up the document.
type DeactivateOptions struct {
	Asynchronous bool
}

// WithAsynchronous configures the asynchronous option of the document.
func WithAsynchronous() DeactivateOption {
	return func(o *DeactivateOptions) { o.Asynchronous = true }
}

// AttachOption configures AttachOptions.
type AttachOption func(*AttachOptions)

// AttachOptions configures how we set up the document.
type AttachOptions struct {
	IsRealtime  bool
	Presence    presence.Data
	InitialRoot yson.Object
	Schema      string
}

// WithRealtimeSync configures the manual sync of the client.
func WithRealtimeSync() AttachOption {
	return func(o *AttachOptions) { o.IsRealtime = true }
}

// WithPresence configures the presence of the client.
func WithPresence(presence presence.Data) AttachOption {
	return func(o *AttachOptions) { o.Presence = presence }
}

// WithInitialRoot sets the initial root of the document. Values in the initial
// root will be discarded if the key already exists in the document. If some
// keys are not in the document, they will be added.
func WithInitialRoot(root yson.Object) AttachOption {
	return func(o *AttachOptions) {
		o.InitialRoot = root
	}
}

// WithSchema configures the schema of the document.
func WithSchema(schema string) AttachOption {
	return func(o *AttachOptions) { o.Schema = schema }
}

// AttachChannelOption configures AttachChannelOptions.
type AttachChannelOption func(*AttachChannelOptions)

// AttachChannelOptions configures how we set up the channel.
type AttachChannelOptions struct {
	// IsRealtime determines whether the channel is in realtime mode.
	// If true, the client will watch for channel changes via streaming.
	// If false, the client must manually refresh to get count updates.
	IsRealtime bool
}

// WithChannelRealtimeSync configures the channel to be in realtime mode.
func WithChannelRealtimeSync() AttachChannelOption {
	return func(o *AttachChannelOptions) { o.IsRealtime = true }
}

// DetachOption configures DetachOptions.
type DetachOption func(*DetachOptions)

// DetachOptions configures how we set up the document.
type DetachOptions struct {
}

// SyncOptions is an option for sync. It contains the key of the resource to
// sync and the sync mode. It can be used for both documents and channels.
type SyncOptions struct {
	key  key.Key
	mode types.SyncMode
}

// WithKey creates a SyncOptions with the given resource key.
func WithKey(k key.Key) SyncOptions {
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
