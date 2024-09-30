/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

// Package system provides the client implementation of Yorkie. It is used to
// connect to the server and attach documents.
package system

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

	"connectrpc.com/connect"
	"github.com/rs/xid"
	"golang.org/x/net/http2"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

type status int

const (
	deactivated status = iota
	activated
)

var (
	// ErrDocumentNotAttached occurs when the given document is not attached to
	// this client.
	ErrDocumentNotAttached = errors.New("document is not attached")
)

// Attachment represents the document attached.
type Attachment struct {
	doc   *document.Document
	docID types.ID

	closeWatchStream context.CancelFunc
}

// Client is TODO.
type Client struct {
	conn          *http.Client
	client        v1connect.SystemServiceClient
	options       Options
	clientOptions []connect.ClientOption

	id          *time.ActorID
	key         string
	status      status
	attachments map[key.Key]*Attachment
}

// New creates TODO.
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

	return &Client{
		conn:          conn,
		clientOptions: clientOptions,
		options:       options,

		id:          options.ActorID,
		key:         k,
		status:      activated,
		attachments: make(map[key.Key]*Attachment),
	}, nil
}

// Dial creates TODO.
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

// Dial creates TODO.
func (c *Client) Dial(rpcAddr string) error {
	if !strings.Contains(rpcAddr, "://") {
		if c.conn.Transport == nil {
			rpcAddr = "http://" + rpcAddr
		} else {
			rpcAddr = "https://" + rpcAddr
		}
	}

	c.client = v1connect.NewSystemServiceClient(c.conn, rpcAddr, c.clientOptions...)

	return nil
}

// PretendAttach sets the document as attached without actual activation.
// This method is used for server-side client deactivate.
func (c *Client) PretendAttach(ctx context.Context, doc *document.Document, docID types.ID) {
	_, cancelFunc := context.WithCancel(ctx)
	c.attachments[doc.Key()] = &Attachment{
		doc:              doc,
		docID:            docID,
		closeWatchStream: cancelFunc,
	}
}

// pretendActivate sets the client as activated without actual activation.
// This method is used for server-side client deactivate.
func (c *Client) pretendActivate(actorID *time.ActorID) {
	c.id = actorID
	c.status = activated
}

// NewMockClient creates TODO.
func NewMockClient(
	clientInfo *database.ClientInfo,
	prjPublicKey,
	rpcAddr string,
	token string) (*Client, error) {
	actorID, err := clientInfo.ID.ToActorID()
	if err != nil {
		return nil, err
	}

	cli, err := Dial(rpcAddr,
		WithKey(clientInfo.Key),
		WithAPIKey(prjPublicKey),
		WithActorID(actorID),
		WithToken(token),
	)
	if err != nil {
		return nil, err
	}

	if err := cli.Dial(rpcAddr); err != nil {
		return nil, err
	}

	return cli, nil
}

// Detach detaches TODO.
func (c *Client) Detach(ctx context.Context, doc *document.Document) error {
	attachment, ok := c.attachments[doc.Key()]
	if !ok {
		return ErrDocumentNotAttached
	}

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

	// TODO(raararaara): We need to revert the presence clearing from the local
	// changes, if the server fails to detach the document.
	res, err := c.client.DetachDocument(
		ctx,
		withShardKey(connect.NewRequest(&api.DetachDocumentRequest{
			ClientId:   c.id.String(),
			DocumentId: attachment.docID.String(),
			ChangePack: pbChangePack,
			//RemoveIfNotAttached: opts.removeIfNotAttached,
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
