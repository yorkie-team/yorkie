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

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/internal/log"
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
)

// Metadata represents custom metadata that can be defined in the client.
type Metadata map[string]string

// Attachment represents the document attached and peers.
type Attachment struct {
	doc         *document.Document
	peerClients map[string]Metadata
}

// Client is a normal client that can communicate with the agent.
// It has documents and sends changes of the document in local
// to the agent to synchronize with other replicas in remote.
type Client struct {
	conn        *grpc.ClientConn
	client      api.YorkieClient
	dialOptions []grpc.DialOption

	id          *time.ActorID
	key         string
	metadata    Metadata
	status      status
	attachments map[string]*Attachment
}

// Option configures how we set up the client.
type Option struct {
	Key      string
	Metadata Metadata
	Token    string

	CertFile           string
	ServerNameOverride string
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
	Keys          []*key.Key
	PeersMapByDoc map[string]map[string]Metadata
	Err           error
}

// NewClient creates an instance of Client.
func NewClient(opts ...Option) (*Client, error) {
	var k string
	if len(opts) > 0 && opts[0].Key != "" {
		k = opts[0].Key
	} else {
		k = uuid.New().String()
	}

	metadata := Metadata{}
	if len(opts) > 0 && opts[0].Metadata != nil {
		metadata = opts[0].Metadata
	}

	var certFile string
	if len(opts) > 0 && opts[0].CertFile != "" {
		certFile = opts[0].CertFile
	}
	var serverNameOverride string
	if len(opts) > 0 && opts[0].ServerNameOverride != "" {
		serverNameOverride = opts[0].ServerNameOverride
	}

	var dialOptions []grpc.DialOption
	if certFile != "" {
		creds, err := credentials.NewClientTLSFromFile(
			certFile,
			serverNameOverride,
		)
		if err != nil {
			log.Logger.Error(err)
			return nil, err
		}
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(creds))
	} else {
		dialOptions = append(dialOptions, grpc.WithInsecure())
	}

	var token string
	if len(opts) > 0 && opts[0].Token != "" {
		token = opts[0].Token
	}
	if token != "" {
		authInterceptor := NewAuthInterceptor(token)
		dialOptions = append(dialOptions, grpc.WithUnaryInterceptor(authInterceptor.Unary()))
		dialOptions = append(dialOptions, grpc.WithStreamInterceptor(authInterceptor.Stream()))
	}

	return &Client{
		key:         k,
		metadata:    metadata,
		dialOptions: dialOptions,
		status:      deactivated,
		attachments: make(map[string]*Attachment),
	}, nil
}

// Dial creates an instance of Client and dials the given rpcAddr.
func Dial(rpcAddr string, opts ...Option) (*Client, error) {
	cli, err := NewClient(opts...)
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
		log.Logger.Error(err)
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

	if err := c.conn.Close(); err != nil {
		log.Logger.Error(err)
		return err
	}

	return nil
}

// Activate activates this client. That is, it register itself to the agent
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
		log.Logger.Error(err)
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

	_, err := c.client.DeactivateClient(ctx, &api.DeactivateClientRequest{
		ClientId: c.id.Bytes(),
	})
	if err != nil {
		log.Logger.Error(err)
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

	doc.SetActor(c.id)

	pbChangePack, err := converter.ToChangePack(doc.CreateChangePack())
	if err != nil {
		return err
	}

	res, err := c.client.AttachDocument(ctx, &api.AttachDocumentRequest{
		ClientId:   c.id.Bytes(),
		ChangePack: pbChangePack,
	})
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	pack, err := converter.FromChangePack(res.ChangePack)
	if err != nil {
		return err
	}

	if err := doc.ApplyChangePack(pack); err != nil {
		log.Logger.Error(err)
		return err
	}

	doc.SetStatus(document.Attached)
	c.attachments[doc.Key().BSONKey()] = &Attachment{
		doc:         doc,
		peerClients: make(map[string]Metadata),
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

	if _, ok := c.attachments[doc.Key().BSONKey()]; !ok {
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
		log.Logger.Error(err)
		return err
	}

	pack, err := converter.FromChangePack(res.ChangePack)
	if err != nil {
		return err
	}

	if err := doc.ApplyChangePack(pack); err != nil {
		log.Logger.Error(err)
		return err
	}

	doc.SetStatus(document.Detached)
	delete(c.attachments, doc.Key().BSONKey())

	return nil
}

// Sync pushes local changes of the attached documents to the Agent and
// receives changes of the remote replica from the agent then apply them to
// local documents.
func (c *Client) Sync(ctx context.Context, keys ...*key.Key) error {
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

// Metadata returns the metadata of this client.
func (c *Client) Metadata() Metadata {
	return c.metadata
}

// PeersMapByDoc returns the peersMap.
func (c *Client) PeersMapByDoc() map[string]map[string]Metadata {
	peersMapByDoc := make(map[string]map[string]Metadata)
	for doc, attachment := range c.attachments {
		peers := make(map[string]Metadata)
		for id, metadata := range attachment.peerClients {
			peers[id] = metadata
		}
		peersMapByDoc[doc] = peers
	}
	return peersMapByDoc
}

// Watch subscribes to events on a given document.
// If the context "ctx" is canceled or timed out, returned channel is closed,
// and "WatchResponse" from this closed channel has zero events and nil "Err()".
func (c *Client) Watch(ctx context.Context, docs ...*document.Document) <-chan WatchResponse {
	var keys []*key.Key
	for _, doc := range docs {
		keys = append(keys, doc.Key())
	}

	rch := make(chan WatchResponse)
	stream, err := c.client.WatchDocuments(ctx, &api.WatchDocumentsRequest{
		Client: converter.ToClient(types.Client{
			ID:       c.id,
			Metadata: c.metadata,
		}),
		DocumentKeys: converter.ToDocumentKeys(keys),
	})
	if err != nil {
		rch <- WatchResponse{Err: err}
		close(rch)
		return rch
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
				for _, client := range clients {
					attachment.peerClients[client.ID.String()] = client.Metadata
				}
			}

			return &WatchResponse{
				Type:          PeersChanged,
				PeersMapByDoc: c.PeersMapByDoc(),
			}, nil
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
			case types.DocumentsWatchedEvent, types.DocumentsUnwatchedEvent:
				for _, k := range converter.FromDocumentKeys(resp.Event.DocumentKeys) {
					cli, err := converter.FromClient(resp.Event.Publisher)
					if err != nil {
						return nil, err
					}

					attachment := c.attachments[k.BSONKey()]
					if eventType == types.DocumentsWatchedEvent {
						attachment.peerClients[cli.ID.String()] = cli.Metadata
					} else {
						delete(attachment.peerClients, cli.ID.String())
					}
				}
				return &WatchResponse{
					Type:          PeersChanged,
					PeersMapByDoc: c.PeersMapByDoc(),
				}, nil
			}
		}
		return nil, fmt.Errorf("unsupported response type")
	}

	// waiting for starting watch
	pbResp, err := stream.Recv()
	if err != nil {
		rch <- WatchResponse{Err: err}
		close(rch)
		return rch
	}
	if _, err := handleResponse(pbResp); err != nil {
		rch <- WatchResponse{Err: err}
		close(rch)
		return rch
	}

	// starting to watch documents
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

	return rch
}

// ID returns the ID of this client.
func (c *Client) ID() *time.ActorID {
	return c.id
}

// Key returns the key of this client.
func (c *Client) Key() string {
	return c.key
}

// IsActive returns whether this client is active or not.
func (c *Client) IsActive() bool {
	return c.status == activated
}

func (c *Client) sync(ctx context.Context, key *key.Key) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	attachment, ok := c.attachments[key.BSONKey()]
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
		log.Logger.Error(err)
		return err
	}

	pack, err := converter.FromChangePack(res.ChangePack)
	if err != nil {
		return err
	}

	if err := attachment.doc.ApplyChangePack(pack); err != nil {
		log.Logger.Error(err)
		return err
	}

	return nil
}
