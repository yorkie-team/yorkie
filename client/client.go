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
	"sync/atomic"
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

	// statusDeactivating is the state of a client that has begun deactivating
	// but has not finished: its watch pipelines are being, or have been, torn
	// down, so nothing that needs one may start. Every guard spelled
	// `!= statusActivated` therefore rejects it -- Attach and its
	// registerAttachment, and lockLiveAttachment, which Detach, Remove and
	// every sync re-check under the attachment's syncMu -- and Activate
	// refuses to lay a new session over the one being ended.
	//
	// A client whose DeactivateClient RPC failed stays here rather than going
	// back to statusActivated. The pipelines retired on the way in do not come
	// back, so a client returned to statusActivated would let the sync loop and
	// Client.Sync apply packs into documents whose pump is gone, and the first
	// event ApplyChangePack publishes would block forever on the document's
	// capacity-one event channel with the event mutex held, taking every other
	// publisher with it.
	//
	// There are two ways out, and the state is not a dead end. A Deactivate
	// that succeeds -- which this state, unlike statusDeactivated, still lets
	// through -- ends the server-side session too, and is the right answer to a
	// transient failure. A failure that will never clear, such as a session the
	// server has already dropped, leaves Close: it gives up on the session and
	// finishes the local deactivation itself, so the client ends deactivated
	// and can be activated again under a new ID.
	statusDeactivating
)

var (
	// ErrNotActivated occurs when an inactive client executes a function
	// that can only be executed when activated.
	ErrNotActivated = errors.FailedPrecond("client is not activated")

	// ErrDeactivating occurs when a client that has begun deactivating is
	// asked to activate again. The deactivation has already retired the watch
	// pipelines and they do not come back, so the client has to finish
	// deactivating -- retrying Deactivate until it succeeds, or Close when the
	// failure will never clear -- before a new session can be opened over it.
	ErrDeactivating = errors.FailedPrecond("client is deactivating")

	// ErrNotAttached occurs when the given resource is not attached to this client.
	ErrNotAttached = errors.FailedPrecond("resource is not attached")

	// ErrNotDetached occurs when the given resource is not detached.
	ErrNotDetached = errors.FailedPrecond("resource is not detached")

	// ErrAlreadyAttached occurs when a resource with the same key is already
	// attached to, or being attached by, this client.
	ErrAlreadyAttached = errors.FailedPrecond("resource with the key is already attached").
				WithCode("ErrAlreadyAttached")

	// ErrAlreadyWatching occurs when the given resource is already being
	// watched by this client: a second watch would share the first one's
	// broadcast requests and retire its servicer.
	ErrAlreadyWatching = errors.FailedPrecond("resource is already being watched").
				WithCode("ErrAlreadyWatching")

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

	// id is the actor ID the server handed back to Activate. Activate writes it
	// on the caller's goroutine while requests already in flight on other
	// goroutines read it -- an Attach that straddles a Deactivate and a new
	// Activate is exactly that, and is the case the generation counter below
	// guards -- so it is published atomically rather than as a plain field.
	// loadID reads it; its zero value is the zero ActorID.
	id atomic.Pointer[time.ActorID]

	key string

	// status is written by Activate and Deactivate on the caller's goroutine
	// and read by every guard that rejects an inactive client, including the
	// sync loop's and a user goroutine's pushPullChanges. The deactivating
	// window only keeps a concurrent sync out if that write is visible to it,
	// so the field is accessed atomically rather than plainly. Its zero value
	// is statusDeactivated.
	status atomic.Int32

	attachments *cmap.Map[key.Key, *Attachment]

	// attaching holds the keys of documents with an attach in flight.
	// attachments is only set once the attach round trip resolves, so this
	// is what rejects a concurrent attach of the same key.
	//
	// attachingMu also orders attachment registration against deactivation:
	// registerAttachment checks the status and publishes the attachment under
	// it, and beginDeactivation leaves statusActivated under it. Every
	// attachment is therefore either registered before Deactivate walks
	// c.attachments -- and retired by that walk -- or rejected.
	attachingMu sync.Mutex
	attaching   map[key.Key]struct{}

	// lifecycleMu serializes Activate, Deactivate and Close's local finish.
	// Each is a sequence of status transitions around an RPC, not a single
	// store: two of them interleaving would let one caller's write land after
	// the other has already moved on -- a Deactivate that returns as a no-op
	// while a concurrent Activate leaves the client activated, or a deactivated
	// client put back into the deactivating window.
	lifecycleMu sync.Mutex

	// generation counts activations. Attach records it before its round trip
	// and registers the attachment only if it is unchanged, so an Attach that
	// straddles a Deactivate and a new Activate cannot hang the old session's
	// attachment on the new one.
	generation atomic.Uint64

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

// Close closes all resources of this client. It is the terminal disposal of a
// client and always leaves it deactivated locally, whatever the server says:
// when DeactivateClient fails for good -- the session is already gone, the
// credentials no longer pass -- retrying Deactivate cannot clear the
// deactivating window, so Close finishes the local half of the deactivation
// itself rather than leaving a client no call can use and a connection no call
// can release. The DeactivateClient error is still returned, so a caller that
// wants the server-side session provably ended can see that it was not; the
// server reaps the leftover session through housekeeping.
func (c *Client) Close() error {
	err := c.Deactivate(context.Background())
	if err != nil {
		c.finishDeactivation()
	}

	c.conn.CloseIdleConnections()

	return err
}

// loadStatus returns the current status of this client.
func (c *Client) loadStatus() status {
	return status(c.status.Load())
}

// storeStatus sets the status of this client.
func (c *Client) storeStatus(s status) {
	c.status.Store(int32(s))
}

// Activate activates this client. That is, it registers itself to the server
// and receives a unique ID from the server. The given ID is used to distinguish
// different clients.
//
// It returns ErrDeactivating while a deactivation of this client is unfinished:
// that deactivation has already retired the watch pipelines and is ending the
// server-side session, so activating over it would hand the client a new ID
// while the ended session's attachments are still in c.attachments, leaving the
// sync loop to push resources the new ID never attached, over pipelines that no
// longer deliver. A deactivation whose RPC failed is also unfinished -- the
// retired pipelines do not come back -- so the caller's next move there is to
// retry Deactivate, or Close when that failure will never clear, not Activate.
func (c *Client) Activate(ctx context.Context) error {
	// A deactivation in progress holds lifecycleMu across its RPC; report it
	// at once rather than queueing behind it.
	if c.loadStatus() == statusDeactivating {
		return ErrDeactivating
	}

	// Serialized against Deactivate and Close: without it, a Deactivate that
	// runs while ActivateClient is in flight reads statusDeactivated, returns
	// nil as a no-op, and the client then ends up activated with a live
	// server-side session and a running sync loop that the caller believes it
	// has just ended. The status is re-read under the lock.
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	switch c.loadStatus() {
	case statusActivated:
		return nil
	case statusDeactivating:
		return ErrDeactivating
	case statusDeactivated:
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

	c.id.Store(&clientID)
	c.generation.Add(1)
	c.storeStatus(statusActivated)

	// Under lifecycleMu, so no Deactivate can observe statusActivated and
	// cancel syncCancel before this assigns it.
	c.runSyncLoop(ctx)

	return nil
}

// Deactivate deactivates this client.
func (c *Client) Deactivate(ctx context.Context, opts ...DeactivateOption) error {
	// Serialized against another Deactivate, Activate and Close: the
	// transitions below are a sequence, and the status a second caller
	// observed before taking the lock may no longer hold. Re-read it here so a
	// call that arrives after the session is already ended is the no-op it
	// should be.
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	if c.loadStatus() == statusDeactivated {
		return nil
	}

	deactiveOpts := &DeactivateOptions{}
	for _, opt := range opts {
		opt(deactiveOpts)
	}

	c.beginDeactivation()

	_, err := c.client.DeactivateClient(
		ctx,
		withShardKey(connect.NewRequest(&api.DeactivateClientRequest{
			ClientId:    c.loadID().String(),
			Synchronous: !deactiveOpts.Asynchronous,
		}), c.options.APIKey, c.key))
	if err != nil {
		// The server-side session outlived the call, so this client is not
		// deactivated -- but it is not usable either, because the watch
		// pipelines retired above do not come back. Stay deactivating rather
		// than restoring statusActivated: a client returned to activated would
		// re-open the sync loop and Client.Sync on attachments that are still
		// registered and still StatusAttached, over pipelines that no longer
		// drain Document.Events. Here, every guard keeps rejecting the client
		// and Activate returns ErrDeactivating, while Deactivate itself still
		// runs, so the caller retries it until the session is gone. A failure
		// that will never clear -- a session the server already dropped,
		// credentials it no longer accepts -- is not a dead end either: Close
		// gives up on the session and finishes the local deactivation, leaving
		// a clean deactivated client.
		return err
	}

	c.dropAttachments()
	c.storeStatus(statusDeactivated)

	return nil
}

// beginDeactivation is the local first half of every deactivation: it stops
// the sync loop, leaves statusActivated, and retires every attachment's watch
// pipeline. Deactivate runs it before its RPC and Close's finishDeactivation
// runs it when that RPC never succeeded; it is idempotent, so a retried
// Deactivate runs it again harmlessly. The caller holds lifecycleMu.
//
// The order is what the pipeline invariant (see lockLiveAttachment) rests on:
//
//  1. The status leaves statusActivated under attachingMu, so an Attach still
//     in flight either registered its attachment before this point -- and the
//     walk below retires it -- or is rejected by registerAttachment.
//  2. Each pipeline is then retired under its attachment's syncMu. A path
//     that already holds syncMu and has passed lockLiveAttachment finishes its
//     ApplyChangePack with the pump still draining; any path that takes syncMu
//     afterwards re-reads the status under it and backs out.
func (c *Client) beginDeactivation() {
	// The sync loop first, so it is not left retrying syncs the status change
	// below would only reject.
	if c.syncCancel != nil {
		c.syncCancel()
		c.syncLoopWg.Wait()
	}

	c.attachingMu.Lock()
	c.storeStatus(statusDeactivating)
	c.attachingMu.Unlock()

	for _, attachment := range c.attachments.Values() {
		attachment.syncMu.Lock()
		stopWatchPipeline(attachment)
		attachment.syncMu.Unlock()
	}
}

// dropAttachments marks every attached resource detached, as the JS SDK does,
// and drops its attachment. The attachment holds the resource ID of an ended
// session, so keeping it would let the sync loop -- which reads c.attachments
// and ignores resource status -- push it again the moment this client is
// activated anew, and would let Detach, Remove or WatchStream address a
// server-side attachment this client no longer holds. Dropping it leaves the
// resource exactly where a plain Detach leaves it: detached, with no
// attachment, free to be attached again.
//
// Callers must have run beginDeactivation first, which retires every
// attachment's watch pipeline and keeps new attachments from registering.
func (c *Client) dropAttachments() {
	for _, attachment := range c.attachments.Values() {
		if attachment.resource.Status() != attachable.StatusRemoved {
			attachment.resource.SetStatus(attachable.StatusDetached)
		}
		if ch, ok := attachment.resource.(*channel.Channel); ok {
			ch.UpdateSessionCount(0, 0)
		}
		c.attachments.Delete(attachment.resource.Key())
	}
}

// finishDeactivation completes the local half of a deactivation whose
// DeactivateClient RPC never succeeded, and is how a client leaves the
// deactivating window when retrying cannot: a session the server has already
// dropped, or credentials it no longer accepts, fails the same way however
// often it is retried, and the client would otherwise stay in a state every
// guard rejects for the rest of the process.
//
// Only Close calls it, because it gives up on ending the server-side session:
// what it does locally is exactly what Deactivate does around a successful RPC
// -- beginDeactivation, then dropAttachments -- so the client it leaves behind
// is a clean deactivated one, safe to activate again under a new ID. The
// session left on the server is reaped by housekeeping.
func (c *Client) finishDeactivation() {
	// Under the same lock Deactivate uses, and re-reading the status under it:
	// a Deactivate that succeeded while we waited has already done all of this.
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	if c.loadStatus() == statusDeactivated {
		return
	}

	c.beginDeactivation()
	c.dropAttachments()
	c.storeStatus(statusDeactivated)
}

// registerAttachment publishes the given attachment under k, provided the
// client is still in the activation the attach started in. It fails with
// ErrNotActivated otherwise, and the caller -- which still owns the attachment
// alone -- must undo the attach locally.
//
// The check and the publication happen together under attachingMu, the lock
// beginDeactivation leaves statusActivated under. Without it an Attach that
// passed its status guard before a Deactivate could register after that
// Deactivate walked c.attachments: its pipeline would never be retired and the
// attachment would outlive the session that holds it on the server.
func (c *Client) registerAttachment(k key.Key, attachment *Attachment, generation uint64) error {
	c.attachingMu.Lock()
	defer c.attachingMu.Unlock()

	if c.loadStatus() != statusActivated || c.generation.Load() != generation {
		return ErrNotActivated
	}

	c.attachments.Set(k, attachment)
	return nil
}

// lockLiveAttachment takes the attachment's syncMu and returns once it holds
// it with the attachment still live: the client is activated and the
// attachment is still the one registered under its key. On failure the lock
// is not held. On success the caller must unlock syncMu.
//
// Every path that applies a change pack to an attached document, or tears an
// attachment down -- Detach, Remove, and syncInternal for Client.Sync and the
// sync loop alike -- goes through here, and that is what makes them safe
// against Deactivate. The invariant is:
//
//	An attachment's watch pipeline is retired only while its syncMu is held,
//	and only after the attachment has been removed from c.attachments or the
//	client has left statusActivated.
//
// Detach, Remove and a sync that observes removal delete the attachment and
// then stop the pipeline, all under syncMu; beginDeactivation leaves
// statusActivated and then stops each pipeline under syncMu. So a goroutine
// that holds syncMu and sees both conditions still true knows the pump is
// draining Document.Events, and keeps knowing it until it releases syncMu.
// That matters because Document.publish is an unconditional send on a
// capacity-one channel made under the document's event mutex: an
// ApplyChangePack whose pack carries two or more events with no pump would
// block forever and take every other publisher of the document with it.
//
// The check has to run after the lock is taken. A status read before it is
// stale by the time the lock is held: Deactivate may have retired the pipeline
// in between, which is exactly the hole a status check outside syncMu leaves.
func (c *Client) lockLiveAttachment(attachment *Attachment) error {
	attachment.syncMu.Lock()

	if c.loadStatus() != statusActivated {
		attachment.syncMu.Unlock()
		return ErrNotActivated
	}
	if current, ok := c.attachments.Get(attachment.resource.Key()); !ok || current != attachment {
		attachment.syncMu.Unlock()
		return ErrNotAttached
	}

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
	// The sync loop works from a snapshot of c.attachments and Client.Sync
	// looks the attachment up before calling here, so the attachment may have
	// been detached, removed or deactivated since. lockLiveAttachment re-checks
	// that under syncMu; see it for why the pump is then guaranteed.
	if err := c.lockLiveAttachment(attachment); err != nil {
		return err
	}
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
		// failure so a pending remote change still forces the next sync. A
		// push-only sync pulls nothing, so it leaves a remote change it was
		// told about waiting for the next pull.
		pending := options.mode != types.SyncModePushOnly &&
			attachment.changeEventReceived.Swap(false)
		if err := c.pushPullChanges(ctx, attachment, d, options); err != nil {
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

	if err := c.refreshChannel(ctx, attachment, p); err != nil {
		return err
	}

	attachment.lastSyncTime = gotime.Now()

	return nil
}

// AttachResource attaches the given resource to this client.
// This is a generalized version of Attach that works with any Attachable resource.
func (c *Client) Attach(ctx context.Context, r attachable.Attachable, opts ...any) error {
	if c.loadStatus() != statusActivated {
		return ErrNotActivated
	}
	// Read after the status check: Activate bumps the generation before it
	// stores statusActivated, so a status that reads activated is never paired
	// with the generation before it. registerAttachment refuses to publish
	// the attachment into any later activation.
	generation := c.generation.Load()
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

	r.SetActor(c.loadID())

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

		return c.attachDocument(ctx, d, attachOpts, generation)

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

	return c.attachChannel(ctx, p, attachChannelOpts, generation)
}

// Detach detaches the given resource from this client.
// This is a generalized version of Detach that works with any Attachable resource.
func (c *Client) Detach(ctx context.Context, r attachable.Attachable, opts ...any) error {
	if c.loadStatus() != statusActivated {
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

	// The checks above are only a fast path: they ran without syncMu, so a
	// Deactivate, a Remove or a sync that observed removal may retire the
	// pipeline before the lock is ours. lockLiveAttachment repeats them under
	// syncMu, which is what keeps the final ApplyChangePack below from running
	// with no pump draining Document.Events.
	if err := c.lockLiveAttachment(attachment); err != nil {
		return err
	}
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

		if err := c.detachDocument(ctx, attachment, d, detachOpts); err != nil {
			return err
		}
	} else {
		p, ok := r.(*channel.Channel)
		if !ok {
			return ErrInvalidResource
		}

		if err := c.detachChannel(ctx, attachment, p); err != nil {
			return err
		}
	}

	// Keep the watch pipeline alive while applying the final ChangePack. Its
	// event pump is the sole consumer of Document.Events, so stopping it first
	// can leave ApplyChangePack blocked when the pack emits multiple events.
	// The attachment is already out of c.attachments and syncMu is still
	// held, so this retirement keeps the invariant lockLiveAttachment relies
	// on. For a channel it ends any WatchChannel stream tied to the
	// attachment.
	stopWatchPipeline(attachment)

	return nil
}

// beginAttach marks the key k as being attached. It fails with
// ErrAlreadyAttached when a resource with k is already attached to, or being
// attached by, this client.
func (c *Client) beginAttach(k key.Key) error {
	stale, err := c.markAttaching(k)
	if err != nil {
		return err
	}

	// A stale attachment is out of c.attachments by now; retire its pipeline
	// under its syncMu, as every other teardown does (see
	// lockLiveAttachment), so a sync still holding it finishes first and its
	// pump does not outlive it. Outside attachingMu, so a long-running sync on
	// the stale attachment does not hold up attaches of other keys.
	if stale != nil {
		stale.syncMu.Lock()
		stopWatchPipeline(stale)
		stale.syncMu.Unlock()
	}

	return nil
}

// markAttaching is beginAttach's critical section. It returns the stale
// attachment it dropped from c.attachments, if any, for the caller to retire.
func (c *Client) markAttaching(k key.Key) (*Attachment, error) {
	c.attachingMu.Lock()
	defer c.attachingMu.Unlock()

	if _, ok := c.attaching[k]; ok {
		return nil, fmt.Errorf("attach %s: %w", k, ErrAlreadyAttached)
	}

	var stale *Attachment
	if attachment, ok := c.attachments.Get(k); ok {
		// An attachment whose resource is no longer attached -- one left by a
		// path that detached or removed the resource without clearing the
		// entry -- is stale, so drop it and let the key be used again.
		if attachment.resource.Status() == attachable.StatusAttached {
			return nil, fmt.Errorf("attach %s: %w", k, ErrAlreadyAttached)
		}
		c.attachments.Delete(k)
		stale = attachment
	}

	c.attaching[k] = struct{}{}
	return stale, nil
}

// endAttach clears the in-flight mark that beginAttach set for k.
func (c *Client) endAttach(k key.Key) {
	c.attachingMu.Lock()
	defer c.attachingMu.Unlock()

	delete(c.attaching, k)
}

// attachDocument attaches the given document to this client. It tells the server that
// this client will synchronize the given document.
func (c *Client) attachDocument(
	ctx context.Context,
	d *document.Document,
	opts *AttachOptions,
	generation uint64,
) error {
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
			ClientId:        c.loadID().String(),
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

	// 04. Build the attachment and bring its delivery pipeline up before the
	// attach pack is applied. ApplyChangePack publishes one event per applied
	// remote change onto the document's capacity-one event channel, under the
	// document's event mutex, and that send has no cancellation path: a pack
	// carrying two or more events applied with no pump draining them wedges
	// this goroutine inside Attach for good, holding the event mutex against
	// every other publisher. The pump is the consumer, so it has to exist
	// first.
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
		// Also before the attachment is published and before the watch stream
		// is opened: the first Receive blocks on the server, and a publisher
		// stalled on the document's event channel in the meantime holds the
		// event mutex against every other publisher.
		startWatchPipeline(watchCtx, attachment, d)
	}

	if err := d.ApplyChangePack(pack); err != nil {
		// The attachment was never published, so nothing else will ever tear
		// its pipeline down. stopWatchPipeline also runs cancelFunc, which is
		// what releases watchCtx on the non-realtime path.
		stopWatchPipeline(attachment)
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
		stopWatchPipeline(attachment)
		return nil
	}
	d.SetStatus(attachable.StatusAttached)

	if opts.IsRealtime {
		// Count the first handshake as a stream reader, and do it before the
		// attachment is published, so no Detach or Deactivate can be waiting
		// on watchReaders yet. A teardown that starts during the handshake
		// then waits for it: otherwise its Wait could return at zero, retire
		// the pump, and leave the reader the handshake registers afterwards
		// publishing into a channel nobody drains.
		attachment.watchReaders.Add(1)
	}
	if err := c.registerAttachment(d.Key(), attachment, generation); err != nil {
		// A Deactivate began while the round trip was in flight. It has
		// already walked c.attachments, or is about to with this attachment
		// kept out, so nothing but this goroutine will ever retire the
		// pipeline. The server-side attachment belongs to the session that
		// Deactivate is ending, which detaches it there.
		if opts.IsRealtime {
			attachment.watchReaders.Done()
		}
		stopWatchPipeline(attachment)
		d.SetStatus(attachable.StatusDetached)
		return err
	}
	if opts.IsRealtime {
		err = c.runWatchLoop(watchCtx, attachment, d)
		attachment.watchReaders.Done()
		if err != nil {
			// AttachDocument has already succeeded, so the server holds the
			// attachment and rejects a second attach of it. Keep the local
			// attachment registered so the caller can Detach -- which tells
			// the server and tears the pipeline down -- and then attach
			// again. Only the stream is ended, as a terminal stream error
			// does: closing the buffer lets the sender close watchStream,
			// while the pump keeps draining Document.Events until Detach or
			// Deactivate tears the pipeline down in stopWatchPipeline, so a
			// sync applying a pack in the meantime never wedges on a channel
			// without a consumer.
			attachment.watchBuf.close()
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
//
// The caller holds attachment.syncMu, taken through lockLiveAttachment.
func (c *Client) detachDocument(
	ctx context.Context,
	attachment *Attachment,
	d *document.Document,
	opts *DetachOptions,
) error {
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
			ClientId:   c.loadID().String(),
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
func (c *Client) attachChannel(
	ctx context.Context,
	ch *channel.Channel,
	opts *AttachChannelOptions,
	generation uint64,
) error {
	res, err := c.client.AttachChannel(
		ctx,
		withShardKey(connect.NewRequest(&api.AttachChannelRequest{
			ClientId:   c.loadID().String(),
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

	// The channel has no event pump, but WatchChannel ties its stream and
	// broadcast goroutines to watchCtx, so the teardowns that retire a
	// document's pipeline -- Detach and Deactivate -- end those too.
	watchCtx, cancelFunc := context.WithCancel(context.Background())
	attachment := &Attachment{
		resource:         ch,
		resourceID:       types.ID(res.Msg.SessionId),
		watchCtx:         watchCtx,
		closeWatchStream: cancelFunc,
		syncMode:         syncMode,
		lastSyncTime:     gotime.Now(),
	}
	if err := c.registerAttachment(ch.Key(), attachment, generation); err != nil {
		// See attachDocument: a Deactivate that began during the round trip
		// ends the server-side session this channel was attached under.
		cancelFunc()
		ch.SetStatus(attachable.StatusDetached)
		return err
	}

	// Update initial session count from attach response
	ch.UpdateSessionCount(res.Msg.SessionCount, 0)

	return nil
}

// refreshChannel refreshes the TTL of the given channel and returns the current session count.
//
// The caller, syncInternal, holds attachment.syncMu taken through
// lockLiveAttachment, which has already rejected a client that is not
// activated: Client.Sync is callable straight from a user goroutine, so a
// channel sync can arrive after Deactivate has marked the client deactivating,
// and refreshing the TTL of a session being torn down -- or of one this client
// no longer holds -- is what the deactivating window exists to reject.
func (c *Client) refreshChannel(ctx context.Context, attachment *Attachment, ch *channel.Channel) error {
	res, err := c.client.RefreshChannel(
		ctx,
		withShardKey(connect.NewRequest(&api.RefreshChannelRequest{
			ClientId:   c.loadID().String(),
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
// The caller holds attachment.syncMu, taken through lockLiveAttachment.
func (c *Client) detachChannel(ctx context.Context, attachment *Attachment, ch *channel.Channel) error {
	_, err := c.client.DetachChannel(
		ctx,
		withShardKey(connect.NewRequest(&api.DetachChannelRequest{
			ClientId:   c.loadID().String(),
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
//
// The watch also ends when the channel is detached or the client deactivated:
// its context is tied to the attachment's, so neither the stream nor the
// broadcast goroutine outlives the session it was opened under.
func (c *Client) WatchChannel(ctx context.Context, ch *channel.Channel) (<-chan int64, func(), error) {
	attachment, ok := c.attachments.Get(ch.Key())
	if !ok || attachment.resource != ch {
		return nil, nil, ErrNotAttached
	}

	if c.loadStatus() != statusActivated {
		return nil, nil, ErrNotActivated
	}

	if ch.Status() != attachable.StatusAttached {
		return nil, nil, fmt.Errorf("channel must be attached before watching")
	}

	// Claim the channel's broadcast requests before opening anything. Two
	// watches of the same channel would otherwise read from the same request
	// queue -- a broadcast answered by whichever won the race -- and the first
	// to retire would retire the other's servicer with it, leaving a live watch
	// that reports every Broadcast as unavailable. The claim is released by the
	// servicer below and by closeFunc, so a watch reopened after a close, a
	// detach or a deactivation is granted it again.
	servingToken, ok := ch.StartBroadcastServing()
	if !ok {
		return nil, nil, fmt.Errorf("watch %s: %w", ch.Key(), ErrAlreadyWatching)
	}

	// Create buffered channel for count updates
	countChan := make(chan int64, 10)

	// Create context for the watch stream, cancelled with the attachment's
	// too. An attachment built outside attachChannel has no watchCtx.
	watchCtx, cancel := context.WithCancel(ctx)
	unlink := func() bool { return false }
	if attachment.watchCtx != nil {
		unlink = context.AfterFunc(attachment.watchCtx, cancel)
	}

	// Start the watch stream using unified Watch RPC
	stream, err := c.openChannelWatch(watchCtx, ch)
	if err != nil {
		unlink()
		cancel()
		ch.StopBroadcastServing(servingToken)
		return nil, nil, err
	}

	// Start goroutine to handle stream messages
	go func() {
		defer close(countChan)
		defer cancel()
		defer unlink()

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

	// Releasing the claim here rather than only in the goroutine below keeps it
	// tied to the watch rather than to the goroutine's exit: a watch reopened
	// right after this returns is granted the claim even while the retiring
	// servicer is still winding down.
	closeFunc := func() {
		cancel()
		ch.StopBroadcastServing(servingToken)
	}

	// Start goroutine to handle broadcast requests. Channel.Broadcast waits for
	// this goroutine's answer, and this goroutine ends with watchCtx -- which
	// Detach and Deactivate cancel -- so the channel has to be told when it is
	// running and when it is gone; otherwise a Broadcast issued after one of
	// those teardowns would block forever.
	go func() {
		defer ch.StopBroadcastServing(servingToken)

		for {
			select {
			case r := <-servingToken.Requests():
				// The claim can be released between the send and this receive,
				// and the request then belongs to a caller that has already
				// been released with ErrBroadcastUnavailable. Skip the round
				// trip rather than broadcast on nobody's behalf.
				select {
				case <-servingToken.Done():
					return
				default:
				}

				err := c.broadcast(ctx, ch, r.Topic, r.Payload)
				// A broadcast that outlived its own watch answers nobody: the
				// caller has been released already, and the answer must not
				// reach the next watch's caller.
				if !ch.SendBroadcastResponse(servingToken, err) {
					return
				}
			case <-servingToken.Done():
				// closeFunc releases the claim before this goroutine notices
				// watchCtx, so stop on the claim too: a successor is already
				// serving, and requests still reachable from here are not its.
				return
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
			ClientId: c.loadID().String(),
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

// WatchStream returns the watch events of the given realtime attachment. The
// channel belongs to the attachment and is stable: a watch loop that
// re-establishes its stream keeps delivering on the same channel, so a
// consumer holding it does not have to re-read this field after a
// disconnect. A response carrying Err is therefore not terminal by itself:
// after a lost stream it is followed by the reconnected stream's events, and
// the channel is closed only once the stream ends for good -- a terminal
// error, a reconnect that fails, Detach, Remove or Deactivate.
//
// The returned CancelFunc ends the stream and closes the channel, but it does
// not stop the attachment's event pump: the pump keeps draining
// Document.Events, discarding what no one reads, until Detach, Remove or
// Deactivate retires the pipeline. It has to, because a sync may still apply
// a pack to the document, and that pack's events need a consumer.
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
//
// For a published attachment the caller must hold syncMu and must already
// have removed the attachment from c.attachments or moved the client out of
// statusActivated: that is the invariant lockLiveAttachment relies on. Only
// attachDocument, on an attachment it never published, calls this without.
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
// and keeps draining the document until stopWatchPipeline tears it down. If the
// context "watchCtx" is canceled or timed out, the response channel is closed, and
// "WatchResponse" from this closed channel has zero events and nil "Err()".
//
// The caller passes the attachment that owns the pipeline rather than letting
// runWatchLoop look it up by key: a reconnect must keep feeding the attachment
// it started with, never one a later Attach registered under the same key.
// The caller must also hold a watchReaders slot for the duration of the call,
// so the readers registered here never start after a teardown's Wait.
func (c *Client) runWatchLoop(ctx context.Context, attachment *Attachment, d *document.Document) error {
	buf := attachment.watchBuf
	if buf == nil {
		return ErrNotAttached
	}

	stream, err := c.client.Watch(
		ctx,
		withShardKey(connect.NewRequest(&api.WatchRequest{
			ClientId: c.loadID().String(),
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
				// keeps draining the document until stopWatchPipeline tears
				// it down, so publishers never wedge on a dead stream.
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
				attachment.changeEventReceived.Store(true)
			}

			buf.push(*resp)
		}

		// The stream ended because Detach, Remove or Deactivate cancelled the
		// watch context. That is the client's own teardown, not a lost
		// stream: end without reporting it or trying to reconnect.
		if ctx.Err() != nil {
			buf.close()
			return
		}

		if err := stream.Err(); err != nil {
			buf.push(WatchDocResponse{Err: err})

			// A client that has begun deactivating is about to cancel ctx
			// and retire this pipeline; opening a new Watch for a session
			// being ended is what the deactivating window rejects elsewhere.
			if c.loadStatus() != statusActivated {
				buf.close()
				return
			}

			// If watch stream is disconnected, we re-establish the watch
			// stream. The buffer stays open and the pump keeps running across
			// the handshake: they belong to the attachment, so the document
			// has a consumer for the whole reconnect and the consumer keeps
			// the same response channel. Only a reconnect that fails ends the
			// stream for the consumer.
			if err := c.runWatchLoop(ctx, attachment, d); err != nil {
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
	return c.loadID()
}

// loadID reads the actor ID published by the most recent Activate, or the zero
// ActorID when the client has never been activated.
func (c *Client) loadID() time.ActorID {
	if id := c.id.Load(); id != nil {
		return *id
	}

	var zero time.ActorID
	return zero
}

// Key returns the key of this client.
func (c *Client) Key() string {
	return c.key
}

// IsActive returns whether this client is active or not.
func (c *Client) IsActive() bool {
	return c.loadStatus() == statusActivated
}

// pushPullChanges pushes the changes of the document to the server and pulls the changes from the server.
//
// The caller, syncInternal, holds attachment.syncMu taken through
// lockLiveAttachment, so the attachment is the live one and its pump drains
// Document.Events for the whole call. It works on that attachment rather than
// re-reading c.attachments by key: an attachment registered under the same key
// by a later Attach is not covered by the syncMu held here.
func (c *Client) pushPullChanges(
	ctx context.Context,
	attachment *Attachment,
	d *document.Document,
	opt SyncOptions,
) error {
	pbChangePack, err := converter.ToChangePack(d.CreateChangePack())
	if err != nil {
		return err
	}

	res, err := c.client.PushPullChanges(
		ctx,
		withShardKey(connect.NewRequest(&api.PushPullChangesRequest{
			ClientId:   c.loadID().String(),
			DocumentId: attachment.resourceID.String(),
			ChangePack: pbChangePack,
			PushOnly:   opt.mode == types.SyncModePushOnly,
			DisableGc:  attachment.disableGC,
		}), c.options.APIKey, d.Key().String()))
	if err != nil {
		return err
	}

	pack, err := converter.FromChangePack(res.Msg.ChangePack)
	if err != nil {
		return err
	}

	// NOTE(chacha912): The reply to a push-only request is a push ack only and
	// must not reach GC; see "Push-only response" in
	// docs/design/garbage-collection.md. Judged by the mode the request was
	// sent in: it is the request that decided nothing was pulled.
	if opt.mode == types.SyncModePushOnly {
		d.AcknowledgePushedChanges(pack)
	} else if err := d.ApplyChangePack(pack); err != nil {
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
	if c.loadStatus() != statusActivated {
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

	// Taken for the same reasons Detach takes it: the removal ends by tearing
	// the delivery pipeline down, which a concurrent sync must not see part
	// way through its own ApplyChangePack, and the checks above ran without
	// syncMu, so a Deactivate may have retired the pipeline since.
	// lockLiveAttachment repeats them under the lock.
	if err := c.lockLiveAttachment(attachment); err != nil {
		return err
	}
	defer attachment.syncMu.Unlock()

	pbChangePack, err := converter.ToChangePack(d.CreateChangePack())
	if err != nil {
		return err
	}
	pbChangePack.IsRemoved = true

	res, err := c.client.RemoveDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.RemoveDocumentRequest{
			ClientId:   c.loadID().String(),
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
	if c.loadStatus() != statusActivated {
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
			ClientId:   c.loadID().String(),
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
