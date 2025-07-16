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
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// Option configures Options.
type Option func(*Options)

// WithSecure configures secure option of the client.
func WithSecure(isSecure bool) Option {
	return func(o *Options) { o.IsSecure = isSecure }
}

// Options configures how we set up the client.
type Options struct {
	// IsSecure is whether to enable the TLS connection of the client.
	IsSecure bool
}

// Client is a client for admin service.
type Client struct {
	conn     *http.Client
	client   v1connect.ClusterServiceClient
	isSecure bool
}

// New creates an instance of Client.
func New(opts ...Option) (*Client, error) {
	var options Options
	for _, opt := range opts {
		opt(&options)
	}

	conn := &http.Client{}
	if options.IsSecure {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		conn.Transport = &http2.Transport{TLSClientConfig: tlsConfig}
	}

	return &Client{
		conn:     conn,
		isSecure: options.IsSecure,
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
		return err
	}

	return nil
}

// CompactDocument compacts the given document.
func (c *Client) CompactDocument(
	ctx context.Context,
	project *types.Project,
	docInfo *database.DocInfo,
) error {
	_, err := c.client.CompactDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.ClusterServiceCompactDocumentRequest{
			ProjectId:   docInfo.ProjectID.String(),
			DocumentId:  docInfo.ID.String(),
			DocumentKey: docInfo.Key.String(),
		}), project.PublicKey, docInfo.Key.String()))
	if err != nil {
		return err
	}

	return nil
}

// PurgeDocument purges the given document.
func (c *Client) PurgeDocument(
	ctx context.Context,
	project *types.Project,
	docInfo *database.DocInfo,
) error {
	_, err := c.client.PurgeDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.ClusterServicePurgeDocumentRequest{
			ProjectId:   docInfo.ProjectID.String(),
			DocumentId:  docInfo.ID.String(),
			DocumentKey: docInfo.Key.String(),
		}), project.PublicKey, docInfo.Key.String()))
	if err != nil {
		return err
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
		return nil, err
	}

	return converter.FromDocumentSummary(response.Msg.Document), nil
}

/**
* withShardKey returns a context with the given shard key in metadata.
 */
func withShardKey[T any](conn *connect.Request[T], keys ...string) *connect.Request[T] {
	conn.Header().Add(types.ShardKey, strings.Join(keys, "/"))

	return conn
}
