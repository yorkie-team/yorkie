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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
)

// Option configures Options.
type Option func(*Options)

// WithToken configures the token of the client.
func WithToken(token string) Option {
	return func(o *Options) { o.Token = token }
}

// WithLogger configures the Logger of the client.
func WithLogger(logger *zap.Logger) Option {
	return func(o *Options) { o.Logger = logger }
}

// Options configures how we set up the client.
type Options struct {
	// Token is the token of the user.
	Token string

	// Logger is the Logger of the client.
	Logger *zap.Logger
}

// Client is a client for admin service.
type Client struct {
	conn            *grpc.ClientConn
	client          api.AdminServiceClient
	dialOptions     []grpc.DialOption
	authInterceptor *AuthInterceptor

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

	authInterceptor := NewAuthInterceptor(options.Token)
	dialOptions = append(dialOptions, grpc.WithUnaryInterceptor(authInterceptor.Unary()))
	dialOptions = append(dialOptions, grpc.WithStreamInterceptor(authInterceptor.Stream()))

	logger := options.Logger
	if logger == nil {
		l, err := zap.NewProduction()
		if err != nil {
			return nil, err
		}
		logger = l
	}

	return &Client{
		logger:          logger,
		dialOptions:     dialOptions,
		authInterceptor: authInterceptor,
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
	c.client = api.NewAdminServiceClient(conn)

	return nil
}

// Close closes the connection to the admin service.
func (c *Client) Close() error {
	return c.conn.Close()
}

// LogIn logs in a user.
func (c *Client) LogIn(
	ctx context.Context,
	username,
	password string,
) (string, error) {
	response, err := c.client.LogIn(ctx, &api.LogInRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		return "", err
	}

	c.authInterceptor.SetToken(response.Token)

	return response.Token, nil
}

// SignUp signs up a new user.
func (c *Client) SignUp(
	ctx context.Context,
	username,
	password string,
) (*types.User, error) {
	response, err := c.client.SignUp(ctx, &api.SignUpRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		return nil, err
	}

	return converter.FromUser(response.User)
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

// GetProject gets the project by name.
func (c *Client) GetProject(ctx context.Context, name string) (*types.Project, error) {
	response, err := c.client.GetProject(
		ctx,
		&api.GetProjectRequest{Name: name},
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
	fields *types.UpdatableProjectFields,
) (*types.Project, error) {
	pbProjectField, err := converter.ToUpdatableProjectFields(fields)
	if err != nil {
		return nil, err
	}

	response, err := c.client.UpdateProject(ctx, &api.UpdateProjectRequest{
		Id:     id,
		Fields: pbProjectField,
	})
	if err != nil {
		return nil, err
	}

	return converter.FromProject(response.Project)
}

// ListDocuments lists documents.
func (c *Client) ListDocuments(ctx context.Context, projectName string) ([]*types.DocumentSummary, error) {
	response, err := c.client.ListDocuments(
		ctx,
		&api.ListDocumentsRequest{
			ProjectName: projectName,
		},
	)
	if err != nil {
		return nil, err
	}

	return converter.FromDocumentSummaries(response.Documents)
}

// ListChangeSummaries returns the change summaries of the given document.
func (c *Client) ListChangeSummaries(
	ctx context.Context,
	projectName string,
	key key.Key,
	previousSeq int64,
	pageSize int32,
	isForward bool,
) ([]*types.ChangeSummary, error) {
	resp, err := c.client.ListChanges(ctx, &api.ListChangesRequest{
		ProjectName: projectName,
		DocumentKey: key.String(),
		PreviousSeq: previousSeq,
		PageSize:    pageSize,
		IsForward:   isForward,
	})

	if err != nil {
		return nil, err
	}

	changes, err := converter.FromChanges(resp.Changes)
	if err != nil {
		return nil, err
	}

	if len(changes) == 0 {
		var summaries []*types.ChangeSummary
		return summaries, nil
	}

	seq := changes[0].ServerSeq() - 1

	snapshotMeta, err := c.client.GetSnapshotMeta(ctx, &api.GetSnapshotMetaRequest{
		ProjectName: projectName,
		DocumentKey: key.String(),
		ServerSeq:   seq,
	})
	if err != nil {
		return nil, err
	}

	newDoc, err := document.NewInternalDocumentFromSnapshot(
		key,
		snapshotMeta.ServerSeq,
		snapshotMeta.Lamport,
		snapshotMeta.Snapshot,
	)

	if err != nil {
		return nil, err
	}
	var summaries []*types.ChangeSummary
	for _, c := range changes {
		if c.ServerSeq() <= newDoc.Checkpoint().ServerSeq {
			continue
		}

		if err := newDoc.ApplyChanges(c); err != nil {
			return nil, err
		}

		// TODO(hackerwins): doc.Marshal is expensive function. We need to optimize it.
		summaries = append([]*types.ChangeSummary{{
			ID:       c.ID(),
			Message:  c.Message(),
			Snapshot: newDoc.Marshal(),
		}}, summaries...)
	}

	return summaries, nil
}
