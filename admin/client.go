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

// Package admin provides the client for the admin service.
package admin

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"go.uber.org/zap"
	"golang.org/x/net/http2"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
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

// WithInsecure configures insecure option of the client.
func WithInsecure(isInsecure bool) Option {
	return func(o *Options) { o.IsInsecure = isInsecure }
}

// Options configures how we set up the client.
type Options struct {
	// Token is the token of the user.
	Token string

	// Logger is the Logger of the client.
	Logger *zap.Logger

	// IsInsecure is whether to disable the TLS connection of the client.
	IsInsecure bool
}

// Client is a client for admin service.
type Client struct {
	conn            *http.Client
	client          v1connect.AdminServiceClient
	authInterceptor *AuthInterceptor
	logger          *zap.Logger
	isInsecure      bool
}

// New creates an instance of Client.
func New(opts ...Option) (*Client, error) {
	var options Options
	for _, opt := range opts {
		opt(&options)
	}

	conn := &http.Client{}
	if !options.IsInsecure {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		conn.Transport = &http2.Transport{TLSClientConfig: tlsConfig}
	}

	logger := options.Logger
	if logger == nil {
		l, err := zap.NewProduction()
		if err != nil {
			return nil, fmt.Errorf("create logger: %w", err)
		}
		logger = l
	}

	return &Client{
		conn:            conn,
		logger:          logger,
		authInterceptor: NewAuthInterceptor(options.Token),
		isInsecure:      options.IsInsecure,
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
		if c.isInsecure {
			rpcAddr = "http://" + rpcAddr
		} else {
			rpcAddr = "https://" + rpcAddr
		}
	}

	c.client = v1connect.NewAdminServiceClient(c.conn, rpcAddr, connect.WithInterceptors(c.authInterceptor))

	return nil
}

// Close closes the connection to the admin service.
func (c *Client) Close() {
	c.conn.CloseIdleConnections()
}

// LogIn logs in a user.
func (c *Client) LogIn(
	ctx context.Context,
	username,
	password string,
) (string, error) {
	response, err := c.client.LogIn(ctx, connect.NewRequest(&api.LogInRequest{
		Username: username,
		Password: password,
	}))
	if err != nil {
		return "", err
	}

	c.authInterceptor.SetToken(response.Msg.Token)

	return response.Msg.Token, nil
}

// SignUp signs up a new user.
func (c *Client) SignUp(
	ctx context.Context,
	username,
	password string,
) (*types.User, error) {
	response, err := c.client.SignUp(ctx, connect.NewRequest(&api.SignUpRequest{
		Username: username,
		Password: password,
	}))
	if err != nil {
		return nil, err
	}

	return converter.FromUser(response.Msg.User), nil
}

// CreateProject creates a new project.
func (c *Client) CreateProject(ctx context.Context, name string) (*types.Project, error) {
	response, err := c.client.CreateProject(
		ctx,
		connect.NewRequest(&api.CreateProjectRequest{Name: name}),
	)
	if err != nil {
		return nil, err
	}

	return converter.FromProject(response.Msg.Project), nil
}

// GetProject gets the project by name.
func (c *Client) GetProject(ctx context.Context, name string) (*types.Project, error) {
	response, err := c.client.GetProject(
		ctx,
		connect.NewRequest(&api.GetProjectRequest{Name: name}),
	)
	if err != nil {
		return nil, err
	}

	return converter.FromProject(response.Msg.Project), nil
}

// ListProjects lists all projects.
func (c *Client) ListProjects(ctx context.Context) ([]*types.Project, error) {
	response, err := c.client.ListProjects(
		ctx,
		connect.NewRequest(&api.ListProjectsRequest{}),
	)
	if err != nil {
		return nil, err
	}

	return converter.FromProjects(response.Msg.Projects), nil
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

	response, err := c.client.UpdateProject(ctx, connect.NewRequest(&api.UpdateProjectRequest{
		Id:     id,
		Fields: pbProjectField,
	}))
	if err != nil {
		return nil, err
	}

	return converter.FromProject(response.Msg.Project), nil
}

// CreateDocument creates a new document.
func (c *Client) CreateDocument(
	ctx context.Context,
	projectName string,
	documentKey string,
	initialRoot yson.Object,
) (*types.DocumentSummary, error) {
	marshalled, err := initialRoot.Marshal()
	if err != nil {
		return nil, err
	}

	response, err := c.client.CreateDocument(
		ctx,
		connect.NewRequest(&api.CreateDocumentRequest{
			ProjectName: projectName,
			DocumentKey: documentKey,
			InitialRoot: marshalled,
		}),
	)
	if err != nil {
		return nil, err
	}

	return converter.FromDocumentSummary(response.Msg.Document), nil
}

// ListDocuments lists documents.
func (c *Client) ListDocuments(
	ctx context.Context,
	projectName string,
	previousID string,
	pageSize int32,
	isForward bool,
	includeRoot bool,
) ([]*types.DocumentSummary, error) {
	response, err := c.client.ListDocuments(
		ctx,
		connect.NewRequest(&api.ListDocumentsRequest{
			ProjectName: projectName,
			PreviousId:  previousID,
			PageSize:    pageSize,
			IsForward:   isForward,
			IncludeRoot: includeRoot,
		},
		))
	if err != nil {
		return nil, err
	}

	return converter.FromDocumentSummaries(response.Msg.Documents), nil
}

// UpdateDocument updates a document.
func (c *Client) UpdateDocument(
	ctx context.Context,
	projectName string,
	documentKey key.Key,
	root string,
	schemaKey string,
) (*types.DocumentSummary, error) {
	response, err := c.client.UpdateDocument(
		ctx,
		connect.NewRequest(&api.UpdateDocumentRequest{
			ProjectName: projectName,
			DocumentKey: documentKey.String(),
			Root:        root,
			SchemaKey:   schemaKey,
		}),
	)
	if err != nil {
		return nil, err
	}

	return converter.FromDocumentSummary(response.Msg.Document), nil
}

// RemoveDocument removes a document of the given key.
func (c *Client) RemoveDocument(
	ctx context.Context,
	projectName string,
	documentKey string,
	force bool,
) error {
	project, err := c.GetProject(ctx, projectName)
	if err != nil {
		return err
	}
	apiKey := project.PublicKey

	_, err = c.client.RemoveDocumentByAdmin(
		ctx,
		withShardKey(connect.NewRequest(&api.RemoveDocumentByAdminRequest{
			ProjectName: projectName,
			DocumentKey: documentKey,
			Force:       force,
		}), apiKey, documentKey),
	)
	return err
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
	resp, err := c.client.ListChanges(ctx, connect.NewRequest(&api.ListChangesRequest{
		ProjectName: projectName,
		DocumentKey: key.String(),
		PreviousSeq: previousSeq,
		PageSize:    pageSize,
		IsForward:   isForward,
	}))

	if err != nil {
		return nil, err
	}

	changes, err := converter.FromChanges(resp.Msg.Changes)
	if err != nil {
		return nil, err
	}

	if len(changes) == 0 {
		var summaries []*types.ChangeSummary
		return summaries, nil
	}

	seq := changes[0].ServerSeq() - 1

	snapshotMeta, err := c.client.GetSnapshotMeta(ctx, connect.NewRequest(&api.GetSnapshotMetaRequest{
		ProjectName: projectName,
		DocumentKey: key.String(),
		ServerSeq:   seq,
	}))
	if err != nil {
		return nil, err
	}

	vector, err := converter.FromVersionVector(snapshotMeta.Msg.VersionVector)
	if err != nil {
		return nil, err
	}

	newDoc, err := document.NewInternalDocumentFromSnapshot(
		key,
		seq,
		snapshotMeta.Msg.Lamport,
		vector,
		snapshotMeta.Msg.Snapshot,
	)

	if err != nil {
		return nil, err
	}
	var summaries []*types.ChangeSummary
	for _, c := range changes {
		if _, err := newDoc.ApplyChanges(c); err != nil {
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

// GetServerVersion gets the server version.
func (c *Client) GetServerVersion(ctx context.Context) (*types.VersionDetail, error) {
	response, err := c.client.GetServerVersion(ctx, connect.NewRequest(&api.GetServerVersionRequest{}))
	if err != nil {
		return nil, err
	}

	return &types.VersionDetail{
		YorkieVersion: response.Msg.YorkieVersion,
		GoVersion:     response.Msg.GoVersion,
		BuildDate:     response.Msg.BuildDate,
	}, nil
}

/**
 * withShardKey returns a context with the given shard key in metadata.
 */
func withShardKey[T any](conn *connect.Request[T], keys ...string) *connect.Request[T] {
	conn.Header().Add(types.APIKeyKey, keys[0])
	conn.Header().Add(types.ShardKey, strings.Join(keys, "/"))

	return conn
}

// DeleteAccount deletes the user's account.
func (c *Client) DeleteAccount(ctx context.Context, username, password string) error {
	_, err := c.client.DeleteAccount(ctx, connect.NewRequest(&api.DeleteAccountRequest{
		Username: username,
		Password: password,
	}))
	if err != nil {
		return err
	}

	return nil
}

// ChangePassword changes the user's password.
func (c *Client) ChangePassword(ctx context.Context, username, password, newPassword string) error {
	_, err := c.client.ChangePassword(ctx, connect.NewRequest(&api.ChangePasswordRequest{
		Username:        username,
		CurrentPassword: password,
		NewPassword:     newPassword,
	}))
	if err != nil {
		return err
	}

	return nil
}

// CreateSchema creates a new schema.
func (c *Client) CreateSchema(
	ctx context.Context,
	projectName,
	schemaName string,
	version int,
	schemaBody string,
	rules []types.Rule,
) error {
	_, err := c.client.CreateSchema(ctx, connect.NewRequest(&api.CreateSchemaRequest{
		ProjectName:   projectName,
		SchemaName:    schemaName,
		SchemaVersion: int32(version),
		SchemaBody:    schemaBody,
		Rules:         converter.ToRules(rules),
	}))
	if err != nil {
		return err
	}

	return nil
}

// GetSchemas gets the schemas.
func (c *Client) GetSchemas(
	ctx context.Context,
	projectName,
	schemaName string,
) ([]*types.Schema, error) {
	response, err := c.client.GetSchemas(
		ctx,
		connect.NewRequest(&api.GetSchemasRequest{
			ProjectName: projectName,
			SchemaName:  schemaName,
		}),
	)
	if err != nil {
		return nil, err
	}

	return converter.FromSchemas(response.Msg.Schemas), nil
}

// RemoveSchema removes a schema.
func (c *Client) RemoveSchema(ctx context.Context, projectName, schemaName string, version int) error {
	_, err := c.client.RemoveSchema(ctx, connect.NewRequest(&api.RemoveSchemaRequest{
		ProjectName: projectName,
		SchemaName:  schemaName,
		Version:     int32(version),
	}))
	if err != nil {
		return err
	}

	return nil
}
