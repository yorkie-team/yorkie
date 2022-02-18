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

package client

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/xid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/types"
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

	// ErrUnsupportedWatchResponseType occurs when the given WatchResponseType
	// is not supported.
	ErrUnsupportedWatchResponseType = errors.New("unsupported watch response type")
)

// Attachment represents the document attached and peers.
type Attachment struct {
	doc   *document.Document
	peers map[string]types.MetadataInfo
}

// Client is a normal client that can communicate with the agent.
// It has documents and sends changes of the document in local
// to the agent to synchronize with other replicas in remote.
type Client struct {
	conn        *grpc.ClientConn
	client      api.YorkieClient
	dialOptions []grpc.DialOption
	logger      *zap.Logger

	id           *time.ActorID
	key          string
	metadataInfo types.MetadataInfo
	status       status
	attachments  map[string]*Attachment
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
	Keys          []key.Key
	PeersMapByDoc map[string]map[string]types.Metadata
	Err           error
}

// New creates an instance of Client.
func New(opts ...Option) (*Client, error) {
	var options Options
	for _, opt := range opts {
		opt(&options)
	}

	var k string
	if options.Key != "" {
		k = options.Key
	} else {
		k = xid.New().String()
	}

	metadata := types.Metadata{}
	if options.Metadata != nil {
		metadata = options.Metadata
	}

	var dialOptions []grpc.DialOption
	if options.CertFile != "" {
		creds, err := credentials.NewClientTLSFromFile(
			options.CertFile,
			options.ServerNameOverride,
		)
		if err != nil {
			return nil, err
		}
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(creds))
	} else {
		dialOptions = append(dialOptions, grpc.WithInsecure())
	}

	if options.Token != "" {
		authInterceptor := NewAuthInterceptor(options.Token)
		dialOptions = append(dialOptions, grpc.WithUnaryInterceptor(authInterceptor.Unary()))
		dialOptions = append(dialOptions, grpc.WithStreamInterceptor(authInterceptor.Stream()))
	}

	var logger *zap.Logger
	var err error
	if options.Logger != nil {
		logger = options.Logger
	} else {
		logger, err = zap.NewProduction()
		if err != nil {
			return nil, err
		}
	}

	return &Client{
		dialOptions: dialOptions,
		logger:      logger,

		key:          k,
		metadataInfo: types.MetadataInfo{Data: metadata},
		status:       deactivated,
		attachments:  make(map[string]*Attachment),
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
		return err
	}

	c.conn = conn
	c.client = api.NewYorkieClient(conn)

	return nil
}

// Close closes all resources of this client.
func (c *Client) Close() error {
	if err := c.Deactivate(context.Background()); err != nil {
		return err
	}

	return c.conn.Close()
}

// Activate activates this client. That is, it registers itself to the agent
// and receives a unique ID from the agent. The given ID is used to distinguish
// different clients.
func (c *Client) Activate(ctx context.Context) error {
	if c.status == activated {
		return nil
	}

	response, err := c.client.ActivateClient(ctx, &api.ActivateClientRequest{
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
	c.id = &clientID

	return nil
}

// Deactivate deactivates this client.
func (c *Client) Deactivate(ctx context.Context) error {
	if c.status == deactivated {
		return nil
	}

	_, err := c.client.DeactivateClient(ctx, &api.DeactivateClientRequest{
		ClientId: c.id.Bytes(),
	})
	if err != nil {
		return err
	}

	c.status = deactivated

	return nil
}

// Attach attaches the given document to this client. It tells the agent that
// this client will synchronize the given document.
func (c *Client) Attach(ctx context.Context, doc *document.Document) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	doc.SetActor(*c.id)

	pbChangePack, err := converter.ToChangePack(doc.CreateChangePack())
	if err != nil {
		return err
	}

	res, err := c.client.AttachDocument(ctx, &api.AttachDocumentRequest{
		ClientId:   c.id.Bytes(),
		ChangePack: pbChangePack,
	})
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

	doc.SetStatus(document.Attached)
	c.attachments[doc.Key().CombinedKey()] = &Attachment{
		doc:   doc,
		peers: make(map[string]types.MetadataInfo),
	}

	return nil
}

// Detach detaches the given document from this client. It tells the
// agent that this client will no longer synchronize the given document.
//
// To collect garbage things like CRDT tombstones left on the document, all the
// changes should be applied to other replicas before GC time. For this, if the
// document is no longer used by this client, it should be detached.
func (c *Client) Detach(ctx context.Context, doc *document.Document) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	if _, ok := c.attachments[doc.Key().CombinedKey()]; !ok {
		return ErrDocumentNotAttached
	}

	pbChangePack, err := converter.ToChangePack(doc.CreateChangePack())
	if err != nil {
		return err
	}

	res, err := c.client.DetachDocument(ctx, &api.DetachDocumentRequest{
		ClientId:   c.id.Bytes(),
		ChangePack: pbChangePack,
	})
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

	doc.SetStatus(document.Detached)
	delete(c.attachments, doc.Key().CombinedKey())

	return nil
}

// Sync pushes local changes of the attached documents to the Agent and
// receives changes of the remote replica from the agent then apply them to
// local documents.
func (c *Client) Sync(ctx context.Context, keys ...key.Key) error {
	if len(keys) == 0 {
		for _, attachment := range c.attachments {
			keys = append(keys, attachment.doc.Key())
		}
	}

	for _, k := range keys {
		if err := c.sync(ctx, k); err != nil {
			return err
		}
	}

	return nil
}

// Watch subscribes to events on a given document.
// If an error occurs before stream initialization, the second response, error,
// is returned. If the context "ctx" is canceled or timed out, returned channel
// is closed, and "WatchResponse" from this closed channel has zero events and
// nil "Err()".
func (c *Client) Watch(
	ctx context.Context,
	docs ...*document.Document,
) (<-chan WatchResponse, error) {
	var keys []key.Key
	for _, doc := range docs {
		keys = append(keys, doc.Key())
	}

	rch := make(chan WatchResponse)
	stream, err := c.client.WatchDocuments(ctx, &api.WatchDocumentsRequest{
		Client: converter.ToClient(types.Client{
			ID:           *c.id,
			MetadataInfo: c.metadataInfo,
		}),
		DocumentKeys: converter.ToDocumentKeys(keys),
	})
	if err != nil {
		return nil, err
	}

	handleResponse := func(pbResp *api.WatchDocumentsResponse) (*WatchResponse, error) {
		switch resp := pbResp.Body.(type) {
		case *api.WatchDocumentsResponse_Initialization_:
			for docID, peers := range resp.Initialization.PeersMapByDoc {
				clients, err := converter.FromClients(peers)
				if err != nil {
					return nil, err
				}

				attachment := c.attachments[docID]
				for _, cli := range clients {
					attachment.peers[cli.ID.String()] = cli.MetadataInfo
				}
			}

			return nil, nil
		case *api.WatchDocumentsResponse_Event:
			eventType, err := converter.FromEventType(resp.Event.Type)
			if err != nil {
				return nil, err
			}

			switch eventType {
			case types.DocumentsChangedEvent:
				return &WatchResponse{
					Type: DocumentsChanged,
					Keys: converter.FromDocumentKeys(resp.Event.DocumentKeys),
				}, nil
			case types.DocumentsWatchedEvent, types.DocumentsUnwatchedEvent, types.MetadataChangedEvent:
				for _, k := range converter.FromDocumentKeys(resp.Event.DocumentKeys) {
					cli, err := converter.FromClient(resp.Event.Publisher)
					if err != nil {
						return nil, err
					}

					attachment := c.attachments[k.CombinedKey()]
					if eventType == types.DocumentsWatchedEvent ||
						eventType == types.MetadataChangedEvent {
						if info, ok := attachment.peers[cli.ID.String()]; ok {
							cli.MetadataInfo.Update(info)
						}
						attachment.peers[cli.ID.String()] = cli.MetadataInfo
					} else {
						delete(attachment.peers, cli.ID.String())
					}
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

// UpdateMetadata updates the metadata of this client.
func (c *Client) UpdateMetadata(ctx context.Context, k, v string) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	c.metadataInfo.Data[k] = v
	c.metadataInfo.Clock++

	if len(c.attachments) == 0 {
		return nil
	}

	var keys []key.Key
	for _, attachment := range c.attachments {
		keys = append(keys, attachment.doc.Key())
	}

	// TODO(hackerwins): We temporarily use Unary Call to update metadata,
	// because grpc-web can't handle Bi-Directional streaming for now.
	// After grpc-web supports bi-directional streaming, we can remove the
	// following.
	if _, err := c.client.UpdateMetadata(ctx, &api.UpdateMetadataRequest{
		Client: converter.ToClient(types.Client{
			ID:           *c.id,
			MetadataInfo: c.metadataInfo,
		}),
		DocumentKeys: converter.ToDocumentKeys(keys),
	}); err != nil {
		return err
	}

	return nil
}

// FetchHistory returns the history of the given document.
func (c *Client) FetchHistory(ctx context.Context, key key.Key) ([]*types.ChangeSummary, error) {
	resp, err := c.client.FetchHistory(ctx, &api.FetchHistoryRequest{
		ClientId:    c.id.Bytes(),
		DocumentKey: converter.ToDocumentKey(key),
	})
	if err != nil {
		return nil, err
	}

	changes, err := converter.FromChanges(resp.Changes)
	if err != nil {
		return nil, err
	}

	doc := document.NewInternalDocument(key)

	var summaries []*types.ChangeSummary
	for _, c := range changes {
		if err := doc.ApplyChanges(c); err != nil {
			return nil, err
		}

		// TODO(hackerwins): doc.Marshal is expensive function. We need to optimize it.
		summaries = append(summaries, &types.ChangeSummary{
			ID:       c.ID(),
			Message:  c.Message(),
			Snapshot: doc.Marshal(),
		})
	}

	return summaries, nil
}

// ID returns the ID of this client.
func (c *Client) ID() *time.ActorID {
	return c.id
}

// Key returns the key of this client.
func (c *Client) Key() string {
	return c.key
}

// Metadata returns the metadata of this client.
func (c *Client) Metadata() types.Metadata {
	metadata := make(types.Metadata)
	for k, v := range c.metadataInfo.Data {
		metadata[k] = v
	}

	return metadata
}

// PeersMapByDoc returns the peersMap.
func (c *Client) PeersMapByDoc() map[string]map[string]types.Metadata {
	peersMapByDoc := make(map[string]map[string]types.Metadata)
	for doc, attachment := range c.attachments {
		peers := make(map[string]types.Metadata)
		for id, metadata := range attachment.peers {
			peers[id] = metadata.Data
		}
		peersMapByDoc[doc] = peers
	}
	return peersMapByDoc
}

// IsActive returns whether this client is active or not.
func (c *Client) IsActive() bool {
	return c.status == activated
}

func (c *Client) sync(ctx context.Context, key key.Key) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	attachment, ok := c.attachments[key.CombinedKey()]
	if !ok {
		return ErrDocumentNotAttached
	}

	pbChangePack, err := converter.ToChangePack(attachment.doc.CreateChangePack())
	if err != nil {
		return err
	}

	res, err := c.client.PushPull(ctx, &api.PushPullRequest{
		ClientId:   c.id.Bytes(),
		ChangePack: pbChangePack,
	})
	if err != nil {
		c.logger.Error("failed to sync", zap.Error(err))
		return err
	}

	pack, err := converter.FromChangePack(res.ChangePack)
	if err != nil {
		return err
	}

	if err := attachment.doc.ApplyChangePack(pack); err != nil {
		c.logger.Error("failed to apply change pack", zap.Error(err))
		return err
	}

	return nil
}

// Restore restores the given document to the given change summary.
func (c *Client) Restore(
	ctx context.Context,
	collection string,
	documentName string,
	change *types.ChangeSummary,
	message string,
) (*document.Document, error) {
	if c.status != activated {
		return nil, ErrClientNotActivated
	}

	doc := document.New(collection, documentName)

	res, err := c.client.RestoreDocument(ctx, &api.RestoreDocumentRequest{
		ClientId:    c.id.Bytes(),
		DocumentKey: converter.ToDocumentKey(doc.Key()),
		ChangeId:    converter.ToChangeID(change.ID),
		Message:     message,
	})
	if err != nil {
		return nil, err
	}

	pack, err := converter.FromChangePack(res.ChangePack)
	if err != nil {
		return nil, err
	}

	if err := doc.ApplyChangePack(pack); err != nil {
		return nil, err
	}

	if c.logger.Core().Enabled(zap.DebugLevel) {
		c.logger.Debug(fmt.Sprintf(
			"after apply %d changes: %s",
			len(pack.Changes),
			doc.RootObject().Marshal(),
		))
	}

	return doc, nil
}
