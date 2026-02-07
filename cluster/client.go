/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

// Package cluster is a package for the cluster service for communication between
// nodes in the Yorkie cluster.
package cluster

import (
	"context"
	"crypto/tls"
	goerrors "errors"
	"net"
	"net/http"
	"strings"
	gotime "time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// Option configures Options.
type Option func(*Options)

// WithSecure configures secure option of the client.
func WithSecure(isSecure bool) Option {
	return func(o *Options) { o.IsSecure = isSecure }
}

// WithRPCTimeout configures the timeout for individual cluster RPC calls.
func WithRPCTimeout(timeout gotime.Duration) Option {
	return func(o *Options) { o.RPCTimeout = timeout }
}

// WithClientTimeout configures the HTTP client timeout for the entire
// request lifecycle (DNS + connect + TLS + request + response).
func WithClientTimeout(timeout gotime.Duration) Option {
	return func(o *Options) { o.ClientTimeout = timeout }
}

// WithPoolSize configures the number of connections per host in the pool.
func WithPoolSize(size int) Option {
	return func(o *Options) { o.PoolSize = size }
}

// Options configures how we set up the client.
type Options struct {
	// IsSecure is whether to enable the TLS connection of the client.
	IsSecure bool

	// RPCTimeout is the timeout for individual cluster RPC calls.
	// If zero, defaults to defaultRPCTimeout.
	RPCTimeout gotime.Duration

	// ClientTimeout is the hard limit for the entire HTTP request lifecycle.
	// If zero, defaults to defaultClientTimeout.
	ClientTimeout gotime.Duration

	// PoolSize is the number of connections per host in the pool.
	// If zero, defaults to 1.
	PoolSize int
}

const (
	// defaultRPCTimeout is the fallback timeout when no RPCTimeout is configured.
	defaultRPCTimeout = 10 * gotime.Second

	// defaultClientTimeout is the fallback HTTP client timeout.
	defaultClientTimeout = 30 * gotime.Second
)

// Client is a client for admin service.
type Client struct {
	conn       *http.Client
	client     v1connect.ClusterServiceClient
	isSecure   bool
	rpcTimeout gotime.Duration
}

// New creates an instance of Client.
func New(opts ...Option) (*Client, error) {
	var options Options
	for _, opt := range opts {
		opt(&options)
	}

	rpcTimeout := options.RPCTimeout
	if rpcTimeout == 0 {
		rpcTimeout = defaultRPCTimeout
	}

	clientTimeout := options.ClientTimeout
	if clientTimeout == 0 {
		clientTimeout = defaultClientTimeout
	}

	conn := &http.Client{
		// Timeout is a hard limit for the entire request lifecycle, acting as
		// a safety net in case per-RPC context timeouts are not set properly.
		Timeout: clientTimeout,
	}
	if options.IsSecure {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		conn.Transport = &http2.Transport{
			TLSClientConfig: tlsConfig,
		}
	} else {
		// NOTE(hackerwins): Use h2c (HTTP/2 without TLS) for cluster communication
		// This enables multiplexing and header compression for better performance
		conn.Transport = &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
			ReadIdleTimeout: 30 * gotime.Second,
			PingTimeout:     15 * gotime.Second,
		}
	}

	return &Client{
		conn:       conn,
		isSecure:   options.IsSecure,
		rpcTimeout: rpcTimeout,
	}, nil
}

// Dial creates an instance of Client and dials to the admin service.
func Dial(rpcAddr string, opts ...Option) (*Client, error) {
	cli, err := New(opts...)
	if err != nil {
		return nil, err
	}

	if err := cli.Dial(rpcAddr); err != nil {
		return nil, err
	}

	return cli, nil
}

// Dial dials to the admin service.
func (c *Client) Dial(rpcAddr string) error {
	if !strings.Contains(rpcAddr, "://") {
		if c.isSecure {
			rpcAddr = "https://" + rpcAddr
		} else {
			rpcAddr = "http://" + rpcAddr
		}
	}

	c.client = v1connect.NewClusterServiceClient(c.conn, rpcAddr)

	return nil
}

// Close closes the connection to the admin service.
func (c *Client) Close() {
	c.conn.CloseIdleConnections()
}

// DetachDocument detaches the given document from the client.
func (c *Client) DetachDocument(
	ctx context.Context,
	project *types.Project,
	clientID time.ActorID,
	docID types.ID,
	docKey key.Key,
) error {
	ctx, cancel := context.WithTimeout(ctx, c.rpcTimeout)
	defer cancel()

	_, err := c.client.DetachDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.ClusterServiceDetachDocumentRequest{
			Project:     converter.ToProject(project),
			ClientId:    clientID.String(),
			DocumentId:  docID.String(),
			DocumentKey: docKey.String(),
		}), project.PublicKey, docKey.String()),
	)
	if err != nil {
		return fromConnectError(err)
	}

	return nil
}

// CompactDocument compacts the given document.
func (c *Client) CompactDocument(
	ctx context.Context,
	project *types.Project,
	docInfo *database.DocInfo,
) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.rpcTimeout)
	defer cancel()

	res, err := c.client.CompactDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.ClusterServiceCompactDocumentRequest{
			ProjectId:   docInfo.ProjectID.String(),
			DocumentId:  docInfo.ID.String(),
			DocumentKey: docInfo.Key.String(),
		}), project.PublicKey, docInfo.Key.String()))
	if err != nil {
		return false, fromConnectError(err)
	}

	return res.Msg.Compacted, nil
}

// PurgeDocument purges the given document.
func (c *Client) PurgeDocument(
	ctx context.Context,
	project *types.Project,
	docInfo *database.DocInfo,
) error {
	ctx, cancel := context.WithTimeout(ctx, c.rpcTimeout)
	defer cancel()

	_, err := c.client.PurgeDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.ClusterServicePurgeDocumentRequest{
			ProjectId:   docInfo.ProjectID.String(),
			DocumentId:  docInfo.ID.String(),
			DocumentKey: docInfo.Key.String(),
		}), project.PublicKey, docInfo.Key.String()))
	if err != nil {
		return fromConnectError(err)
	}

	return nil
}

// GetDocument gets the document for a single document.
func (c *Client) GetDocument(
	ctx context.Context,
	project *types.Project,
	documentKey string,
	includeRoot bool,
	includePresences bool,
) (*types.DocumentSummary, error) {
	ctx, cancel := context.WithTimeout(ctx, c.rpcTimeout)
	defer cancel()

	response, err := c.client.GetDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.ClusterServiceGetDocumentRequest{
			Project:          converter.ToProject(project),
			DocumentKey:      documentKey,
			IncludeRoot:      includeRoot,
			IncludePresences: includePresences,
		}), project.PublicKey, documentKey),
	)
	if err != nil {
		return nil, fromConnectError(err)
	}

	return converter.FromDocumentSummary(response.Msg.Document), nil
}

// ListChannels lists channels for the given project.
// If query is not empty, it filters channels by the query prefix.
func (c *Client) ListChannels(
	ctx context.Context,
	projectID types.ID,
	query string,
	limit int32,
) ([]*types.ChannelSummary, error) {
	ctx, cancel := context.WithTimeout(ctx, c.rpcTimeout)
	defer cancel()

	response, err := c.client.ListChannels(
		ctx,
		connect.NewRequest(&api.ClusterServiceListChannelsRequest{
			ProjectId: projectID.String(),
			Query:     query,
			Limit:     limit,
		}),
	)
	if err != nil {
		return nil, fromConnectError(err)
	}

	var channels []*types.ChannelSummary
	for _, ch := range response.Msg.Channels {
		channels = append(channels, converter.FromChannelSummary(ch))
	}

	return channels, nil
}

// GetChannels gets multiple channels in a single RPC call.
// All channels should have the same first key path for istio consistent hash sharding.
func (c *Client) GetChannels(
	ctx context.Context,
	project *types.Project,
	channelKeys []key.Key,
	firstKeyPath string,
	includeSubPath bool,
) ([]*types.ChannelSummary, error) {
	ctx, cancel := context.WithTimeout(ctx, c.rpcTimeout)
	defer cancel()

	keyStrings := make([]string, len(channelKeys))
	for i, k := range channelKeys {
		keyStrings[i] = k.String()
	}

	response, err := c.client.GetChannels(
		ctx,
		withShardKey(connect.NewRequest(&api.ClusterServiceGetChannelsRequest{
			ProjectId:      project.ID.String(),
			ChannelKeys:    keyStrings,
			IncludeSubPath: includeSubPath,
		}), project.PublicKey, firstKeyPath),
	)
	if err != nil {
		return nil, fromConnectError(err)
	}

	var channels []*types.ChannelSummary
	for _, ch := range response.Msg.Channels {
		channels = append(channels, converter.FromChannelSummary(ch))
	}

	return channels, nil
}

// GetChannelCount gets the channel count for the given project.
func (c *Client) GetChannelCount(
	ctx context.Context,
	projectID types.ID,
) (int32, error) {
	ctx, cancel := context.WithTimeout(ctx, c.rpcTimeout)
	defer cancel()

	response, err := c.client.GetChannelCount(
		ctx,
		connect.NewRequest(&api.ClusterServiceGetChannelCountRequest{
			ProjectId: projectID.String(),
		}),
	)
	if err != nil {
		return 0, fromConnectError(err)
	}

	return response.Msg.ChannelCount, nil
}

// InvalidateCache invalidates the cache of the given type and key.
func (c *Client) InvalidateCache(
	ctx context.Context,
	cacheType types.CacheType,
	key string,
) error {
	ctx, cancel := context.WithTimeout(ctx, c.rpcTimeout)
	defer cancel()

	_, err := c.client.InvalidateCache(
		ctx,
		connect.NewRequest(&api.InvalidateCacheRequest{
			CacheType: converter.ToCacheType(cacheType),
			Key:       key,
		}),
	)
	if err != nil {
		return fromConnectError(err)
	}

	return nil
}

/**
* withShardKey returns a context with the given shard key in metadata.
 */
func withShardKey[T any](conn *connect.Request[T], keys ...string) *connect.Request[T] {
	conn.Header().Add(types.ShardKey, strings.Join(keys, "/"))

	return conn
}

// fromConnectError converts connect.Error into our StatusError where possible.
// If conversion is not possible, returns the original error.
func fromConnectError(err error) error {
	var cErr *connect.Error
	if !goerrors.As(err, &cErr) {
		return err
	}

	// Map connect.Code to our pkg/errors constructors where appropriate.
	switch cErr.Code() {
	case connect.CodeInvalidArgument:
		return errors.InvalidArgument(cErr.Error())
	case connect.CodeNotFound:
		return errors.NotFound(cErr.Error())
	case connect.CodeAlreadyExists:
		return errors.AlreadyExists(cErr.Error())
	case connect.CodePermissionDenied:
		return errors.PermissionDenied(cErr.Error())
	case connect.CodeResourceExhausted:
		return errors.ResourceExhausted(cErr.Error())
	case connect.CodeFailedPrecondition:
		return errors.FailedPrecond(cErr.Error())
	case connect.CodeUnauthenticated:
		return errors.Unauthenticated(cErr.Error())
	case connect.CodeInternal:
		return errors.Internal(cErr.Error())
	case connect.CodeUnavailable:
		return errors.Unavailable(cErr.Error())
	default:
		// For codes without direct mapping, return the original connect error
		return err
	}
}
