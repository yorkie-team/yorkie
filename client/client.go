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
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	gotime "time"

	"connectrpc.com/connect"
	"github.com/rs/xid"
	"go.uber.org/zap"
	"golang.org/x/net/http2"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/pkg/presence"
	"github.com/yorkie-team/yorkie/server/logging"
)

type status int

const (
	statusDeactivated status = iota
	statusActivated
)

var (
	// ErrNotActivated occurs when an inactive client executes a function
	// that can only be executed when activated.
	ErrNotActivated = errors.FailedPrecond("client is not activated")

	// ErrNotAttached occurs when the given resource is not attached to this client.
	ErrNotAttached = errors.FailedPrecond("resource is not attached")

	// ErrNotDetached occurs when the given resource is not detached.
	ErrNotDetached = errors.FailedPrecond("resource is not detached")

	// ErrInvalidResource occurs when the given resource is invalid.
	ErrInvalidResource = errors.InvalidArgument("invalid resource")

	// ErrUnsupportedWatchResponseType occurs when the given WatchResponseType
	// is not supported.
	ErrUnsupportedWatchResponseType = errors.InvalidArgument("unsupported watch response type")

	// ErrInitNotReceived occurs when the first response of the watch stream is not received.
	ErrInitNotReceived = errors.Internal("initialization is not received").WithCode("ErrInitNotReceived")

	// ErrAlreadySubscribed occurs when the client is already subscribed to the document.
	ErrAlreadySubscribed = errors.AlreadyExists("already subscribed").WithCode("ErrAlreadySubscribed")
)

// Client is a normal client that can communicate with the server.
// It has documents and sends changes of the document in local
// to the server to synchronize with other replicas in remote.
type Client struct {
	conn          *http.Client
	client        v1connect.YorkieServiceClient
	options       Options
	clientOptions []connect.ClientOption

	logger      *zap.Logger
	interceptor *AuthInterceptor

	id          time.ActorID
	key         string
	status      status
	attachments *cmap.Map[key.Key, *Attachment]

	syncCtx    context.Context
	syncCancel context.CancelFunc
	syncLoopWg sync.WaitGroup
}

// WatchDocResponseType is type of watch response.
type WatchDocResponseType string

// The values below are types of WatchDocResponseType.
const (
	DocumentChanged   WatchDocResponseType = "document-changed"
	DocumentWatched   WatchDocResponseType = "document-watched"
	DocumentUnwatched WatchDocResponseType = "document-unwatched"
	PresenceChanged   WatchDocResponseType = "presence-changed"
	DocumentBroadcast WatchDocResponseType = "document-broadcast"
)

// WatchDocResponse is the response of watching document.
type WatchDocResponse struct {
	Type      WatchDocResponseType
	Presences map[string]document.PresenceData
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

	// Set default sync loop duration if not configured (50ms like JS SDK)
	if options.SyncLoopDuration == 0 {
		options.SyncLoopDuration = 50 * gotime.Millisecond
	}

	// Set default retry sync loop delay if not configured
	if options.RetrySyncLoopDelay == 0 {
		options.RetrySyncLoopDelay = 1000 * gotime.Millisecond
	}

	// Set default heartbeat interval if not configured
	if options.PresenceHeartbeatInterval == 0 {
		options.PresenceHeartbeatInterval = 30 * gotime.Second
	}

	conn := &http.Client{}
	if options.CertFile != "" {
		tlsConfig, err := newTLSConfigFromFile(options.CertFile, options.ServerNameOverride)
		if err != nil {
			return nil, fmt.Errorf("create client tls from file: %w", err)
		}

		conn.Transport = &http2.Transport{TLSClientConfig: tlsConfig}
	}

	interceptor := NewAuthInterceptor(options.APIKey, options.Token)

	var connectOpts []connect.ClientOption
	connectOpts = append(connectOpts, connect.WithInterceptors(interceptor))
	if options.MaxCallRecvMsgSize != 0 {
		connectOpts = append(connectOpts, connect.WithReadMaxBytes(options.MaxCallRecvMsgSize))
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
		clientOptions: connectOpts,
		options:       options,
		logger:        logger,
		interceptor:   interceptor,

		key:         k,
		status:      statusDeactivated,
		attachments: cmap.New[key.Key, *Attachment](),
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
		if c.options.CertFile == "" {
			rpcAddr = "http://" + rpcAddr
		} else {
			rpcAddr = "https://" + rpcAddr
		}
	}

	c.client = v1connect.NewYorkieServiceClient(c.conn, rpcAddr, c.clientOptions...)

	return nil
}

// SetToken sets the given token of this client.
func (c *Client) SetToken(token string) {
	c.interceptor.SetToken(token)
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
	if c.status == statusActivated {
		return nil
	}

	response, err := c.client.ActivateClient(
		ctx,
		withShardKey(connect.NewRequest(&api.ActivateClientRequest{
			ClientKey: c.key,
		}), c.options.APIKey, c.key))
	if err != nil {
		return err
	}

	clientID, err := time.ActorIDFromHex(response.Msg.ClientId)
	if err != nil {
		return err
	}

	c.status = statusActivated
	c.id = clientID

	c.runSyncLoop(ctx)

	return nil
}

// Deactivate deactivates this client.
func (c *Client) Deactivate(ctx context.Context, opts ...DeactivateOption) error {
	if c.status == statusDeactivated {
		return nil
	}

	deactiveOpts := &DeactivateOptions{}
	for _, opt := range opts {
		opt(deactiveOpts)
	}

	// Stop sync loop before closing watch streams
	if c.syncCancel != nil {
		c.syncCancel()
		c.syncLoopWg.Wait()
	}

	for _, attachment := range c.attachments.Values() {
		if attachment.closeWatchStream != nil {
			attachment.closeWatchStream()
		}
	}

	_, err := c.client.DeactivateClient(
		ctx,
		withShardKey(connect.NewRequest(&api.DeactivateClientRequest{
			ClientId:    c.id.String(),
			Synchronous: !deactiveOpts.Asynchronous,
		}), c.options.APIKey, c.key))
	if err != nil {
		return err
	}

	c.status = statusDeactivated

	return nil
}

// runSyncLoop runs the sync loop for all attached resources.
// It periodically checks if any resource needs synchronization and performs it.
func (c *Client) runSyncLoop(ctx context.Context) {
	c.syncCtx, c.syncCancel = context.WithCancel(ctx)
	c.syncLoopWg.Add(1)

	go func() {
		defer c.syncLoopWg.Done()

		ticker := gotime.NewTicker(c.options.SyncLoopDuration)
		defer ticker.Stop()

		for {
			select {
			case <-c.syncCtx.Done():
				return
			case <-ticker.C:
				for _, attachment := range c.attachments.Values() {
					if !attachment.needSync(c.options.PresenceHeartbeatInterval) {
						continue
					}

					if err := c.syncInternal(c.syncCtx, attachment, nil); err != nil {
						logging.DefaultLogger().Warnf("sync failed: %v", err)
						gotime.Sleep(c.options.RetrySyncLoopDelay)
					}

				}
			}
		}
	}()
}

// syncInternal performs synchronization for the given attachment based on its type.
// If syncOpts is provided, it will be used for the sync operation; otherwise,
// the attachment's sync mode will be used.
func (c *Client) syncInternal(ctx context.Context, attachment *Attachment, opts *SyncOptions) error {
	attachment.syncMu.Lock()
	defer attachment.syncMu.Unlock()

	if attachment.Is(attachable.TypeDocument) {
		d, ok := attachment.resource.(*document.Document)
		if !ok {
			return ErrInvalidResource
		}

		options := SyncOptions{
			key:  d.Key(),
			mode: types.SyncModePushPull,
		}

		// Use provided sync options if available, otherwise use attachment's sync mode
		if opts != nil {
			options = *opts
		} else if attachment.syncMode == SyncModeRealtimePushOnly {
			options.mode = types.SyncModePushOnly
		}

		if err := c.pushPullChanges(ctx, options); err != nil {
			return err
		}

		attachment.changeEventReceived = false
		return nil
	}

	p, ok := attachment.resource.(*presence.Presence)
	if !ok {
		return ErrInvalidResource
	}

	if err := c.refreshPresence(ctx, p); err != nil {
		return err
	}

	attachment.lastSyncTime = gotime.Now()

	return nil
}

// AttachResource attaches the given resource to this client.
// This is a generalized version of Attach that works with any Attachable resource.
func (c *Client) Attach(ctx context.Context, r attachable.Attachable, opts ...interface{}) error {
	if c.status != statusActivated {
		return ErrNotActivated
	}
	if r.Status() != attachable.StatusDetached {
		return ErrNotDetached
	}

	r.SetActor(c.id)

	if r.Type() == attachable.TypeDocument {
		d, ok := r.(*document.Document)
		if !ok {
			return ErrInvalidResource
		}

		attachOpts := &AttachOptions{}
		for _, opt := range opts {
			if attachOpt, ok := opt.(AttachOption); ok {
				attachOpt(attachOpts)
			}
		}

		return c.attachDocument(ctx, d, attachOpts)

	}

	p, ok := r.(*presence.Presence)
	if !ok {
		return ErrInvalidResource
	}

	return c.attachPresence(ctx, p)
}

// Detach detaches the given resource from this client.
// This is a generalized version of Detach that works with any Attachable resource.
func (c *Client) Detach(ctx context.Context, r attachable.Attachable, opts ...interface{}) error {
	if c.status != statusActivated {
		return ErrNotActivated
	}
	attachment, ok := c.attachments.Get(r.Key())
	if !ok {
		return ErrNotAttached
	}

	attachment.syncMu.Lock()
	defer attachment.syncMu.Unlock()

	if attachment.closeWatchStream != nil {
		attachment.closeWatchStream()
	}

	if attachment.Is(attachable.TypeDocument) {
		d, ok := r.(*document.Document)
		if !ok {
			return ErrInvalidResource
		}

		detachOpts := &DetachOptions{}
		for _, opt := range opts {
			if detachOpt, ok := opt.(DetachOption); ok {
				detachOpt(detachOpts)
			}
		}

		return c.detachDocument(ctx, d, detachOpts)
	}

	p, ok := r.(*presence.Presence)
	if !ok {
		return ErrInvalidResource
	}

	return c.detachPresence(ctx, p)
}

// attachDocument attaches the given document to this client. It tells the server that
// this client will synchronize the given document.
func (c *Client) attachDocument(ctx context.Context, d *document.Document, opts *AttachOptions) error {
	// 01. Initialize presence data
	if err := d.Update(func(r *json.Object, p *document.Presence) error {
		p.Initialize(opts.Presence)
		return nil
	}); err != nil {
		return err
	}
	pbChangePack, err := converter.ToChangePack(d.CreateChangePack())
	if err != nil {
		return err
	}

	// 02. Call AttachDocument rpc
	res, err := c.client.AttachDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   c.id.String(),
			ChangePack: pbChangePack,
			SchemaKey:  opts.Schema,
		}), c.options.APIKey, d.Key().String()),
	)
	if err != nil {
		return err
	}

	// 03. Apply the received change pack
	pack, err := converter.FromChangePack(res.Msg.ChangePack)
	if err != nil {
		return err
	}

	d.MaxSizeLimit = int(res.Msg.MaxSizePerDocument)
	if res.Msg.SchemaRules != nil {
		d.SchemaRules = converter.FromRules(res.Msg.SchemaRules)
	}

	if err := d.ApplyChangePack(pack); err != nil {
		return err
	}
	if c.logger.Core().Enabled(zap.DebugLevel) {
		c.logger.Debug(fmt.Sprintf(
			"after apply %d changes: %s",
			len(pack.Changes),
			d.RootObject().Marshal(),
		))
	}

	if d.Status() == attachable.StatusRemoved {
		return nil
	}
	d.SetStatus(attachable.StatusAttached)

	// 04. Start watch stream
	watchCtx, cancelFunc := context.WithCancel(ctx)

	// Set sync mode based on IsRealtime option
	syncMode := SyncModeManual
	if opts.IsRealtime {
		syncMode = SyncModeRealtime
	}

	c.attachments.Set(d.Key(), &Attachment{
		resource:            d,
		resourceID:          types.ID(res.Msg.DocumentId),
		watchCtx:            watchCtx,
		closeWatchStream:    cancelFunc,
		syncMode:            syncMode,
		changeEventReceived: false,
	})
	if opts.IsRealtime {
		if err = c.runWatchLoop(watchCtx, d); err != nil {
			return err
		}
	}

	// 05. Set initial root values if provided
	return d.Update(func(r *json.Object, p *document.Presence) error {
		for k, v := range opts.InitialRoot {
			if r.Get(k) != nil {
				continue
			}

			r.SetYSONElement(k, v)
		}

		return nil
	})
}

// detachDocument detaches the given document from this client. It tells the
// server that this client will no longer synchronize the given document.
//
// To collect garbage things like CRDT tombstones left on the document, all the
// changes should be applied to other replicas before GC time. For this, if the
// document is no longer used by this client, it should be detached.
func (c *Client) detachDocument(ctx context.Context, d *document.Document, opts *DetachOptions) error {
	attachment, ok := c.attachments.Get(d.Key())
	if !ok {
		return ErrNotAttached
	}

	if err := d.Update(func(r *json.Object, p *document.Presence) error {
		p.Clear()
		return nil
	}); err != nil {
		return err
	}

	pbChangePack, err := converter.ToChangePack(d.CreateChangePack())
	if err != nil {
		return err
	}

	res, err := c.client.DetachDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.DetachDocumentRequest{
			ClientId:            c.id.String(),
			DocumentId:          attachment.resourceID.String(),
			ChangePack:          pbChangePack,
			RemoveIfNotAttached: opts.removeIfNotAttached,
		}), c.options.APIKey, d.Key().String()))
	if err != nil {
		return err
	}

	pack, err := converter.FromChangePack(res.Msg.ChangePack)
	if err != nil {
		return err
	}

	if err := d.ApplyChangePack(pack); err != nil {
		return err
	}
	if d.Status() != document.StatusRemoved {
		d.SetStatus(document.StatusDetached)
	}
	c.attachments.Delete(d.Key())

	return nil
}

// attachPresence attaches a presence counter to the server.
func (c *Client) attachPresence(ctx context.Context, p *presence.Presence) error {
	res, err := c.client.AttachPresence(
		ctx,
		withShardKey(connect.NewRequest(&api.AttachPresenceRequest{
			ClientId:    c.id.String(),
			PresenceKey: p.Key().String(),
		}), c.options.APIKey, p.Key().String()))
	if err != nil {
		return err
	}

	p.SetStatus(attachable.StatusAttached)
	attachment := &Attachment{
		resource:     p,
		resourceID:   types.ID(res.Msg.PresenceId),
		syncMode:     SyncModeRealtime,
		lastSyncTime: gotime.Now(),
	}
	c.attachments.Set(p.Key(), attachment)

	// Update initial count from attach response
	p.UpdateCount(res.Msg.Count, 0)

	return nil
}

// refreshPresence refreshes the TTL of the given presence counter.
func (c *Client) refreshPresence(ctx context.Context, p *presence.Presence) error {
	attachment, ok := c.attachments.Get(p.Key())
	if !ok {
		return ErrNotAttached
	}

	_, err := c.client.RefreshPresence(
		ctx,
		withShardKey(connect.NewRequest(&api.RefreshPresenceRequest{
			ClientId:    c.id.String(),
			PresenceId:  attachment.resourceID.String(),
			PresenceKey: p.Key().String(),
		}), c.options.APIKey, p.Key().String()))

	return err
}

// detachPresence detaches a presence counter from the server.
func (c *Client) detachPresence(ctx context.Context, p *presence.Presence) error {
	attachment, ok := c.attachments.Get(p.Key())
	if !ok {
		return ErrNotAttached
	}

	_, err := c.client.DetachPresence(
		ctx,
		withShardKey(connect.NewRequest(&api.DetachPresenceRequest{
			ClientId:    c.id.String(),
			PresenceId:  attachment.resourceID.String(),
			PresenceKey: p.Key().String(),
		}), c.options.APIKey, p.Key().String()))
	if err != nil {
		return err
	}

	// Update counter status and reset count to 0
	p.SetStatus(attachable.StatusDetached)
	p.UpdateCount(0, 0) // Reset count and seq when detached

	c.attachments.Delete(p.Key())

	return nil
}

// WatchPresence starts watching presence count changes for the given counter.
// It returns a channel that receives count updates and a close function to stop watching.
func (c *Client) WatchPresence(ctx context.Context, p *presence.Presence) (<-chan int64, func(), error) {
	_, ok := c.attachments.Get(p.Key())
	if !ok {
		return nil, nil, ErrNotAttached
	}

	if c.status != statusActivated {
		return nil, nil, ErrNotActivated
	}

	if p.Status() != attachable.StatusAttached {
		return nil, nil, fmt.Errorf("presence counter must be attached before watching")
	}

	// Create buffered channel for count updates
	countChan := make(chan int64, 10)

	// Create context for the watch stream
	watchCtx, cancel := context.WithCancel(ctx)

	// Start the watch stream using presence_key
	stream, err := c.client.WatchPresence(
		watchCtx,
		withShardKey(connect.NewRequest(&api.WatchPresenceRequest{
			ClientId:    c.id.String(),
			PresenceKey: p.Key().String(),
		}), c.options.APIKey, p.Key().String()))
	if err != nil {
		cancel()
		return nil, nil, err
	}

	// Start goroutine to handle stream messages
	go func() {
		defer close(countChan)
		defer cancel()

		for {
			select {
			case <-watchCtx.Done():
				return
			default:
				if !stream.Receive() {
					if err := stream.Err(); err != nil {
						// Log error and return to exit the goroutine
						if c.logger != nil {
							c.logger.Error("WatchPresence stream error", zap.Error(err))
						}
						return
					}
					// Stream ended normally
					return
				}

				msg := stream.Msg()
				switch body := msg.Body.(type) {
				case *api.WatchPresenceResponse_Initialized:
					// Always accept initial count and update counter
					p.UpdateCount(body.Initialized.Count, body.Initialized.Seq)
					select {
					case countChan <- body.Initialized.Count:
					case <-watchCtx.Done():
						return
					}
				case *api.WatchPresenceResponse_Event:
					// Let Counter decide whether to accept the update based on sequence
					if body.Event != nil {
						if p.UpdateCount(body.Event.Count, body.Event.Seq) {
							// Only send to channel if Counter accepted the update
							select {
							case countChan <- body.Event.Count:
							case <-watchCtx.Done():
								return
							}
						}
						// Counter automatically drops out-of-order events
					}
				}
			}
		}
	}()

	closeFunc := func() {
		cancel()
	}

	return countChan, closeFunc, nil
}

// Sync pushes local changes of the attached documents to the server and
// receives changes of the remote replica from the server then apply them to
// local documents.
func (c *Client) Sync(ctx context.Context, opts ...SyncOptions) error {
	if len(opts) == 0 {
		for _, attachment := range c.attachments.Values() {
			opts = append(opts, WithDocKey(attachment.resource.Key()))
		}
	}

	for _, opt := range opts {
		attachment, ok := c.attachments.Get(opt.key)
		if !ok {
			return ErrNotAttached
		}

		if err := c.syncInternal(ctx, attachment, &opt); err != nil {
			return err
		}
	}

	return nil
}

// WatchStream returns a stream of watch events for testing purposes.
func (c *Client) WatchStream(
	r attachable.Attachable,
) (<-chan WatchDocResponse, context.CancelFunc, error) {
	attachment, ok := c.attachments.Get(r.Key())
	if !ok {
		return nil, nil, ErrNotAttached
	}

	return attachment.watchStream, attachment.closeWatchStream, nil
}

// runWatchLoop subscribes to events on a given documentIDs.
// If an error occurs before stream initialization, the second response, error,
// is returned. If the context "watchCtx" is canceled or timed out, returned channel
// is closed, and "WatchResponse" from this closed channel has zero events and
// nil "Err()".
func (c *Client) runWatchLoop(ctx context.Context, d *document.Document) error {
	attachment, ok := c.attachments.Get(d.Key())
	if !ok {
		return ErrNotAttached
	}

	stream, err := c.client.WatchDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.WatchDocumentRequest{
			ClientId:   c.id.String(),
			DocumentId: attachment.resourceID.String(),
		}), c.options.APIKey, d.Key().String()),
	)
	if err != nil {
		return err
	}

	// NOTE(hackerwins): We need to receive the first response to initialize
	// the watch stream. runWatchLoop should be blocked until the first response is
	// received.
	if !stream.Receive() {
		return ErrInitNotReceived
	}
	if _, err := handleResponse(stream.Msg(), d); err != nil {
		return err
	}
	if err = stream.Err(); err != nil {
		return err
	}

	rch := make(chan WatchDocResponse)
	attachment.watchStream = rch

	go func() {
		for stream.Receive() {
			pbResp := stream.Msg()
			resp, err := handleResponse(pbResp, d)
			if err != nil {
				rch <- WatchDocResponse{Err: err}
				ctx.Done()
				close(rch)
				return
			}
			if resp == nil {
				continue
			}

			// Set remote change event flag when document change is received
			if resp.Type == DocumentChanged {
				attachment, ok := c.attachments.Get(d.Key())
				if ok {
					attachment.syncMu.Lock()
					attachment.changeEventReceived = true
					attachment.syncMu.Unlock()
				}
			}

			rch <- *resp
		}
		if err = stream.Err(); err != nil {
			rch <- WatchDocResponse{Err: err}
			ctx.Done()
			close(rch)

			// If watch stream is disconnected, we re-establish the watch stream.
			err = c.runWatchLoop(ctx, d)
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
			case e := <-d.Events():
				t := PresenceChanged
				if e.Type == document.WatchedEvent {
					t = DocumentWatched
				} else if e.Type == document.UnwatchedEvent {
					t = DocumentUnwatched
				}
				rch <- WatchDocResponse{Type: t, Presences: e.Presences}
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		for {
			select {
			case r := <-d.BroadcastRequests():
				d.BroadcastResponses() <- c.broadcast(ctx, d, r.Topic, r.Payload)
			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

func handleResponse(pbResp *api.WatchDocumentResponse, d *document.Document) (*WatchDocResponse, error) {
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

		d.SetOnlineClients(clientIDs...)
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
		case events.DocChanged:
			return &WatchDocResponse{Type: DocumentChanged}, nil
		case events.DocWatched:
			d.AddOnlineClient(cli.String())

			// NOTE(hackerwins): If the presence does not exist, it means that
			// PushPull is not received before watching. In that case, the 'watched'
			// event is ignored here, and it will be triggered by PushPull.
			if d.Presence(cli.String()) == nil {
				return nil, nil
			}

			return &WatchDocResponse{
				Type: DocumentWatched,
				Presences: map[string]document.PresenceData{
					cli.String(): d.Presence(cli.String()),
				},
			}, nil
		case events.DocUnwatched:
			p := d.Presence(cli.String())
			d.RemoveOnlineClient(cli.String())

			// NOTE(hackerwins): If the presence does not exist, it means that
			// PushPull is already received before unwatching. In that case, the
			// 'unwatched' event is ignored here, and it was triggered by PushPull.
			if p == nil {
				return nil, nil
			}

			return &WatchDocResponse{
				Type: DocumentUnwatched,
				Presences: map[string]document.PresenceData{
					cli.String(): p,
				},
			}, nil
		case events.DocBroadcast:
			eventBody := resp.Event.Body
			// If the handler exists, it means that the broadcast topic has been subscribed to.
			if handler, ok := d.BroadcastEventHandlers()[eventBody.Topic]; ok && handler != nil {
				err := handler(eventBody.Topic, resp.Event.Publisher, eventBody.Payload)
				if err != nil {
					return &WatchDocResponse{
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
func (c *Client) ID() time.ActorID {
	return c.id
}

// Key returns the key of this client.
func (c *Client) Key() string {
	return c.key
}

// IsActive returns whether this client is active or not.
func (c *Client) IsActive() bool {
	return c.status == statusActivated
}

// pushPullChanges pushes the changes of the document to the server and pulls the changes from the server.
func (c *Client) pushPullChanges(ctx context.Context, opt SyncOptions) error {
	if c.status != statusActivated {
		return ErrNotActivated
	}
	attachment, ok := c.attachments.Get(opt.key)
	if !ok {
		return ErrNotAttached
	}

	d, ok := attachment.resource.(*document.Document)
	if !ok {
		return ErrInvalidResource
	}

	pbChangePack, err := converter.ToChangePack(d.CreateChangePack())
	if err != nil {
		return err
	}

	res, err := c.client.PushPullChanges(
		ctx,
		withShardKey(connect.NewRequest(&api.PushPullChangesRequest{
			ClientId:   c.id.String(),
			DocumentId: attachment.resourceID.String(),
			ChangePack: pbChangePack,
			PushOnly:   opt.mode == types.SyncModePushOnly,
		}), c.options.APIKey, opt.key.String()))
	if err != nil {
		return err
	}

	pack, err := converter.FromChangePack(res.Msg.ChangePack)
	if err != nil {
		return err
	}
	if err := d.ApplyChangePack(pack); err != nil {
		return err
	}
	if d.Status() == document.StatusRemoved {
		c.attachments.Delete(d.Key())
	}

	return nil
}

// Remove removes the given document.
func (c *Client) Remove(ctx context.Context, d *document.Document) error {
	if c.status != statusActivated {
		return ErrNotActivated
	}

	attachment, ok := c.attachments.Get(d.Key())
	if !ok {
		return ErrNotAttached
	}

	pbChangePack, err := converter.ToChangePack(d.CreateChangePack())
	if err != nil {
		return err
	}
	pbChangePack.IsRemoved = true

	res, err := c.client.RemoveDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.RemoveDocumentRequest{
			ClientId:   c.id.String(),
			DocumentId: attachment.resourceID.String(),
			ChangePack: pbChangePack,
		}), c.options.APIKey, d.Key().String()))
	if err != nil {
		return err
	}

	pack, err := converter.FromChangePack(res.Msg.ChangePack)
	if err != nil {
		return err
	}

	if err := d.ApplyChangePack(pack); err != nil {
		return err
	}
	if d.Status() == document.StatusRemoved {
		c.attachments.Delete(d.Key())
	}

	return nil
}

func (c *Client) broadcast(
	ctx context.Context,
	d *document.Document,
	topic string,
	payload []byte,
) error {
	if c.status != statusActivated {
		return ErrNotActivated
	}

	attachment, ok := c.attachments.Get(d.Key())
	if !ok {
		return ErrNotAttached
	}

	_, err := c.client.Broadcast(
		ctx,
		withShardKey(connect.NewRequest(&api.BroadcastRequest{
			ClientId:   c.id.String(),
			DocumentId: attachment.resourceID.String(),
			Topic:      topic,
			Payload:    payload,
		}), c.options.APIKey, d.Key().String()))
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
		return nil, fmt.Errorf("read TLS config file %q: %w", certFile, err)
	}
	cp := x509.NewCertPool()
	if !cp.AppendCertsFromPEM(b) {
		return nil, fmt.Errorf("failure to append certs from PEM")
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
