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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"connectrpc.com/connect"
	"github.com/rs/xid"
	"go.uber.org/zap"
	"golang.org/x/net/http2"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
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

	// ErrInitializationNotReceived occurs when the first response of the watch stream is not received.
	ErrInitializationNotReceived = errors.New("initialization is not received")

	// ErrAlreadySubscribed occurs when the client is already subscribed to the document.
	ErrAlreadySubscribed = errors.New("already subscribed")
)

// Attachment represents the document attached.
type Attachment struct {
	doc   *document.Document
	docID types.ID

	// TODO(krapie): We need to consider the case where a client opens multiple subscriptions for the same document.
	isSubscribed atomic.Bool

	rch              <-chan WatchResponse
	watchCtx         context.Context
	closeWatchStream context.CancelFunc
}

// Client is a normal client that can communicate with the server.
// It has documents and sends changes of the document in local
// to the server to synchronize with other replicas in remote.
type Client struct {
	conn          *http.Client
	client        v1connect.YorkieServiceClient
	options       Options
	clientOptions []connect.ClientOption
	logger        *zap.Logger

	id          *time.ActorID
	key         string
	status      status
	attachments map[key.Key]*Attachment
}

// WatchResponseType is type of watch response.
type WatchResponseType string

// The values below are types of WatchResponseType.
const (
	DocumentChanged   WatchResponseType = "document-changed"
	DocumentWatched   WatchResponseType = "document-watched"
	DocumentUnwatched WatchResponseType = "document-unwatched"
	PresenceChanged   WatchResponseType = "presence-changed"
	DocumentBroadcast WatchResponseType = "document-broadcast"
)

// WatchResponse is a structure representing response of Watch.
type WatchResponse struct {
	Type      WatchResponseType
	Presences map[string]innerpresence.Presence
	Err       error
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

	conn := &http.Client{}
	if options.CertFile != "" {
		tlsConfig, err := newTLSConfigFromFile(options.CertFile, options.ServerNameOverride)
		if err != nil {
			return nil, fmt.Errorf("create client tls from file: %w", err)
		}

		conn.Transport = &http2.Transport{TLSClientConfig: tlsConfig}
	}

	var clientOptions []connect.ClientOption

	clientOptions = append(clientOptions, connect.WithInterceptors(NewAuthInterceptor(options.APIKey, options.Token)))
	if options.MaxCallRecvMsgSize != 0 {
		clientOptions = append(clientOptions, connect.WithReadMaxBytes(options.MaxCallRecvMsgSize))
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
		conn:          conn,
		clientOptions: clientOptions,
		options:       options,
		logger:        logger,

		key:         k,
		status:      deactivated,
		attachments: make(map[key.Key]*Attachment),
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
	if !strings.Contains(rpcAddr, "://") {
		if c.conn.Transport == nil {
			rpcAddr = "http://" + rpcAddr
		} else {
			rpcAddr = "https://" + rpcAddr
		}
	}

	c.client = v1connect.NewYorkieServiceClient(c.conn, rpcAddr, c.clientOptions...)

	return nil
}

// Close closes all resources of this client.
func (c *Client) Close() error {
	if err := c.Deactivate(context.Background()); err != nil {
		return err
	}

	c.conn.CloseIdleConnections()

	return nil
}

// Activate activates this client. That is, it registers itself to the server
// and receives a unique ID from the server. The given ID is used to distinguish
// different clients.
func (c *Client) Activate(ctx context.Context) error {
	if c.status == activated {
		return nil
	}

	response, err := c.client.ActivateClient(
		ctx,
		withShardKey(connect.NewRequest(&api.ActivateClientRequest{
			ClientKey: c.key,
		},
		), c.options.APIKey))
	if err != nil {
		return err
	}

	clientID, err := time.ActorIDFromHex(response.Msg.ClientId)
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

	_, err := c.client.DeactivateClient(
		ctx,
		withShardKey(connect.NewRequest(&api.DeactivateClientRequest{
			ClientId: c.id.String(),
		},
		), c.options.APIKey))
	if err != nil {
		return err
	}

	c.status = deactivated

	return nil
}

// Attach attaches the given document to this client. It tells the server that
// this client will synchronize the given document.
func (c *Client) Attach(ctx context.Context, doc *document.Document, options ...AttachOption) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	if doc.Status() != document.StatusDetached {
		return ErrDocumentNotDetached
	}

	opts := &AttachOptions{}
	for _, opt := range options {
		opt(opts)
	}

	doc.SetActor(c.id)

	if err := doc.Update(func(root *json.Object, p *presence.Presence) error {
		p.Initialize(opts.Presence)
		return nil
	}); err != nil {
		return err
	}

	pbChangePack, err := converter.ToChangePack(doc.CreateChangePack())
	if err != nil {
		return err
	}

	res, err := c.client.AttachDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   c.id.String(),
			ChangePack: pbChangePack,
		},
		), c.options.APIKey, doc.Key().String()))
	if err != nil {
		return err
	}

	pack, err := converter.FromChangePack(res.Msg.ChangePack)
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
		docID: types.ID(res.Msg.DocumentId),
	}

	watchCtx, cancelFunc := context.WithCancel(ctx)
	c.attachments[doc.Key()].watchCtx = watchCtx
	c.attachments[doc.Key()].closeWatchStream = cancelFunc

	if !opts.IsManual {
		err = c.runWatchLoop(watchCtx, doc)
		if err != nil {
			return err
		}
	}

	return nil
}

// Detach detaches the given document from this client. It tells the
// server that this client will no longer synchronize the given document.
//
// To collect garbage things like CRDT tombstones left on the document, all the
// changes should be applied to other replicas before GC time. For this, if the
// document is no longer used by this client, it should be detached.
func (c *Client) Detach(ctx context.Context, doc *document.Document, options ...DetachOption) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	opts := &DetachOptions{}
	for _, opt := range options {
		opt(opts)
	}

	attachment, ok := c.attachments[doc.Key()]
	if !ok {
		return ErrDocumentNotAttached
	}

	attachment.closeWatchStream()

	if err := doc.Update(func(root *json.Object, p *presence.Presence) error {
		p.Clear()
		return nil
	}); err != nil {
		return err
	}

	pbChangePack, err := converter.ToChangePack(doc.CreateChangePack())
	if err != nil {
		return err
	}

	res, err := c.client.DetachDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.DetachDocumentRequest{
			ClientId:            c.id.String(),
			DocumentId:          attachment.docID.String(),
			ChangePack:          pbChangePack,
			RemoveIfNotAttached: opts.removeIfNotAttached,
		},
		), c.options.APIKey, doc.Key().String()))
	if err != nil {
		return err
	}

	pack, err := converter.FromChangePack(res.Msg.ChangePack)
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

// Sync pushes local changes of the attached documents to the server and
// receives changes of the remote replica from the server then apply them to
// local documents.
func (c *Client) Sync(ctx context.Context, options ...SyncOptions) error {
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

// Subscribe subscribes to events on a given document.
func (c *Client) Subscribe(
	doc *document.Document,
) (<-chan WatchResponse, context.CancelFunc, error) {
	attachment, ok := c.attachments[doc.Key()]
	if !ok {
		return nil, nil, ErrDocumentNotAttached
	}

	if !attachment.isSubscribed.CompareAndSwap(false, true) {
		return nil, nil, ErrAlreadySubscribed
	}

	return attachment.rch, attachment.closeWatchStream, nil
}

// Watch watches events on a given document. It is not necessary to be called
// outside of this package, but it is exposed for testing purposes.
func (c *Client) Watch(ctx context.Context, doc *document.Document) (
	*connect.ServerStreamForClient[api.WatchDocumentResponse],
	error,
) {
	attachment, ok := c.attachments[doc.Key()]
	if !ok {
		return nil, ErrDocumentNotAttached
	}

	return c.client.WatchDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.WatchDocumentRequest{
			ClientId:   c.id.String(),
			DocumentId: attachment.docID.String(),
		}), c.options.APIKey, doc.Key().String()),
	)
}

// runWatchLoop subscribes to events on a given documentIDs.
// If an error occurs before stream initialization, the second response, error,
// is returned. If the context "watchCtx" is canceled or timed out, returned channel
// is closed, and "WatchResponse" from this closed channel has zero events and
// nil "Err()".
func (c *Client) runWatchLoop(
	ctx context.Context,
	doc *document.Document,
) error {
	attachment, ok := c.attachments[doc.Key()]
	if !ok {
		return ErrDocumentNotAttached
	}

	stream, err := c.Watch(ctx, doc)
	if err != nil {
		return err
	}

	// NOTE(hackerwins): We need to receive the first response to initialize
	// the watch stream. runWatchLoop should be blocked until the first response is
	// received.
	if !stream.Receive() {
		return ErrInitializationNotReceived
	}
	if _, err := handleResponse(stream.Msg(), doc); err != nil {
		return err
	}
	if err = stream.Err(); err != nil {
		return err
	}

	rch := make(chan WatchResponse)
	attachment.rch = rch

	go func() {
		for stream.Receive() {
			pbResp := stream.Msg()
			resp, err := handleResponse(pbResp, doc)
			if err != nil {
				rch <- WatchResponse{Err: err}
				ctx.Done()
				close(rch)
				return
			}
			if resp == nil || !attachment.isSubscribed.Load() {
				continue
			}

			rch <- *resp
		}
		if err = stream.Err(); err != nil {
			attachment.isSubscribed.Store(false)
			rch <- WatchResponse{Err: err}
			ctx.Done()
			close(rch)

			// If watch stream is disconnected, we re-establish the watch stream.
			err = c.runWatchLoop(ctx, doc)
			if err != nil {
				return
			}
			return
		}
	}()

	// TODO(hackerwins): We need to revise the implementation of the watch
	// event handling. Currently, we are using the same channel for both
	// document events and watch events. This is not ideal because the
	// client cannot distinguish between document events and watch events.
	// We'll expose only document events and watch events will be handled
	// internally.

	// TODO(hackerwins): We should ensure that the goroutine is closed when
	// the stream is closed.
	go func() {
		for {
			select {
			case e := <-doc.Events():
				t := PresenceChanged
				if e.Type == document.WatchedEvent {
					t = DocumentWatched
				} else if e.Type == document.UnwatchedEvent {
					t = DocumentUnwatched
				}
				rch <- WatchResponse{Type: t, Presences: e.Presences}
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		for {
			select {
			case r := <-doc.BroadcastRequests():
				doc.BroadcastResponses() <- c.broadcast(ctx, doc, r.Topic, r.Payload)
			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

func handleResponse(
	pbResp *api.WatchDocumentResponse,
	doc *document.Document,
) (*WatchResponse, error) {
	switch resp := pbResp.Body.(type) {
	case *api.WatchDocumentResponse_Initialization_:
		var clientIDs []string
		for _, clientID := range resp.Initialization.ClientIds {
			id, err := time.ActorIDFromHex(clientID)
			if err != nil {
				return nil, err
			}
			clientIDs = append(clientIDs, id.String())
		}

		doc.SetOnlineClients(clientIDs...)
		return nil, nil
	case *api.WatchDocumentResponse_Event:
		eventType, err := converter.FromEventType(resp.Event.Type)
		if err != nil {
			return nil, err
		}

		cli, err := time.ActorIDFromHex(resp.Event.Publisher)
		if err != nil {
			return nil, err
		}

		switch eventType {
		case types.DocumentChangedEvent:
			return &WatchResponse{Type: DocumentChanged}, nil
		case types.DocumentWatchedEvent:
			doc.AddOnlineClient(cli.String())
			if doc.Presence(cli.String()) == nil {
				return nil, nil
			}

			return &WatchResponse{
				Type: DocumentWatched,
				Presences: map[string]innerpresence.Presence{
					cli.String(): doc.Presence(cli.String()),
				},
			}, nil
		case types.DocumentUnwatchedEvent:
			p := doc.Presence(cli.String())
			doc.RemoveOnlineClient(cli.String())
			if p == nil {
				return nil, nil
			}

			return &WatchResponse{
				Type: DocumentUnwatched,
				Presences: map[string]innerpresence.Presence{
					cli.String(): p,
				},
			}, nil
		case types.DocumentBroadcastEvent:
			eventBody := resp.Event.Body
			// If the handler exists, it means that the broadcast topic has been subscribed to.
			if handler, ok := doc.BroadcastEventHandlers()[eventBody.Topic]; ok && handler != nil {
				err := handler(eventBody.Topic, resp.Event.Publisher, eventBody.Payload)
				if err != nil {
					return &WatchResponse{
						Type: DocumentBroadcast,
						Err:  err,
					}, nil
				}
			}
			return nil, nil
		}
	}
	return nil, ErrUnsupportedWatchResponseType
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

// pushPullChanges pushes the changes of the document to the server and pulls the changes from the server.
func (c *Client) pushPullChanges(ctx context.Context, opt SyncOptions) error {
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
		ctx,
		withShardKey(connect.NewRequest(&api.PushPullChangesRequest{
			ClientId:   c.id.String(),
			DocumentId: attachment.docID.String(),
			ChangePack: pbChangePack,
			PushOnly:   opt.mode == types.SyncModePushOnly,
		},
		), c.options.APIKey, opt.key.String()))
	if err != nil {
		return err
	}

	pack, err := converter.FromChangePack(res.Msg.ChangePack)
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
		ctx,
		withShardKey(connect.NewRequest(&api.RemoveDocumentRequest{
			ClientId:   c.id.String(),
			DocumentId: attachment.docID.String(),
			ChangePack: pbChangePack,
		},
		), c.options.APIKey, doc.Key().String()))
	if err != nil {
		return err
	}

	pack, err := converter.FromChangePack(res.Msg.ChangePack)
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

func (c *Client) broadcast(ctx context.Context, doc *document.Document, topic string, payload []byte) error {
	if c.status != activated {
		return ErrClientNotActivated
	}

	attachment, ok := c.attachments[doc.Key()]
	if !ok {
		return ErrDocumentNotAttached
	}

	_, err := c.client.Broadcast(
		ctx,
		withShardKey(connect.NewRequest(&api.BroadcastRequest{
			ClientId:   c.id.String(),
			DocumentId: attachment.docID.String(),
			Topic:      topic,
			Payload:    payload,
		},
		), c.options.APIKey, doc.Key().String()))
	if err != nil {
		return err
	}

	return nil
}

/**
* newTLSConfigFromFile returns a new tls.Config from the given certFile.
 */
func newTLSConfigFromFile(certFile, serverNameOverride string) (*tls.Config, error) {
	b, err := os.ReadFile(filepath.Clean(certFile))
	if err != nil {
		return nil, fmt.Errorf("credentials: failed to read TLS config file %q: %w", certFile, err)
	}
	cp := x509.NewCertPool()
	if !cp.AppendCertsFromPEM(b) {
		return nil, fmt.Errorf("credentials: failed to append certificates")
	}

	return &tls.Config{ServerName: serverNameOverride, RootCAs: cp, MinVersion: tls.VersionTLS12}, nil
}

/**
* withShardKey returns a context with the given shard key in metadata.
 */
func withShardKey[T any](conn *connect.Request[T], keys ...string) *connect.Request[T] {
	conn.Header().Add(types.ShardKey, strings.Join(keys, "/"))

	return conn
}
