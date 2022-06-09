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

package admin

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
)

// Option configures Options.
type Option func(*Options)

// WithLogger configures the Logger of the client.
func WithLogger(logger *zap.Logger) Option {
	return func(o *Options) { o.Logger = logger }
}

// Options configures how we set up the client.
type Options struct {
	// Logger is the Logger of the client.
	Logger *zap.Logger
}

// Client is a client for admin service.
type Client struct {
	conn        *grpc.ClientConn
	client      api.AdminClient
	dialOptions []grpc.DialOption

	logger *zap.Logger
}

// New creates an instance of Client.
func New(opts ...Option) (*Client, error) {
	var options Options
	for _, opt := range opts {
		opt(&options)
	}

	credentials := grpc.WithTransportCredentials(insecure.NewCredentials())
	dialOptions := []grpc.DialOption{credentials}

	logger := options.Logger
	if logger == nil {
		l, err := zap.NewProduction()
		if err != nil {
			return nil, err
		}
		logger = l
	}

	return &Client{
		logger:      logger,
		dialOptions: dialOptions,
	}, nil
}

// Dial creates an instance of Client and dials to the admin service.
func Dial(adminAddr string, opts ...Option) (*Client, error) {
	cli, err := New(opts...)
	if err != nil {
		return nil, err
	}

	if err := cli.Dial(adminAddr); err != nil {
		return nil, err
	}

	return cli, nil
}

// Dial dials to the admin service.
func (c *Client) Dial(adminAddr string) error {
	conn, err := grpc.Dial(adminAddr, c.dialOptions...)
	if err != nil {
		return err
	}

	c.conn = conn
	c.client = api.NewAdminClient(conn)

	return nil
}

// Close closes the connection to the admin service.
func (c *Client) Close() error {
	return c.conn.Close()
}

// CreateProject creates a new project.
func (c *Client) CreateProject(ctx context.Context, name string) (*types.Project, error) {
	response, err := c.client.CreateProject(
		ctx,
		&api.CreateProjectRequest{
			Name: name,
		},
	)
	if err != nil {
		return nil, err
	}

	return converter.FromProject(response.Project)
}

// ListProjects lists all projects.
func (c *Client) ListProjects(ctx context.Context) ([]*types.Project, error) {
	response, err := c.client.ListProjects(
		ctx,
		&api.ListProjectsRequest{},
	)
	if err != nil {
		return nil, err
	}

	return converter.FromProjects(response.Projects)
}

// UpdateProject updates an existing project.
func (c *Client) UpdateProject(
	ctx context.Context,
	id string,
	field *types.ProjectField,
) (*types.Project, error) {
	pbProjectField, err := converter.ToProjectField(field)
	if err != nil {
		return nil, err
	}

	response, err := c.client.UpdateProject(ctx, &api.UpdateProjectRequest{
		Id:    id,
		Field: pbProjectField,
	})
	if err != nil {
		return nil, err
	}

	return converter.FromProject(response.Project)
}
