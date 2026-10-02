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
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/pkg/key"
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

	// ErrAlreadyAttached occurs when a resource with the same key is already
	// attached to, or being attached by, this client.
	ErrAlreadyAttached = errors.FailedPrecond("resource with the key is already attached").
				WithCode("ErrAlreadyAttached")

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

	// attaching holds the keys of documents with an attach in flight.
	// attachments is only set once the attach round trip resolves, so this
	// is what rejects a concurrent attach of the same key.
	attachingMu sync.Mutex
	attaching   map[key.Key]struct{}

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
	if options.ChannelHeartbeatInterval == 0 {
		options.ChannelHeartbeatInterval = 30 * gotime.Second
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
		attaching:   make(map[key.Key]struct{}),
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

	// The sync loop is already stopped above, so the pipelines have no
	// ApplyChangePack left to serve; the teardown still has to drain the
	// stream readers before retiring each pump.
	for _, attachment := range c.attachments.Values() {
		stopWatchPipeline(attachment)
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

	// The server detached every resource of this client, so mark them
	// detached here too, as the JS SDK does, and drop their attachments. The
	// attachment holds the resource ID of the session the server just ended,
	// so keeping it would let the sync loop -- which reads c.attachments and
	// ignores resource status -- push it again the moment this client is
	// activated anew, and would let Detach, Remove or WatchStream address a
	// server-side attachment this client no longer holds. Dropping it leaves
	// the resource exactly where a plain Detach leaves it: detached, with no
	// attachment, free to be attached again.
	for _, attachment := range c.attachments.Values() {
		if attachment.resource.Status() != attachable.StatusRemoved {
			attachment.resource.SetStatus(attachable.StatusDetached)
		}
		if ch, ok := attachment.resource.(*channel.Channel); ok {
			ch.UpdateSessionCount(0, 0)
		}
		c.attachments.Delete(attachment.resource.Key())
	}

	c.status = statusDeactivated

	return nil
}

// runSyncLoop runs the sync loop for all attached resources.
// It periodically checks if any resource needs synchronization and performs it.
func (c *Client) runSyncLoop(ctx context.Context) {
	c.syncCtx, c.syncCancel = context.WithCancel(ctx)

	c.syncLoopWg.Go(func() {
		ticker := gotime.NewTicker(c.options.SyncLoopDuration)
		defer ticker.Stop()

		for {
			select {
			case <-c.syncCtx.Done():
				return
			case <-ticker.C:
				for _, attachment := range c.attachments.Values() {
					if !attachment.needSync(c.options.ChannelHeartbeatInterval) {
						continue
					}

					if err := c.syncInternal(c.syncCtx, attachment, nil); err != nil {
						logging.DefaultLogger().Warnf("sync failed: %v", err)
						gotime.Sleep(c.options.RetrySyncLoopDelay)
					}

				}
			}
		}
	})
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

		// Cleared before the push, not after it: the stream reader sets the
		// flag without taking syncMu, so clearing afterwards would swallow a
		// change event that landed while the push was in flight. Restored on
		// failure so a pending remote change still forces the next sync.
		pending := attachment.changeEventReceived.Swap(false)
		if err := c.pushPullChanges(ctx, options); err != nil {
			if pending {
				attachment.changeEventReceived.Store(true)
			}
			return err
		}

		return nil
	}

	p, ok := attachment.resource.(*channel.Channel)
	if !ok {
		return ErrInvalidResource
	}

	if err := c.refreshChannel(ctx, p); err != nil {
		return err
	}

	attachment.lastSyncTime = gotime.Now()

	return nil
}

// AttachResource attaches the given resource to this client.
// This is a generalized version of Attach that works with any Attachable resource.
func (c *Client) Attach(ctx context.Context, r attachable.Attachable, opts ...any) error {
	if c.status != statusActivated {
		return ErrNotActivated
	}
	if r.Status() != attachable.StatusDetached {
		return ErrNotDetached
	}

	// Reject a second resource with the same key before any RPC or any change
	// to it, as the JS SDK does. attachments is keyed by key.Key alone and
	// shared by every resource type, so a second attach would otherwise
	// replace the first entry and orphan the resource behind it, whatever the
	// two types are. The server cannot tell a concurrent attach of the same
	// key apart while the first is in flight.
	if err := c.beginAttach(r.Key()); err != nil {
		return err
	}
	defer c.endAttach(r.Key())

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

	p, ok := r.(*channel.Channel)
	if !ok {
		return ErrInvalidResource
	}

	attachChannelOpts := &AttachChannelOptions{}
	for _, opt := range opts {
		if attachOpt, ok := opt.(AttachChannelOption); ok {
			attachOpt(attachChannelOpts)
		}
	}

	return c.attachChannel(ctx, p, attachChannelOpts)
}

// Detach detaches the given resource from this client.
// This is a generalized version of Detach that works with any Attachable resource.
func (c *Client) Detach(ctx context.Context, r attachable.Attachable, opts ...any) error {
	if c.status != statusActivated {
		return ErrNotActivated
	}
	// The attachment is looked up by key, so it must also be held by r
	// itself. Another resource with the same key, one rejected by the attach
	// guard or a stale one from before a deactivation, would otherwise send
	// the attached resource's ID and detach it on the server.
	attachment, ok := c.attachments.Get(r.Key())
	if !ok || attachment.resource != r {
		return ErrNotAttached
	}

	attachment.syncMu.Lock()
	defer attachment.syncMu.Unlock()

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

		if err := c.detachDocument(ctx, d, detachOpts); err != nil {
			return err
		}
	} else {
		p, ok := r.(*channel.Channel)
		if !ok {
			return ErrInvalidResource
		}

		if err := c.detachChannel(ctx, p); err != nil {
			return err
		}
	}

	// Keep the watch pipeline alive while applying the final ChangePack. Its
	// event pump is the sole consumer of Document.Events, so stopping it first
	// can leave ApplyChangePack blocked when the pack emits multiple events.
	// syncMu is still held here, which is what keeps a concurrent sync from
	// applying a pack into a pipeline that is being dismantled.
	stopWatchPipeline(attachment)

	return nil
}

// beginAttach marks the key k as being attached. It fails with
// ErrAlreadyAttached when a resource with k is already attached to, or being
// attached by, this client.
func (c *Client) beginAttach(k key.Key) error {
	c.attachingMu.Lock()
	defer c.attachingMu.Unlock()

	if _, ok := c.attaching[k]; ok {
		return fmt.Errorf("attach %s: %w", k, ErrAlreadyAttached)
	}
	if attachment, ok := c.attachments.Get(k); ok {
		// An attachment whose resource is no longer attached -- one left by a
		// path that detached or removed the resource without clearing the
		// entry -- is stale, so drop it and let the key be used again.
		if attachment.resource.Status() == attachable.StatusAttached {
			return fmt.Errorf("attach %s: %w", k, ErrAlreadyAttached)
		}
		c.attachments.Delete(k)
	}

	c.attaching[k] = struct{}{}
	return nil
}

// endAttach clears the in-flight mark that beginAttach set for k.
func (c *Client) endAttach(k key.Key) {
	c.attachingMu.Lock()
	defer c.attachingMu.Unlock()

	delete(c.attaching, k)
}

// attachDocument attaches the given document to this client. It tells the server that
// this client will synchronize the given document.
func (c *Client) attachDocument(ctx context.Context, d *document.Document, opts *AttachOptions) error {
	// 01. Initialize presence data. Skip when the caller declared the
	// document presenceless so we never produce an initial PUT change for
	// a doc that will reject presence on the wire anyway.
	if !opts.DisablePresence {
		if err := d.Update(func(r *json.Object, p *document.Presence) error {
			p.Initialize(opts.Presence)
			return nil
		}); err != nil {
			return err
		}
	}
	pbChangePack, err := converter.ToChangePack(d.CreateChangePack())
	if err != nil {
		return err
	}

	// 02. Call AttachDocument rpc
	res, err := c.client.AttachDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:        c.id.String(),
			ChangePack:      pbChangePack,
			SchemaKey:       opts.Schema,
			DisableGc:       opts.DisableGC,
			DisablePresence: opts.DisablePresence,
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

	// Through the setters, not the exported fields: Update reads both under
	// the document's lock, so writing them unguarded from this goroutine
	// races every concurrent updater.
	d.SetMaxSizeLimit(int(res.Msg.MaxSizePerDocument))
	if res.Msg.SchemaRules != nil {
		d.SetSchemaRules(converter.FromRules(res.Msg.SchemaRules))
	}

	// Record the opt-out decisions before applying the attach response so the
	// first ApplyChangePack already routes remote changes through the
	// lamport-only sync path (DisableGC) and so Update silently drops
	// presence (DisablePresence). The DisablePresence value is taken from
	// the server-fixated response — a late attacher to a presenceless doc
	// observes the persisted true even without WithDisablePresence locally.
	d.SetDisableGC(opts.DisableGC)
	d.SetDisablePresence(res.Msg.DisablePresence)

	// If the server reported the doc as presenceless but the local client
	// did not opt in (so Step 01 produced an initial empty PUT), reset the
	// local presence map. The wire-side PUT was stripped by the server and
	// never crosses the boundary, but the local InternalDocument still
	// carries the cloned entry until we clear it here.
	// Through Document.ResetPresences, not the internal document's: the
	// latter replaces the presence maps with no lock held, racing every
	// presence reader on the document.
	if res.Msg.DisablePresence && !opts.DisablePresence {
		d.ResetPresences()
	}

	if err := d.ApplyChangePack(pack); err != nil {
		return err
	}
	if c.logger.Core().Enabled(zap.DebugLevel) {
		c.logger.Debug(fmt.Sprintf(
			"after apply %d changes: %s",
			len(pack.Changes),
			// Marshal, not RootObject().Marshal(): the former does the whole
			// traversal under the document lock, while the latter walks the
			// live CRDT root after the lock is released.
			d.Marshal(),
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

	attachment := &Attachment{
		resource:         d,
		resourceID:       types.ID(res.Msg.DocumentId),
		watchCtx:         watchCtx,
		closeWatchStream: cancelFunc,
		syncMode:         syncMode,
		disableGC:        opts.DisableGC,
		disablePresence:  res.Msg.DisablePresence,
	}
	if opts.IsRealtime {
		// Start the delivery pipeline before the attachment is published and
		// before the watch stream is opened: the first Receive blocks on the
		// server, and a publisher stalled on the document's event channel in
		// the meantime holds the event mutex against every other publisher.
		startWatchPipeline(watchCtx, attachment, d)
	}
	c.attachments.Set(d.Key(), attachment)
	if opts.IsRealtime {
		if err = c.runWatchLoop(watchCtx, d); err != nil {
			// Roll the half-established attachment back. Leaving it registered
			// keeps a realtime attachment that has no watch stream and whose
			// watchCtx is never cancelled, so its pipeline would outlive the
			// failed Attach with nothing to feed it. The server may still hold
			// the attachment; the document goes back to detached so the caller
			// can retry Attach rather than being stuck with an unusable one.
			c.attachments.Delete(d.Key())
			stopWatchPipeline(attachment)
			d.SetStatus(attachable.StatusDetached)
			return err
		}
	}

	// 05. Set initial root values if provided
	if err := d.Update(func(r *json.Object, p *document.Presence) error {
		for k, v := range opts.InitialRoot {
			if r.Get(k) != nil {
				continue
			}

			r.SetYSONElement(k, v)
		}

		return nil
	}); err != nil {
		return err
	}

	// 06. Clear the undo/redo stacks so that pre-attach changes, including
	// the initial root setup above, are not reachable via undo.
	//
	// By this point SetStatus(StatusAttached) has run, the attachment is
	// registered in c.attachments, and (for a realtime doc) runWatchLoop is
	// already running. ClearHistory can now return ErrRefusedDuringUpdate,
	// so a concurrent doc.Update landing at this instant makes Attach
	// return an error while leaving a live, attached document with a
	// running watch stream. The window needs a concurrent Update during
	// Attach on a not-yet-shared document, so it is narrow -- and step 05's
	// d.Update above already has this same partial-attach property, so this
	// widens an existing hole rather than opening a new one.
	if err := d.ClearHistory(); err != nil {
		return err
	}

	return nil
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
	if d.Status() != document.StatusRemoved {
		d.SetStatus(document.StatusDetached)
	}
	c.attachments.Delete(d.Key())

	return nil
}

// attachChannel attaches a channel to the server.
func (c *Client) attachChannel(ctx context.Context, ch *channel.Channel, opts *AttachChannelOptions) error {
	res, err := c.client.AttachChannel(
		ctx,
		withShardKey(connect.NewRequest(&api.AttachChannelRequest{
			ClientId:   c.id.String(),
			ChannelKey: ch.Key().String(),
		}), c.options.APIKey, ch.FirstKeyPath()))
	if err != nil {
		return err
	}

	ch.SetStatus(attachable.StatusAttached)

	syncMode := SyncModeManual
	if opts.IsRealtime {
		syncMode = SyncModeRealtime
	}

	attachment := &Attachment{
		resource:     ch,
		resourceID:   types.ID(res.Msg.SessionId),
		syncMode:     syncMode,
		lastSyncTime: gotime.Now(),
	}
	c.attachments.Set(ch.Key(), attachment)

	// Update initial session count from attach response
	ch.UpdateSessionCount(res.Msg.SessionCount, 0)

	return nil
}

// refreshChannel refreshes the TTL of the given channel and returns the current session count.
func (c *Client) refreshChannel(ctx context.Context, ch *channel.Channel) error {
	attachment, ok := c.attachments.Get(ch.Key())
	if !ok {
		return ErrNotAttached
	}

	res, err := c.client.RefreshChannel(
		ctx,
		withShardKey(connect.NewRequest(&api.RefreshChannelRequest{
			ClientId:   c.id.String(),
			ChannelKey: ch.Key().String(),
			SessionId:  attachment.resourceID.String(),
		}), c.options.APIKey, ch.FirstKeyPath()))

	if err != nil {
		return err
	}

	// Update session count from refresh response
	ch.UpdateSessionCount(res.Msg.SessionCount, 0)

	return nil
}

// detachChannel detaches a channel from the server.
func (c *Client) detachChannel(ctx context.Context, ch *channel.Channel) error {
	attachment, ok := c.attachments.Get(ch.Key())
	if !ok {
		return ErrNotAttached
	}

	_, err := c.client.DetachChannel(
		ctx,
		withShardKey(connect.NewRequest(&api.DetachChannelRequest{
			ClientId:   c.id.String(),
			ChannelKey: ch.Key().String(),
			SessionId:  attachment.resourceID.String(),
		}), c.options.APIKey, ch.FirstKeyPath()))
	if err != nil {
		return err
	}

	// Update counter status and reset count to 0
	ch.SetStatus(attachable.StatusDetached)
	ch.UpdateSessionCount(0, 0) // Reset session count and seq when detached

	c.attachments.Delete(ch.Key())

	return nil
}

// WatchChannel starts watching channel count changes for the given counter.
// It returns a channel that receives count updates and a close function to stop watching.
func (c *Client) WatchChannel(ctx context.Context, ch *channel.Channel) (<-chan int64, func(), error) {
	_, ok := c.attachments.Get(ch.Key())
	if !ok {
		return nil, nil, ErrNotAttached
	}

	if c.status != statusActivated {
		return nil, nil, ErrNotActivated
	}

	if ch.Status() != attachable.StatusAttached {
		return nil, nil, fmt.Errorf("channel must be attached before watching")
	}

	// Create buffered channel for count updates
	countChan := make(chan int64, 10)

	// Create context for the watch stream
	watchCtx, cancel := context.WithCancel(ctx)

	// Start the watch stream using unified Watch RPC
	stream, err := c.openChannelWatch(watchCtx, ch)
	if err != nil {
		cancel()
		return nil, nil, err
	}

	// Start goroutine to handle stream messages
	go func() {
		defer close(countChan)
		defer cancel()

		for {
			// A stream the application did not close ended on the server's
			// terms: the connection dropped, or the subscription behind it
			// pruned itself. Neither means the watch is over, so re-establish
			// it — without a new stream the channel stops delivering
			// broadcasts and session counts for the rest of its life.
			if !c.pumpChannelWatch(watchCtx, ch, stream, countChan) {
				return
			}
			if watchCtx.Err() != nil {
				return
			}

			// Re-establishing only follows a stream that delivered something,
			// so a server that ends every stream at once cannot spin this
			// loop.
			stream, err = c.openChannelWatch(watchCtx, ch)
			if err != nil {
				if c.logger != nil {
					c.logger.Error("WatchChannel re-establish failed", zap.Error(err))
				}
				return
			}
		}
	}()

	closeFunc := func() {
		cancel()
	}

	// Start goroutine to handle broadcast requests
	go func() {
		for {
			select {
			case r := <-ch.BroadcastRequests():
				ch.BroadcastResponses() <- c.broadcast(ctx, ch, r.Topic, r.Payload)
			case <-watchCtx.Done():
				return
			}
		}
	}()

	return countChan, closeFunc, nil
}

// openChannelWatch opens a Watch stream carrying the given channel.
func (c *Client) openChannelWatch(
	ctx context.Context,
	ch *channel.Channel,
) (*connect.ServerStreamForClient[api.WatchResponse], error) {
	return c.client.Watch(
		ctx,
		withShardKey(connect.NewRequest(&api.WatchRequest{
			ClientId: c.id.String(),
			Resources: []*api.ResourceDescriptor{{
				Resource: &api.ResourceDescriptor_Channel{
					Channel: &api.ChannelDescriptor{
						ChannelKey: ch.Key().String(),
					},
				},
			}},
		}), c.options.APIKey, ch.FirstKeyPath()))
}

// pumpChannelWatch delivers the responses of one channel watch stream until
// the stream ends or the watch is canceled. It reports whether the stream
// delivered at least one response, which is what tells the caller the server
// still accepts this watch and re-establishing it is worth attempting.
func (c *Client) pumpChannelWatch(
	ctx context.Context,
	ch *channel.Channel,
	stream *connect.ServerStreamForClient[api.WatchResponse],
	countChan chan<- int64,
) bool {
	delivered := false

	for {
		select {
		case <-ctx.Done():
			return false
		default:
			if !stream.Receive() {
				if err := stream.Err(); err != nil && c.logger != nil {
					c.logger.Error("WatchChannel stream error", zap.Error(err))
				}
				return delivered
			}
			delivered = true

			msg := stream.Msg()
			switch body := msg.Body.(type) {
			case *api.WatchResponse_Initialization:
				for _, init := range body.Initialization.ResourceInits {
					if ci, ok := init.Init.(*api.ResourceInit_ChannelInit); ok {
						ch.UpdateSessionCount(ci.ChannelInit.SessionCount, ci.ChannelInit.Seq)
						select {
						case countChan <- ci.ChannelInit.SessionCount:
						case <-ctx.Done():
							return false
						}
					}
				}
			case *api.WatchResponse_Event:
				if ce, ok := body.Event.Event.(*api.WatchEvent_ChannelEvent); ok {
					event := ce.ChannelEvent.Event
					if event != nil {
						// Handle broadcast events
						if event.Type == api.ChannelEvent_TYPE_BROADCAST {
							if handler, ok := ch.BroadcastEventHandlers()[event.Topic]; ok && handler != nil {
								_ = handler(event.Topic, event.Publisher, event.Payload)
							}
						} else if ch.UpdateSessionCount(event.SessionCount, event.Seq) {
							select {
							case countChan <- event.SessionCount:
							case <-ctx.Done():
								return false
							}
						}
					}
				}
			}
		}
	}
}

// Sync pushes local changes of the attached documents to the server and
// receives changes of the remote replica from the server then apply them to
// local documents. For channels, it refreshes the TTL and retrieves
// the current count.
func (c *Client) Sync(ctx context.Context, opts ...SyncOptions) error {
	if len(opts) == 0 {
		for _, attachment := range c.attachments.Values() {
			opts = append(opts, WithKey(attachment.resource.Key()))
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

// WatchStream returns a stream of watch events for testing purposes. The
// channel belongs to the attachment and is stable: a watch loop that
// re-establishes its stream keeps delivering on the same channel, so a
// consumer holding it does not have to re-read this field after a
// disconnect. It is closed once the stream ends for good.
func (c *Client) WatchStream(
	r attachable.Attachable,
) (<-chan WatchDocResponse, context.CancelFunc, error) {
	// Held by r itself, not merely keyed by r.Key(): a resource rejected by
	// the attach guard, or a stale one from before a deactivation, would
	// otherwise observe another resource's stream.
	attachment, ok := c.attachments.Get(r.Key())
	if !ok || attachment.resource != r {
		return nil, nil, ErrNotAttached
	}

	return attachment.watchStream, attachment.closeWatchStream, nil
}

// startWatchPipeline starts the delivery pipeline of the given attachment.
// It has three goroutines with a single direction of backpressure:
//
//	stream reader ─┐
//	               ├─> watchBuf ─> sender ─> watchStream
//	pump ──────────┘
//
// The pump and the sender are owned by the attachment, not by a single
// runWatchLoop invocation. The pump is the sole consumer of the document event
// channel and only appends to the unbounded buffer, so producers emitting
// document events under the document's event mutex are never blocked by a slow
// application reading the stream, nor by a watch loop re-establishing its
// stream after a disconnect. The sender is the sole writer and closer of the
// response channel.
//
// The sender stops with ctx -- the attachment's watchCtx -- but the pump stops
// only when stopWatchPipeline closes watchPumpStop, which it does after every
// producer has exited. Document.publish is an unconditional send on a
// capacity-one channel held under the document's event mutex, so a pump that
// left while a producer was still running would wedge that producer, and with
// it every other publisher, for good.
func startWatchPipeline(ctx context.Context, attachment *Attachment, d *document.Document) {
	buf := newWatchBuffer()
	rch := make(chan WatchDocResponse)
	pumpStop := make(chan struct{})
	pumpDone := make(chan struct{})
	attachment.watchBuf = buf
	attachment.watchStream = rch
	attachment.watchPumpStop = pumpStop
	attachment.watchPumpDone = pumpDone

	// pump: document events -> buf.
	go func() {
		defer close(pumpDone)
		for {
			select {
			case e := <-d.Events():
				t := PresenceChanged
				switch e.Type {
				case document.WatchedEvent:
					t = DocumentWatched
				case document.UnwatchedEvent:
					t = DocumentUnwatched
				}
				buf.push(WatchDocResponse{Type: t, Presences: e.Presences})
			case <-pumpStop:
				return
			}
		}
	}()

	// sender: buf -> rch.
	go func() {
		defer close(rch)
		for {
			resp, ok := buf.pop(ctx)
			if !ok {
				return
			}
			select {
			case rch <- resp:
			case <-ctx.Done():
				return
			}
		}
	}()
}

// stopWatchPipeline tears the attachment's delivery pipeline down and returns
// once no goroutine is left consuming or producing the document's events.
//
// The order is producers first, pump last. Cancelling watchCtx does not stop a
// stream reader instantly: it may be inside handleWatchResponse, publishing a
// presence reconciliation onto the document's capacity-one event channel under
// the document's event mutex. That send has no cancellation path, so the pump
// has to stay until every reader has returned -- otherwise the reader blocks
// forever and takes every other publisher, the sync goroutine's
// ApplyChangePack included, down with it.
//
// Callers must not hold attachment.syncMu on behalf of another goroutine that
// a reader waits for; the readers themselves take no client lock, which is why
// Detach, Remove and pushPullChanges can call this under syncMu.
func stopWatchPipeline(attachment *Attachment) {
	attachment.watchStopOnce.Do(func() {
		if attachment.closeWatchStream != nil {
			attachment.closeWatchStream()
		}
		attachment.watchReaders.Wait()
		if attachment.watchPumpStop != nil {
			close(attachment.watchPumpStop)
		}
		if attachment.watchPumpDone != nil {
			<-attachment.watchPumpDone
		}
	})
}

// runWatchLoop subscribes to events on a given document using the unified Watch RPC.
// If an error occurs before stream initialization, the error is returned and the
// attachment's delivery pipeline is left untouched: it is owned by the attachment
// and keeps draining the document until the watch context is cancelled. If the
// context "watchCtx" is canceled or timed out, the response channel is closed, and
// "WatchResponse" from this closed channel has zero events and nil "Err()".
func (c *Client) runWatchLoop(ctx context.Context, d *document.Document) error {
	attachment, ok := c.attachments.Get(d.Key())
	if !ok {
		return ErrNotAttached
	}
	buf := attachment.watchBuf
	if buf == nil {
		return ErrNotAttached
	}

	stream, err := c.client.Watch(
		ctx,
		withShardKey(connect.NewRequest(&api.WatchRequest{
			ClientId: c.id.String(),
			Resources: []*api.ResourceDescriptor{{
				Resource: &api.ResourceDescriptor_Document{
					Document: &api.DocumentDescriptor{
						DocumentId: attachment.resourceID.String(),
					},
				},
			}},
		}), c.options.APIKey, d.Key().String()),
	)
	if err != nil {
		return err
	}

	// NOTE(hackerwins): We need to receive the first response to initialize
	// the watch stream. runWatchLoop should be blocked until the first response is
	// received.
	//
	// A failure below leaves the attachment's pump and sender alone: they
	// belong to the attachment, so the document keeps a consumer for its
	// events whether or not this stream ever came up. Receive reporting false
	// carries the RPC failure -- permission denied, unavailable -- on
	// stream.Err(); ErrInitNotReceived is only for a stream that ended
	// cleanly without sending its initialization.
	if !stream.Receive() {
		if err := stream.Err(); err != nil {
			return err
		}
		return ErrInitNotReceived
	}
	if _, err := handleWatchResponse(stream.Msg(), d); err != nil {
		return err
	}
	if err = stream.Err(); err != nil {
		return err
	}

	// stream reader: server responses -> buf. Registered with the attachment
	// so stopWatchPipeline can wait for it; a reconnect registers its
	// successor from inside this goroutine, before this one returns, so the
	// counter never dips to zero across the handover.
	attachment.watchReaders.Go(func() {
		for stream.Receive() {
			pbResp := stream.Msg()
			resp, err := handleWatchResponse(pbResp, d)
			if err != nil {
				// Terminal: no reconnect follows, so the buffer is closed and
				// the consumer sees the error and then end of stream. The pump
				// keeps draining the document until the watch context is
				// cancelled, so publishers never wedge on a dead stream.
				buf.push(WatchDocResponse{Err: err})
				buf.close()
				return
			}
			if resp == nil {
				continue
			}

			// Set remote change event flag when document change is received.
			// Without syncMu: this goroutine is what a teardown holding
			// syncMu waits for, so taking the lock here would deadlock.
			if resp.Type == DocumentChanged {
				if attachment, ok := c.attachments.Get(d.Key()); ok {
					attachment.changeEventReceived.Store(true)
				}
			}

			buf.push(*resp)
		}

		if err := stream.Err(); err != nil {
			buf.push(WatchDocResponse{Err: err})

			// If watch stream is disconnected, we re-establish the watch
			// stream. The buffer stays open and the pump keeps running across
			// the handshake: they belong to the attachment, so the document
			// has a consumer for the whole reconnect and the consumer keeps
			// the same response channel. Only a reconnect that fails ends the
			// stream for the consumer.
			if err := c.runWatchLoop(ctx, d); err != nil {
				c.logger.Warn(fmt.Sprintf("re-establish watch stream: %v", err))
				buf.close()
			}
			return
		}
		buf.close()
	})

	return nil
}

func handleWatchResponse(pbResp *api.WatchResponse, d *document.Document) (*WatchDocResponse, error) {
	switch body := pbResp.Body.(type) {
	case *api.WatchResponse_Initialization:
		for _, init := range body.Initialization.ResourceInits {
			switch ri := init.Init.(type) {
			case *api.ResourceInit_DocumentInit:
				var clientIDs []string
				for _, clientID := range ri.DocumentInit.ClientIds {
					id, err := time.ActorIDFromHex(clientID)
					if err != nil {
						return nil, err
					}
					clientIDs = append(clientIDs, id.String())
				}
				d.SetOnlineClients(clientIDs...)
			}
		}
		return nil, nil
	case *api.WatchResponse_Event:
		switch we := body.Event.Event.(type) {
		case *api.WatchEvent_DocEvent:
			eventType, err := converter.FromEventType(we.DocEvent.Event.Type)
			if err != nil {
				return nil, err
			}

			cli, err := time.ActorIDFromHex(we.DocEvent.Event.Publisher)
			if err != nil {
				return nil, err
			}

			switch eventType {
			case events.DocChanged:
				return &WatchDocResponse{Type: DocumentChanged}, nil
			case events.DocWatched:
				// NOTE(hackerwins): The reconciled event is emitted through
				// the document event channel and forwarded by the event
				// goroutine in runWatchLoop, keeping presence lifecycle
				// events on a single ordered path (yorkie#1847).
				d.AddOnlineClientAndReconcile(cli.String())
				return nil, nil
			case events.DocUnwatched:
				d.RemoveOnlineClientAndReconcile(cli.String())
				return nil, nil
			}
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
			DisableGc:  attachment.disableGC,
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
		// The pipeline is owned by the attachment, not by the watch loop, so
		// dropping the attachment is not enough: without cancelling watchCtx
		// the pump and the sender would outlive the removed document. Cancel
		// after ApplyChangePack, as Detach does, so the pump is still
		// consuming Document.Events while the final pack is applied. syncMu
		// is held by our caller, syncInternal, for the whole of that.
		stopWatchPipeline(attachment)
	}

	return nil
}

// Remove removes the given document.
func (c *Client) Remove(ctx context.Context, d *document.Document) error {
	if c.status != statusActivated {
		return ErrNotActivated
	}

	// As in Detach, the attachment is looked up by key, so it must also be
	// held by d itself. Another document with the same key, one rejected by
	// the attach guard or a stale one from before a deactivation, would
	// otherwise send the attached document's ID and remove it on the server.
	attachment, ok := c.attachments.Get(d.Key())
	if !ok || attachment.resource != d {
		return ErrNotAttached
	}

	// Held for the same reason Detach holds it: the removal ends by tearing
	// the delivery pipeline down, and a sync running concurrently would
	// otherwise lose the consumer of Document.Events part way through its own
	// ApplyChangePack and wedge on the next event it publishes.
	attachment.syncMu.Lock()
	defer attachment.syncMu.Unlock()

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
		// See pushPullChanges: the attachment owns the pipeline, so the
		// removal has to cancel watchCtx or the pump and the sender leak.
		// Deleted from the map first so a stream reader still winding down
		// stops recording change events against a removed document.
		stopWatchPipeline(attachment)
	}

	return nil
}

func (c *Client) broadcast(
	ctx context.Context,
	ch *channel.Channel,
	topic string,
	payload []byte,
) error {
	if c.status != statusActivated {
		return ErrNotActivated
	}

	// Held by ch itself, not merely keyed by ch.Key(), so a rejected or stale
	// channel cannot publish through another channel's attachment.
	attachment, ok := c.attachments.Get(ch.Key())
	if !ok || attachment.resource != ch {
		return ErrNotAttached
	}

	_, err := c.client.Broadcast(
		ctx,
		withShardKey(connect.NewRequest(&api.BroadcastRequest{
			ClientId:   c.id.String(),
			ChannelKey: ch.Key().String(),
			Topic:      topic,
			Payload:    payload,
		}), c.options.APIKey, ch.FirstKeyPath()))
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
