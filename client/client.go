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

	"github.com/google/uuid"
	"google.golang.org/grpc"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
)

type status int

const (
	deactivated status = 0
	activated   status = 1
)

var (
	errClientNotActivated  = errors.New("client is not activated")
	errDocumentNotAttached = errors.New("document is not attached")
)

// Client is a normal client that can communicate with the agent.
// It has documents and sends changes of the document in local
// to the agent to synchronize with other replicas in remote.
type Client struct {
	conn   *grpc.ClientConn
	client api.YorkieClient

	id           *time.ActorID
	key          string
	status       status
	attachedDocs map[string]*document.Document
}

// NewClient creates an instance of Client.
func NewClient(rpcAddr string, opts ...string) (*Client, error) {
	var k string
	if len(opts) == 0 {
		k = uuid.New().String()
	} else {
		k = opts[0]
	}

	conn, err := grpc.Dial(rpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	client := api.NewYorkieClient(conn)

	return &Client{
		conn:         conn,
		client:       client,
		key:          k,
		status:       deactivated,
		attachedDocs: make(map[string]*document.Document),
	}, nil
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

	reply, err := c.client.ActivateClient(ctx, &api.ActivateClientRequest{
		ClientKey: c.key,
	})

	if err != nil {
		log.Logger.Error(err)
		return err
	}

	c.status = activated
	c.id = time.ActorIDFromHex(reply.ClientId)

	return nil
}

// Deactivate deactivates this client.
func (c *Client) Deactivate(ctx context.Context) error {
	if c.status == deactivated {
		return nil
	}

	_, err := c.client.DeactivateClient(ctx, &api.DeactivateClientRequest{
		ClientId: c.id.String(),
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
		return errClientNotActivated
	}

	doc.SetActor(c.id)

	res, err := c.client.AttachDocument(ctx, &api.AttachDocumentRequest{
		ClientId:   c.id.String(),
		ChangePack: converter.ToChangePack(doc.CreateChangePack()),
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

	doc.UpdateState(document.Attached)
	c.attachedDocs[doc.Key().BSONKey()] = doc

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
		return errClientNotActivated
	}

	if _, ok := c.attachedDocs[doc.Key().BSONKey()]; !ok {
		return errDocumentNotAttached
	}

	res, err := c.client.DetachDocument(ctx, &api.DetachDocumentRequest{
		ClientId:   c.id.String(),
		ChangePack: converter.ToChangePack(doc.CreateChangePack()),
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

	doc.UpdateState(document.Detached)
	delete(c.attachedDocs, doc.Key().BSONKey())

	return nil
}

// Sync pushes local changes of the attached documents to the Agent and
// receives changes of the remote replica from the agent then apply them to
// local documents.
func (c *Client) Sync(ctx context.Context, keys ...*key.Key) error {
	if len(keys) == 0 {
		for _, doc := range c.attachedDocs {
			keys = append(keys, doc.Key())
		}
	}

	for _, k := range keys {
		if err := c.sync(ctx, k); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) sync(ctx context.Context, key *key.Key) error {
	if c.status != activated {
		return errClientNotActivated
	}

	doc, ok := c.attachedDocs[key.BSONKey()]
	if !ok {
		return errDocumentNotAttached
	}

	res, err := c.client.PushPull(ctx, &api.PushPullRequest{
		ClientId:   c.id.String(),
		ChangePack: converter.ToChangePack(doc.CreateChangePack()),
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

	return nil
}

type WatchResponse struct {
	Keys []*key.Key
	Err  error
}

func (c *Client) Watch(ctx context.Context, docs ...*document.Document) <-chan WatchResponse {
	var keys []*key.Key
	for _, doc := range docs {
		keys = append(keys, doc.Key())
	}

	rch := make(chan WatchResponse)
	stream, err := c.client.WatchDocuments(ctx, &api.WatchDocumentsRequest{
		ClientId:     c.id.String(),
		DocumentKeys: converter.ToDocumentKeys(keys...),
	})
	if err != nil {
		rch <- WatchResponse{Err: err}
		close(rch)
		return rch
	}

	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				rch <- WatchResponse{Err: err}
				close(rch)
				return
			}

			if resp != nil {
				rch <- WatchResponse{
					Keys: converter.FromDocumentKeys(resp.DocumentKeys),
				}
			}
		}
	}()

	return rch
}

// IsActivate returns whether this client is active or not.
func (c *Client) IsActive() bool {
	return c.status == activated
}
