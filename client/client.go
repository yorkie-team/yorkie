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

// Package client provides the client implementation of Yorkie. It is used to
// connect to the server and attach documents.
package client

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/metadata"

	"github.com/rs/xid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

type status int

const (
	deactivated status = iota
	activated
)

var (
	// ErrClientNotActivated occurs when an inactive client executes a function
	// that can only be executed when activated.
	ErrClientNotActivated = errors.New("client is not activated")

	// ErrDocumentNotAttached occurs when the given document is not attached to
	// this client.
	ErrDocumentNotAttached = errors.New("document is not attached")

	// ErrDocumentNotDetached occurs when the given document is not detached from
	// this client.
	ErrDocumentNotDetached = errors.New("document is not detached")

	// ErrUnsupportedWatchResponseType occurs when the given WatchResponseType
	// is not supported.
	ErrUnsupportedWatchResponseType = errors.New("unsupported watch response type")
)

// SyncOption is an option for sync. It contains the key of the document to
// sync and the sync mode.
type SyncOption struct {
	key  key.Key
	mode types.SyncMode
}

// WithPushOnly returns a SyncOption with the sync mode set to PushOnly.
func (o SyncOption) WithPushOnly() SyncOption {
	return SyncOption{
		key:  o.key,
		mode: types.SyncModePushOnly,
	}
}

// Attachment represents the document attached and peers.
type Attachment struct {
	doc   *document.Document
	docID types.ID
	peers map[string]types.PresenceInfo
}

// Client is a normal client that can communicate with the server.
// It has documents and sends changes of the document in local
// to the server to synchronize with other replicas in remote.
type Client struct {
	conn        *grpc.ClientConn
	client      api.YorkieServiceClient
	options     Options
	dialOptions []grpc.DialOption
	logger      *zap.Logger

	id           *time.ActorID
	key          string
	presenceInfo types.PresenceInfo
	status       status
	attachments  map[key.Key]*Attachment
}

// WatchResponseType is type of watch response.
type WatchResponseType string

// The values below are types of WatchResponseType.
const (
	DocumentsChanged WatchResponseType = "documents-changed"
	PeersChanged     WatchResponseType = "peers-changed"
)

// WatchResponse is a structure representing response of Watch.
type WatchResponse struct {
	Type          WatchResponseType
	Key           key.Key
	PeersMapByDoc map[key.Key]map[string]types.Presence
	Err           error
}

// New creates an instance of Client.
func New(opts ...Option) (*Client, error) {
	var options Options
	for _, opt := range opts {
		opt(&options)
	}

	k := options.Key
	if k == "" {
		k = xid.New().String()
	}

	presence := types.Presence{}
	if options.Presence != nil {
		presence = options.Presence
	}

	var dialOptions []grpc.DialOption

	transportCreds := grpc.WithTransportCredentials(insecure.NewCredentials())
	if options.CertFile != "" {
		creds, err := credentials.NewClientTLSFromFile(options.CertFile, options.ServerNameOverride)
		if err != nil {
			return nil, fmt.Errorf("create client tls from file: %w", err)
		}
		transportCreds = grpc.WithTransportCredentials(creds)
	}
	dialOptions = append(dialOptions, transportCreds)

	authInterceptor := NewAuthInterceptor(options.APIKey, options.Token)
	dialOptions = append(dialOptions, grpc.WithUnaryInterceptor(authInterceptor.Unary()))
	dialOptions = append(dialOptions, grpc.WithStreamInterceptor(authInterceptor.Stream()))

	if options.MaxCallRecvMsgSize != 0 {
		dialOptions = append(dialOptions, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(options.MaxCallRecvMsgSize)))
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
		dialOptions: dialOptions,
		options:     options,
		logger:      logger,

		key:          k,
		presenceInfo: types.PresenceInfo{Presence: presence},
		status:       deactivated,
		attachments:  make(map[key.Key]*Attachment),
	}, nil
}

// Dial creates an instance of Client and dials the given rpcAddr.
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

// Dial dials the given rpcAddr.
func (c *Client) Dial(rpcAddr string) error {
	conn, err := grpc.Dial(rpcAddr, c.dialOptions...)
	if err != nil {
		return fmt.Errorf("dial to %s: %w", rpcAddr, err)
	}

	c.conn = conn
	c.client = api.NewYorkieServiceClient(conn)

	return nil
}

// Close closes all resources of this client.
func (c *Client) Close() error {
	if err := c.Deactivate(context.Background()); err != nil {
		return err
	}

	if err := c.conn.Close(); err != nil {
		return fmt.Errorf("close connection: %w", err)
	}

	return nil
}

// Activate activates this client. That is, it registers itself to the server
// and receives a unique ID from the server. The given ID is used to distinguish
// different clients.
func (c *Client) Activate(ctx context.Context) error {
	if c.status == activated {
		return nil
	}

	response, err := c.client.ActivateClient(withShardKey(ctx, c.options.APIKey), &api.ActivateClientRequest{
		ClientKey: c.key,
	})
	if err != nil {
		return err
	}

	clientID, err := time.ActorIDFromBytes(response.ClientId)
	if err != nil {
		return err
	}

	c.status = activated
	c.id = clientID

	return nil
}

// Deactivate deactivates this client.
func (c *Client) Deactivate(ctx context.Context) error {
	if c.status == deactivated {
		return nil
	}

	_, err := c.client.DeactivateClient(withShardKey(ctx, c.options.APIKey), &api.DeactivateClientRequest{
		ClientId: c.id.Bytes(),
	})
	if err != nil {
		return err
	}

	c.status = deactivated

	return nil
}

// Attach attaches the given document to this client. It tells the server that
// this client will synchronize the given document.
func (c *Client) Attach(ctx context.Context, doc *document.Document) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	if doc.Status() != document.StatusDetached {
		return ErrDocumentNotDetached
	}

	doc.SetActor(c.id)

	pbChangePack, err := converter.ToChangePack(doc.CreateChangePack())
	if err != nil {
		return err
	}

	res, err := c.client.AttachDocument(
		withShardKey(ctx, c.options.APIKey, doc.Key().String()),
		&api.AttachDocumentRequest{
			ClientId:   c.id.Bytes(),
			ChangePack: pbChangePack,
		},
	)
	if err != nil {
		return err
	}

	pack, err := converter.FromChangePack(res.ChangePack)
	if err != nil {
		return err
	}

	if err := doc.ApplyChangePack(pack); err != nil {
		return err
	}
	if c.logger.Core().Enabled(zap.DebugLevel) {
		c.logger.Debug(fmt.Sprintf(
			"after apply %d changes: %s",
			len(pack.Changes),
			doc.RootObject().Marshal(),
		))
	}

	if doc.Status() == document.StatusRemoved {
		return nil
	}

	doc.SetStatus(document.StatusAttached)
	c.attachments[doc.Key()] = &Attachment{
		doc:   doc,
		docID: types.ID(res.DocumentId),
		peers: make(map[string]types.PresenceInfo),
	}

	return nil
}

// Detach detaches the given document from this client. It tells the
// server that this client will no longer synchronize the given document.
//
// To collect garbage things like CRDT tombstones left on the document, all the
// changes should be applied to other replicas before GC time. For this, if the
// document is no longer used by this client, it should be detached.
func (c *Client) Detach(ctx context.Context, doc *document.Document, removeIfNotAttached bool) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	attachment, ok := c.attachments[doc.Key()]
	if !ok {
		return ErrDocumentNotAttached
	}

	pbChangePack, err := converter.ToChangePack(doc.CreateChangePack())
	if err != nil {
		return err
	}

	res, err := c.client.DetachDocument(
		withShardKey(ctx, c.options.APIKey, doc.Key().String()),
		&api.DetachDocumentRequest{
			ClientId:            c.id.Bytes(),
			DocumentId:          attachment.docID.String(),
			ChangePack:          pbChangePack,
			RemoveIfNotAttached: removeIfNotAttached,
		},
	)
	if err != nil {
		return err
	}

	pack, err := converter.FromChangePack(res.ChangePack)
	if err != nil {
		return err
	}

	if err := doc.ApplyChangePack(pack); err != nil {
		return err
	}
	if doc.Status() != document.StatusRemoved {
		doc.SetStatus(document.StatusDetached)
	}
	delete(c.attachments, doc.Key())

	return nil
}

// WithDocKey creates a SyncOption with the given document key.
func WithDocKey(k key.Key) SyncOption {
	return SyncOption{
		key:  k,
		mode: types.SyncModePushPull,
	}
}

// Sync pushes local changes of the attached documents to the server and
// receives changes of the remote replica from the server then apply them to
// local documents.
func (c *Client) Sync(ctx context.Context, options ...SyncOption) error {
	if len(options) == 0 {
		for _, attachment := range c.attachments {
			options = append(options, WithDocKey(attachment.doc.Key()))
		}
	}

	for _, opt := range options {
		if err := c.pushPullChanges(ctx, opt); err != nil {
			return err
		}
	}

	return nil
}

// Watch subscribes to events on a given documentIDs.
// If an error occurs before stream initialization, the second response, error,
// is returned. If the context "ctx" is canceled or timed out, returned channel
// is closed, and "WatchResponse" from this closed channel has zero events and
// nil "Err()".
func (c *Client) Watch(
	ctx context.Context,
	doc *document.Document,
) (<-chan WatchResponse, error) {
	attachment, ok := c.attachments[doc.Key()]
	if !ok {
		return nil, ErrDocumentNotAttached
	}

	rch := make(chan WatchResponse)
	stream, err := c.client.WatchDocument(
		withShardKey(ctx, c.options.APIKey, doc.Key().String()),
		&api.WatchDocumentRequest{
			Client: converter.ToClient(types.Client{
				ID:           c.id,
				PresenceInfo: c.presenceInfo,
			}),
			DocumentId: attachment.docID.String(),
		},
	)
	if err != nil {
		return nil, err
	}

	handleResponse := func(pbResp *api.WatchDocumentResponse) (*WatchResponse, error) {
		switch resp := pbResp.Body.(type) {
		case *api.WatchDocumentResponse_Initialization_:
			clients, err := converter.FromClients(resp.Initialization.Peers)
			if err != nil {
				return nil, err
			}

			attachment := c.attachments[doc.Key()]
			for _, cli := range clients {
				attachment.peers[cli.ID.String()] = cli.PresenceInfo
			}

			return nil, nil
		case *api.WatchDocumentResponse_Event:
			eventType, err := converter.FromEventType(resp.Event.Type)
			if err != nil {
				return nil, err
			}

			docKey, err := c.findDocKey(resp.Event.DocumentId)
			if err != nil {
				return nil, err
			}

			switch eventType {
			case types.DocumentsChangedEvent:
				return &WatchResponse{
					Type: DocumentsChanged,
					Key:  docKey,
				}, nil
			case types.DocumentsWatchedEvent, types.DocumentsUnwatchedEvent, types.PresenceChangedEvent:
				cli, err := converter.FromClient(resp.Event.Publisher)
				if err != nil {
					return nil, err
				}

				attachment := c.attachments[docKey]
				if eventType == types.DocumentsWatchedEvent ||
					eventType == types.PresenceChangedEvent {
					if info, ok := attachment.peers[cli.ID.String()]; ok {
						cli.PresenceInfo.Update(info)
					}
					attachment.peers[cli.ID.String()] = cli.PresenceInfo
				} else {
					delete(attachment.peers, cli.ID.String())
				}

				return &WatchResponse{
					Type:          PeersChanged,
					PeersMapByDoc: c.PeersMapByDoc(),
				}, nil
			}
		}
		return nil, ErrUnsupportedWatchResponseType
	}

	pbResp, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if _, err := handleResponse(pbResp); err != nil {
		return nil, err
	}

	go func() {
		for {
			pbResp, err := stream.Recv()
			if err != nil {
				rch <- WatchResponse{Err: err}
				close(rch)
				return
			}
			resp, err := handleResponse(pbResp)
			if err != nil {
				rch <- WatchResponse{Err: err}
				close(rch)
				return
			}
			rch <- *resp
		}
	}()

	return rch, nil
}

func (c *Client) findDocKey(docID string) (key.Key, error) {
	for _, attachment := range c.attachments {
		if attachment.docID.String() == docID {
			return attachment.doc.Key(), nil
		}
	}

	return "", ErrDocumentNotAttached
}

// UpdatePresence updates the presence of this client.
func (c *Client) UpdatePresence(ctx context.Context, k, v string) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	c.presenceInfo.Presence[k] = v
	c.presenceInfo.Clock++

	if len(c.attachments) == 0 {
		return nil
	}

	// TODO(hackerwins): We temporarily use Unary Call to update presence,
	// because grpc-web can't handle Bi-Directional streaming for now.
	// After grpc-web supports bi-directional streaming, we can remove the
	// following.
	// TODO(hackerwins): We will move Presence from client-level to document-level.
	for _, attachment := range c.attachments {
		if _, err := c.client.UpdatePresence(
			withShardKey(ctx, c.options.APIKey, attachment.doc.Key().String()),
			&api.UpdatePresenceRequest{
				Client: converter.ToClient(types.Client{
					ID:           c.id,
					PresenceInfo: c.presenceInfo,
				}),
				DocumentId: attachment.docID.String(),
			}); err != nil {
			return err
		}
	}

	return nil
}

// ID returns the ID of this client.
func (c *Client) ID() *time.ActorID {
	return c.id
}

// Key returns the key of this client.
func (c *Client) Key() string {
	return c.key
}

// Presence returns the presence data of this client.
func (c *Client) Presence() types.Presence {
	presence := make(types.Presence)
	for k, v := range c.presenceInfo.Presence {
		presence[k] = v
	}

	return presence
}

// PeersMapByDoc returns the peersMap.
func (c *Client) PeersMapByDoc() map[key.Key]map[string]types.Presence {
	peersMapByDoc := make(map[key.Key]map[string]types.Presence)
	for docKey, attachment := range c.attachments {
		peers := make(map[string]types.Presence)
		for id, info := range attachment.peers {
			peers[id] = info.Presence
		}
		peersMapByDoc[docKey] = peers
	}
	return peersMapByDoc
}

// IsActive returns whether this client is active or not.
func (c *Client) IsActive() bool {
	return c.status == activated
}

// pushPullChanges pushes the changes of the document to the server and pulls the changes from the server.
func (c *Client) pushPullChanges(ctx context.Context, opt SyncOption) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	attachment, ok := c.attachments[opt.key]
	if !ok {
		return ErrDocumentNotAttached
	}

	pbChangePack, err := converter.ToChangePack(attachment.doc.CreateChangePack())
	if err != nil {
		return err
	}

	res, err := c.client.PushPullChanges(
		withShardKey(ctx, c.options.APIKey, opt.key.String()),
		&api.PushPullChangesRequest{
			ClientId:   c.id.Bytes(),
			DocumentId: attachment.docID.String(),
			ChangePack: pbChangePack,
			PushOnly:   opt.mode == types.SyncModePushOnly,
		},
	)
	if err != nil {
		return err
	}

	pack, err := converter.FromChangePack(res.ChangePack)
	if err != nil {
		return err
	}

	if err := attachment.doc.ApplyChangePack(pack); err != nil {
		return err
	}
	if attachment.doc.Status() == document.StatusRemoved {
		delete(c.attachments, attachment.doc.Key())
	}

	return nil
}

// Remove removes the given document.
func (c *Client) Remove(ctx context.Context, doc *document.Document) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	attachment, ok := c.attachments[doc.Key()]
	if !ok {
		return ErrDocumentNotAttached
	}

	pbChangePack, err := converter.ToChangePack(doc.CreateChangePack())
	if err != nil {
		return err
	}
	pbChangePack.IsRemoved = true

	res, err := c.client.RemoveDocument(
		withShardKey(ctx, c.options.APIKey, doc.Key().String()),
		&api.RemoveDocumentRequest{
			ClientId:   c.id.Bytes(),
			DocumentId: attachment.docID.String(),
			ChangePack: pbChangePack,
		},
	)
	if err != nil {
		return err
	}

	pack, err := converter.FromChangePack(res.ChangePack)
	if err != nil {
		return err
	}

	if err := doc.ApplyChangePack(pack); err != nil {
		return err
	}
	if doc.Status() == document.StatusRemoved {
		delete(c.attachments, doc.Key())
	}

	return nil
}

/**
 * withShardKey returns a context with the given shard key in metadata.
 */
func withShardKey(ctx context.Context, keys ...string) context.Context {
	return metadata.AppendToOutgoingContext(
		ctx,
		types.ShardKey,
		strings.Join(keys, "/"),
	)
}
